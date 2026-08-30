// Package service 实现核心业务服务。
package service

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"edt/internal/config"
)

// ConfigEntry 解析后的配置项，供前端使用。
type ConfigEntry struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`      // 可为空（DIRECT）
	YxIP   string `json:"yx_ip"`   // 可为空
	YxHost string `json:"yx_host"` // 可为空
	YxPort int    `json:"yx_port"` // 0 表示无端口

	lineNumber int // 源文件行号（1-based）
	rawIP      string
	rawYxIP    string
}

// ConfigStore 管理 vless.txt 文件的读写。
type ConfigStore struct {
	mu        sync.Mutex // 保护文件读写，防止并发数据竞争
	filePath  string
	nrtFile   string
	variables map[string]config.VariableSource
}

// NewConfigStore 构造 ConfigStore。
func NewConfigStore(filePath, nrtFile string, variables map[string]config.VariableSource) *ConfigStore {
	// 确保数据目录存在（空数据部署时避免写入 500）
	if dir := filepath.Dir(filePath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if dir := filepath.Dir(nrtFile); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return &ConfigStore{
		filePath:  filePath,
		nrtFile:   nrtFile,
		variables: variables,
	}
}

// FilePath 返回当前管理的文件路径。
func (c *ConfigStore) FilePath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.filePath
}

// Variables 返回变量表。
func (c *ConfigStore) Variables() map[string]config.VariableSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.variables
}

// Reload 热更新配置（配置重载时调用）：替换文件路径与变量表，
// 与构造函数同样保证新路径的父目录存在。
func (c *ConfigStore) Reload(filePath, nrtFile string, variables map[string]config.VariableSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dir := filepath.Dir(filePath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if dir := filepath.Dir(nrtFile); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	c.filePath = filePath
	c.nrtFile = nrtFile
	c.variables = variables
}

// FindDuplicate 查找与给定 ip 或 name 匹配的现有节点，返回可见行号（1-based）。
// ip 或 name 任一匹配即视为重复。未找到返回 0。
func (c *ConfigStore) FindDuplicate(ip, name string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.parseUnlocked()
	if err != nil {
		return 0, err
	}
	for i, e := range entries {
		if ip != "" && e.IP == ip {
			return i + 1, nil
		}
		if name != "" && e.Name == name {
			return i + 1, nil
		}
	}
	return 0, nil
}

// BatchDelete 批量删除多个可见行号。
// 内部按源文件行号降序删除，避免行号错乱。返回成功/失败计数。
func (c *ConfigStore) BatchDelete(lines []int) (success, failed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 解析一次获取 visible line -> 源文件 line 映射
	entries, err := c.parseUnlocked()
	if err != nil {
		return 0, len(lines)
	}
	sourceLines, failed := collectSourceLines(entries, lines)
	if len(sourceLines) == 0 {
		return
	}
	success, failed, err = c.deleteSourceLines(sourceLines, len(lines))
	if err != nil {
		return 0, len(lines)
	}
	return
}

// collectSourceLines 将可见行号映射为源文件行号（越界计为失败）。
func collectSourceLines(entries []ConfigEntry, visibleLines []int) (sourceLines []int, failed int) {
	for _, vl := range visibleLines {
		if vl < 1 || vl > len(entries) {
			failed++
			continue
		}
		sourceLines = append(sourceLines, entries[vl-1].lineNumber)
	}
	return sourceLines, failed
}

// deleteSourceLines 按源行号降序逐个删除并写回（含末尾换行归一化）。
// total 用于整体失败时返回准确的失败总数。
func (c *ConfigStore) deleteSourceLines(sourceLines []int, total int) (success, failed int, err error) {
	sort.Slice(sourceLines, func(i, j int) bool { return sourceLines[i] > sourceLines[j] }) // 降序

	allLines, err := c.readLines()
	if err != nil {
		return 0, total, err
	}
	for _, srcLine := range sourceLines {
		if srcLine < 1 || srcLine > len(allLines) {
			failed++
			continue
		}
		allLines = append(allLines[:srcLine-1], allLines[srcLine:]...)
		success++
	}
	if err := c.writeNormalized(allLines); err != nil {
		return 0, total, err
	}
	return success, failed, nil
}

// writeNormalized 去掉末尾空行/尾换行后整体写回。
func (c *ConfigStore) writeNormalized(lines []string) error {
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines[len(lines)-1] = strings.TrimRight(lines[len(lines)-1], "\n")
	}
	return os.WriteFile(c.filePath, []byte(strings.Join(lines, "")), 0o644)
}

// MoveTo 把 visibleLine 移动到 targetLine 的位置（插入式）。
// visibleLine 和 targetLine 都是 1-based 可见行号。
// 移动后，原 visibleLine 行被删除，插入到 targetLine 之前。
func (c *ConfigStore) MoveTo(visibleLine, targetLine int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.parseUnlocked()
	if err != nil {
		return err
	}
	if visibleLine < 1 || visibleLine > len(entries) {
		return ErrOutOfRange
	}
	if targetLine < 1 || targetLine > len(entries) {
		return ErrOutOfRange
	}
	if visibleLine == targetLine {
		return nil
	}
	// 收集所有可见行对应的源行号（升序）
	srcLines := make([]int, 0, len(entries))
	for _, e := range entries {
		srcLines = append(srcLines, e.lineNumber)
	}
	// 读取所有原始行并重排
	allLines, err := c.readLines()
	if err != nil {
		return err
	}
	newLines := reorderLines(allLines, srcLines[visibleLine-1], srcLines[targetLine-1])
	// 规范化末尾换行
	if len(newLines) > 0 {
		newLines[len(newLines)-1] = strings.TrimRight(newLines[len(newLines)-1], "\n")
	}
	return os.WriteFile(c.filePath, []byte(strings.Join(newLines, "")), 0o644)
}

// reorderLines 将源行 moveSrcLine 移动插入到 targetSrcLine 之前（1-based 行号）。
// 删除源行后目标行号会前移一格，此处统一换算。
func reorderLines(allLines []string, moveSrcLine, targetSrcLine int) []string {
	moveContent := allLines[moveSrcLine-1]
	// 从 allLines 删除源行
	allLines = append(allLines[:moveSrcLine-1], allLines[moveSrcLine:]...)
	// 重新计算目标位置（因为删除了源行，目标行号可能变化）
	newTargetIdx := targetSrcLine
	if moveSrcLine < targetSrcLine {
		newTargetIdx = targetSrcLine - 1
	}
	if newTargetIdx < 1 {
		newTargetIdx = 1
	}
	if newTargetIdx > len(allLines)+1 {
		newTargetIdx = len(allLines) + 1
	}
	// 在 newTargetIdx 位置插入
	var newLines []string
	newLines = append(newLines, allLines[:newTargetIdx-1]...)
	newLines = append(newLines, moveContent)
	newLines = append(newLines, allLines[newTargetIdx-1:]...)
	return newLines
}

// 节点行解析正则。
var (
	reWithYx = regexp.MustCompile(`^(?P<proxyip>[^@]*)@(?P<yx_ip>[^#]+)#(?P<name>.+)$`)
	// 仅 proxyip#name
	rePlain = regexp.MustCompile(`^(?P<proxyip>[^#]*)#(?P<name>.+)$`)
)

// Parse 解析文件返回配置列表（保留行号便于后续原位修改）。
// 公开入口自带并发保护；已持有 c.mu 的内部方法请改用 parseUnlocked。
func (c *ConfigStore) Parse() ([]ConfigEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parseUnlocked()
}

// parseUnlocked 为 Parse 的无锁内部实现（调用方必须已持有 c.mu）。
func (c *ConfigStore) parseUnlocked() ([]ConfigEntry, error) {
	lines, err := c.readLines()
	if err != nil {
		return nil, err
	}
	result := make([]ConfigEntry, 0, len(lines))
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, ok := c.parseLine(line)
		if !ok {
			continue
		}
		entry.lineNumber = lineNo
		result = append(result, entry)
	}
	return result, nil
}

// Add 在文件末尾追加一条配置。
func (c *ConfigStore) Add(ip, name string, yxIP string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	content := c.formatLine(ip, name, yxIP)
	lines, err := c.readLines()
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		lines = []string{content}
	} else {
		if !strings.HasSuffix(lines[len(lines)-1], "\n") {
			lines[len(lines)-1] = lines[len(lines)-1] + "\n"
		}
		lines = append(lines, content)
	}
	return os.WriteFile(c.filePath, []byte(strings.Join(lines, "")), 0o644)
}

// UpdateVisible 按可见行号（1-based）更新配置；未提交字段回填原值。
func (c *ConfigStore) UpdateVisible(visibleLine int, ip, name, yxIP *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.parseUnlocked()
	if err != nil {
		return err
	}
	if visibleLine < 1 || visibleLine > len(entries) {
		return ErrOutOfRange
	}
	src := entries[visibleLine-1]
	content := c.formatLine(
		pickOrDefault(ip, src.rawIP),
		pickOrDefault(name, src.Name),
		pickOrDefault(yxIP, src.rawYxIP),
	)

	lines, err := c.readLines()
	if err != nil {
		return err
	}
	lineNo := src.lineNumber
	if lineNo < 1 || lineNo > len(lines) {
		return ErrOutOfRange
	}
	lines[lineNo-1] = content + "\n"
	if lineNo == len(lines) {
		lines[len(lines)-1] = strings.TrimRight(lines[len(lines)-1], "\n")
	}
	return os.WriteFile(c.filePath, []byte(strings.Join(lines, "")), 0o644)
}

// pickOrDefault 指针非 nil 取指针值，否则回退原值。
func pickOrDefault(update *string, fallback string) string {
	if update != nil {
		return *update
	}
	return fallback
}

// DeleteVisible 按可见行号删除配置。
func (c *ConfigStore) DeleteVisible(visibleLine int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.parseUnlocked()
	if err != nil {
		return err
	}
	if visibleLine < 1 || visibleLine > len(entries) {
		return ErrOutOfRange
	}
	lineNo := entries[visibleLine-1].lineNumber
	lines, err := c.readLines()
	if err != nil {
		return err
	}
	if lineNo < 1 || lineNo > len(lines) {
		return ErrOutOfRange
	}
	lines = append(lines[:lineNo-1], lines[lineNo:]...)
	// 去掉末尾空行
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines[len(lines)-1] = strings.TrimRight(lines[len(lines)-1], "\n")
	}
	return os.WriteFile(c.filePath, []byte(strings.Join(lines, "")), 0o644)
}

// SwapVisible 交换两个可见行号对应的配置。
func (c *ConfigStore) SwapVisible(firstLine, secondLine int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.parseUnlocked()
	if err != nil {
		return err
	}
	if firstLine < 1 || firstLine > len(entries) || secondLine < 1 || secondLine > len(entries) {
		return ErrOutOfRange
	}
	lineA := entries[firstLine-1].lineNumber
	lineB := entries[secondLine-1].lineNumber
	lines, err := c.readLines()
	if err != nil {
		return err
	}
	lines[lineA-1], lines[lineB-1] = lines[lineB-1], lines[lineA-1]
	return os.WriteFile(c.filePath, []byte(strings.Join(lines, "")), 0o644)
}

// readLines 读取文件并按行切分（保留行尾换行符，与 Python splitlines(keepends=True) 一致）。
func (c *ConfigStore) readLines() ([]string, error) {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// splitlines(keepends=True)
	s := string(data)
	if s == "" {
		return nil, nil
	}
	return splitLinesKeepEnds(s), nil
}

// splitLinesKeepEnds 按行切分并保留行尾分隔符。
// Python splitlines(keepends=True) 行为：按 \n \r\n \r 切分且保留分隔符。
func splitLinesKeepEnds(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
			i++
			continue
		}
		if c == '\r' {
			if i+1 < len(s) && s[i+1] == '\n' {
				lines = append(lines, s[start:i+2])
				start = i + 2
				i += 2
				continue
			}
			lines = append(lines, s[start:i+1])
			start = i + 1
			i++
			continue
		}
		i++
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// parseLine 解析单行节点文本（两种形态：proxyip@yx_ip#name / proxyip#name）。
func (c *ConfigStore) parseLine(line string) (ConfigEntry, bool) {
	if m := reWithYx.FindStringSubmatch(line); m != nil {
		rawProxyIP := strings.TrimSpace(m[1])
		rawYxIP := strings.TrimSpace(m[2])
		yxHost, yxPort := SplitHostPort(rawYxIP)
		entry := ConfigEntry{
			Name:    strings.TrimSpace(m[3]),
			YxIP:    c.resolveVariable(rawYxIP),
			YxHost:  c.resolveVariable(yxHost),
			YxPort:  yxPort,
			rawIP:   rawProxyIP,
			rawYxIP: rawYxIP,
		}
		if rawProxyIP == "" || rawProxyIP == "DIRECT" {
			entry.IP = ""
		} else {
			entry.IP = c.resolveVariable(rawProxyIP)
		}
		return entry, true
	}
	if m := rePlain.FindStringSubmatch(line); m != nil {
		rawProxyIP := strings.TrimSpace(m[1])
		entry := ConfigEntry{
			Name:  strings.TrimSpace(m[2]),
			rawIP: rawProxyIP,
		}
		if rawProxyIP == "" || rawProxyIP == "DIRECT" {
			entry.IP = ""
		} else {
			entry.IP = c.resolveVariable(rawProxyIP)
		}
		return entry, true
	}
	return ConfigEntry{}, false
}

// resolveVariable 解析变量引用（原样/不带花括号/双花括号形式均支持）。
func (c *ConfigStore) resolveVariable(value string) string {
	token := strings.TrimSpace(value)
	key := c.extractVariableKey(token)
	if key == "" {
		return token
	}
	return c.loadVariableValue(key)
}

// extractVariableKey 若 token 是已注册变量则返回变量名，否则空串。
// 内置变量 NRT 无需在 variables 中配置即可使用（裸值 / {{NRT}} / ${NRT} 均识别）。
func (c *ConfigStore) extractVariableKey(token string) string {
	key := token
	if strings.HasPrefix(token, "{{") && strings.HasSuffix(token, "}}") {
		key = strings.TrimSpace(token[2 : len(token)-2])
	} else if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		key = strings.TrimSpace(token[2 : len(token)-1])
	}
	if key == "NRT" {
		return key
	}
	if _, ok := c.variables[key]; ok {
		return key
	}
	return ""
}

// loadVariableValue 读取变量的实际值（file 读文件 / inline 取内容 / NRT 内置兜底）。
func (c *ConfigStore) loadVariableValue(key string) string {
	src, ok := c.variables[key]
	if !ok {
		// 内置 NRT 变量兜底
		if key == "NRT" {
			data, err := os.ReadFile(c.nrtFile)
			if err != nil {
				return key
			}
			return strings.TrimSpace(string(data))
		}
		return key
	}
	switch src.SourceType {
	case "file":
		data, err := os.ReadFile(src.Path)
		if err != nil {
			return key
		}
		return strings.TrimSpace(string(data))
	case "inline":
		return strings.TrimSpace(src.Value)
	}
	return key
}

// sanitizeField 去除字段内的换行/回车（防止破坏 vless.txt 的行结构）。
func sanitizeField(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

// formatLine 按字段拼接节点行（有优选 IP 用 @ 语法，DIRECT/空 IP 规范化）。
func (c *ConfigStore) formatLine(ip, name, yxIP string) string {
	normalizedIP := ""
	if ip != "" && ip != "DIRECT" {
		normalizedIP = sanitizeField(ip)
	}
	normalizedYx := ""
	if yxIP != "" {
		normalizedYx = sanitizeField(yxIP)
	}
	name = sanitizeField(name)
	if normalizedYx != "" {
		return normalizedIP + "@" + normalizedYx + "#" + name
	}
	return normalizedIP + "#" + name
}
