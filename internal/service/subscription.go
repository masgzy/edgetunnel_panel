// subscription.go - 订阅生成服务。
// 对应原 Python edt_app/services/subscription.py，并集成 subconverter 桥接。
package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"edt/internal/config"
	"edt/internal/module"
)

// SubItem 订阅构建用的单个节点。
type SubItem struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	YxIP   string `json:"yx_ip,omitempty"`
	YxHost string `json:"yx_host,omitempty"`
	YxPort int    `json:"yx_port,omitempty"`
}

// SubConverterConfig subconverter 桥接配置。
type SubConverterConfig struct {
	// Mode: "local" | "remote" | "off"
	// local = 调用本地 bin/subconverter/subconverter 二进制（由 edt_panel 启动）
	// remote = 调用配置的远程 URL
	// off = 不启用（仅 edt_panel 原生 vless/clash/mihomo）
	Mode      string
	Remote    string // 远程 subconverter base url（如 https://api.v1.mk）
	LocalBin  string // 本地 subconverter 二进制路径
	LocalPort int    // 本地 subconverter 监听端口
}

// SubscriptionService 订阅服务。
type SubscriptionService struct {
	configStore    *ConfigStore
	resultStore    *ResultStore
	preIPStore     *PreIPStore
	uuidService    *UUIDService
	nrtFile        string
	mihomoTemplate string
	mihomoFile     string
	port           int
	profiles       map[string]config.SubscriptionProfile
	defaultProfile string
	defaultByID    map[string]string
	encodeB64      bool
	dataSources    map[string]config.DataSourceConfig

	// subconverter 桥接（可选）
	subConverter SubConverterConfig

	// 生成配置：自动面板 / 手动 / 缺省三级来源
	genAutoFromPanel bool
	genAggregate     bool // 聚合面板(Worker)原生订阅（gen.aggregate_worker_sub）
	genManual        config.GenSettings
	genSet           bool // SetGenConfig 是否已调用（未调用时用缺省值）

	// userinfo 过期时间字符串（由 server 注入）
	userinfoExpireStr string
}

// NewSubscriptionService 构造。
func NewSubscriptionService(
	cs *ConfigStore,
	rs *ResultStore,
	ps *PreIPStore,
	us *UUIDService,
	nrtFile, mihomoTemplate, mihomoFile string,
	port int,
	profiles map[string]config.SubscriptionProfile,
	defaultProfile string,
	defaultByID map[string]string,
	encodeB64 bool,
	dataSources map[string]config.DataSourceConfig,
) *SubscriptionService {
	return &SubscriptionService{
		configStore:    cs,
		resultStore:    rs,
		preIPStore:     ps,
		uuidService:    us,
		nrtFile:        nrtFile,
		mihomoTemplate: mihomoTemplate,
		mihomoFile:     mihomoFile,
		port:           port,
		profiles:       profiles,
		defaultProfile: defaultProfile,
		defaultByID:    defaultByID,
		encodeB64:      encodeB64,
		dataSources:    dataSources,
	}
}

// SetSubConverter 注入 subconverter 桥接配置。
func (s *SubscriptionService) SetSubConverter(cfg SubConverterConfig) {
	s.subConverter = cfg
}

// SetGenConfig 注入生成配置（gen 节解析结果）：auto_from_panel / aggregate_worker_sub 开关 + 手动默认值。
func (s *SubscriptionService) SetGenConfig(autoFromPanel, aggregateWorkerSub bool, manual config.GenSettings) {
	s.genAutoFromPanel = autoFromPanel
	s.genAggregate = aggregateWorkerSub
	s.genManual = manual.Normalized()
	s.genSet = true
}

// Reload 热更新订阅服务配置（配置重载时调用）。
// 与 Set* 注入方法同一约定：低频调用；map 字段为整表替换，
// 读端持有的旧引用继续可读（整表替换不并发写同一 map）。
func (s *SubscriptionService) Reload(
	nrtFile, mihomoTemplate, mihomoFile string,
	port int,
	profiles map[string]config.SubscriptionProfile,
	defaultProfile string,
	defaultByID map[string]string,
	encodeB64 bool,
	dataSources map[string]config.DataSourceConfig,
) {
	s.nrtFile = nrtFile
	s.mihomoTemplate = mihomoTemplate
	s.mihomoFile = mihomoFile
	s.port = port
	s.profiles = profiles
	s.defaultProfile = defaultProfile
	s.defaultByID = defaultByID
	s.encodeB64 = encodeB64
	s.dataSources = dataSources
}

// resolveGenSettings 计算当前生效的生成配置：
// 自动模式且面板快照可用 → 面板值；否则手动默认值；未注入时退回缺省。
func (s *SubscriptionService) resolveGenSettings() config.GenSettings {
	if s.genAutoFromPanel && s.uuidService != nil {
		if gs, _, ok := s.uuidService.GenPanelSnapshot(); ok {
			return gs
		}
	}
	if s.genSet {
		return s.genManual
	}
	return config.DefaultGenSettings()
}

// EffectiveGenSettings 返回当前实际生效的生成配置（面板自动 / 手动 / 缺省）。
func (s *SubscriptionService) EffectiveGenSettings() config.GenSettings {
	return s.resolveGenSettings()
}

// PanelGenAvailable 报告面板生成配置快照是否可用（已拉取且含协议/传输字段）。
func (s *SubscriptionService) PanelGenAvailable() bool {
	if s.uuidService == nil {
		return false
	}
	_, _, ok := s.uuidService.GenPanelSnapshot()
	return ok
}

// GetDomain 返回订阅域名。
func (s *SubscriptionService) GetDomain(subID, subType string) (string, error) {
	p, err := s.resolveProfile(subID, subType)
	if err != nil {
		return "", err
	}
	return p.Domain, nil
}

// GetUserinfo 返回 Subscription-Userinfo 头。
// 优先使用远程面板（EDT-Pages 型）上报的 CF 真实用量；不可用时退化为
// 原有时间伪造算法（无面板的纯本地部署保持旧行为）。
func (s *SubscriptionService) GetUserinfo() string {
	if s.uuidService != nil {
		if cf, ok := s.uuidService.UsageSnapshot(); ok {
			return module.GetUserinfoWithUsage(s.userinfoExpire(), module.Usage{
				Pages:   cf.Pages,
				Workers: cf.Workers,
				Max:     cf.Max,
				Valid:   true,
			})
		}
	}
	return module.GetUserinfo(s.userinfoExpire())
}

// LoadClashFile 读取已生成的 mihomo 订阅（兼容原 /sub?clash）。
// 候选顺序：files.mihomo_file 显式配置 → 模板同目录的 mihomo.yaml →
// 模板文件本身（与旧版行为一致；旧版用字符串切片拼接路径，
// 在模板名不同或过短时会算出错误路径甚至越界 panic）。
func (s *SubscriptionService) LoadClashFile() (string, error) {
	candidates := make([]string, 0, 3)
	if s.mihomoFile != "" {
		candidates = append(candidates, s.mihomoFile)
	}
	if s.mihomoTemplate != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(s.mihomoTemplate), "mihomo.yaml"))
	}
	candidates = append(candidates, s.mihomoTemplate)

	var firstErr error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", fmt.Errorf("无法读取 mihomo 配置（候选: %s）: %w",
		strings.Join(candidates, ", "), firstErr)
}

// BuildSubscription 构建 vless:// 订阅。
func (s *SubscriptionService) BuildSubscription(subID, subType string) (string, error) {
	profile, err := s.resolveProfile(subID, subType)
	if err != nil {
		return "", err
	}
	uuid, err := s.resolveUUID(profile)
	if err != nil {
		return "", err
	}
	yxIP, data, err := s.loadSubscriptionData(subID)
	if err != nil {
		return "", err
	}
	data = s.applyUniqueNames(data)

	var lines []string
	for _, item := range data {
		name := item.Name
		currentName := name
		if subID != "3" {
			currentName = module.AddFlagEmoji(name)
		}
		currentYxIP := item.YxIP
		if currentYxIP == "" {
			currentYxIP = yxIP
		}
		lines = append(lines, s.buildNodeLine(profile, uuid, item.IP, currentYxIP, currentName))
	}
	if s.genAggregate && s.uuidService != nil {
		// 聚合面板(Worker)原生订阅：token 鉴权拉取 mixed 节点列表，追加在本地节点之后。
		if rows, err := s.uuidService.FetchWorkerSub(); err == nil {
			seen := make(map[string]bool, len(lines)+len(rows))
			for _, l := range lines {
				seen[l] = true
			}
			for _, r := range rows {
				if !seen[r] {
					seen[r] = true
					lines = append(lines, r)
				}
			}
		}
	}
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if s.encodeB64 {
		return base64.StdEncoding.EncodeToString([]byte(body)), nil
	}
	return body, nil
}

// BuildMihomoSubscription 构建 mihomo/clash 订阅（完整可用配置）。
// configURL 可选：指定 ACL4SSR 规则集 URL，生成器据此切换 proxy-groups/rules 模板；
// 为空时用内嵌精简规则。
func (s *SubscriptionService) BuildMihomoSubscription(subID, subType, configURL string) (string, error) {
	profile, err := s.resolveProfile(subID, subType)
	if err != nil {
		return "", err
	}
	uuid, err := s.resolveUUID(profile)
	if err != nil {
		return "", err
	}
	yxIP, data, err := s.loadSubscriptionData(subID)
	if err != nil {
		return "", err
	}
	data = s.applyUniqueNames(data)

	entries := make([]module.SubEntry, 0, len(data))
	for _, item := range data {
		name := item.Name
		currentName := name
		if subID != "3" {
			currentName = module.AddFlagEmoji(name)
		}
		currentYxIP := item.YxIP
		if currentYxIP == "" {
			currentYxIP = yxIP
		}
		entries = append(entries, module.SubEntry{
			Name:    currentName,
			UUID:    uuid,
			ProxyIP: item.IP,
			YxIP:    currentYxIP,
			Domain:  profile.Domain,
			Port:    s.port,
		})
		gs := s.resolveGenSettings()
		entries[len(entries)-1].Gen = &gs
	}
	return module.BuildSubWithConfig(entries, configURL), nil
}

// ConvertViaSubConverter 调用 subconverter 把 vless 订阅转成其他格式（singbox/surge/quanx 等）。
// sourceURL 是 edt_panel 自己生成的 vless 订阅地址（subconverter 作为远程抓取源）。
func (s *SubscriptionService) ConvertViaSubConverter(target, sourceURL, externalConfig string) (string, error) {
	baseURL, err := s.subconverterBaseURL()
	if err != nil {
		return "", err
	}
	params := url.Values{}
	params.Set("target", target)
	params.Set("url", sourceURL)
	if externalConfig != "" {
		params.Set("config", externalConfig)
	}
	fullURL := baseURL + "/sub?" + params.Encode()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(fullURL)
	if err != nil {
		return "", fmt.Errorf("subconverter 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("subconverter 返回 %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	result := string(body)
	// 把后端常见失败语义化，便于定位（如官方构建不支持 vless 分享链接）
	if strings.Contains(result, "No nodes were found") {
		return "", fmt.Errorf(
			"subconverter 未从订阅中解析出节点：请确认订阅内容有效，" +
				"且所配 subconverter 后端支持对应协议（官方构建不支持 vless 链接，建议使用支持 vless 的分支）")
	}
	return result, nil
}

// SubConverterStatus 返回 subconverter 桥接状态（供前端显示）。
func (s *SubscriptionService) SubConverterStatus() map[string]interface{} {
	return map[string]interface{}{
		"mode":       s.subConverter.Mode,
		"remote":     s.subConverter.Remote,
		"local_port": s.subConverter.LocalPort,
	}
}

// RuntimeStatus 返回运行时状态摘要（供仪表盘状态卡片显示）。
func (s *SubscriptionService) RuntimeStatus() map[string]interface{} {
	return map[string]interface{}{
		"subscription_port": s.port,
		"default_profile":   s.defaultProfile,
		"encode_base64":     s.encodeB64,
		"profile_count":     len(s.profiles),
		"data_source_count": len(s.dataSources),
		"profiles":          profileList(s.profiles),
		"data_sources":      dataSourceList(s.dataSources),
	}
}

// profileList 将模板映射转为按 key 排序的列表，保证 API 输出顺序稳定。
func profileList(profiles map[string]config.SubscriptionProfile) []map[string]interface{} {
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]map[string]interface{}, 0, len(profiles))
	for _, k := range keys {
		p := profiles[k]
		out = append(out, map[string]interface{}{
			"key":       p.Key,
			"name":      p.Name,
			"format":    p.Format,
			"domain":    p.Domain,
			"uuid_mode": p.UUIDMode,
			"aliases":   p.Aliases,
		})
	}
	return out
}

// dataSourceList 将数据源映射转为按 id 排序的列表（id 为纯数字时按数值序，否则按字典序）。
func dataSourceList(sources map[string]config.DataSourceConfig) []map[string]interface{} {
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, errA := strconv.Atoi(ids[i])
		b, errB := strconv.Atoi(ids[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return ids[i] < ids[j]
	})

	out := make([]map[string]interface{}, 0, len(sources))
	for _, id := range ids {
		ds := sources[id]
		out = append(out, map[string]interface{}{
			"id":   ds.ID,
			"name": ds.Name,
			"kind": ds.Kind,
		})
	}
	return out
}

// ----- 内部 -----

// resolveProfile 将订阅 ID + 类型解析为具体模板；类型为空时按 defaults_by_id 或全局默认。
func (s *SubscriptionService) resolveProfile(subID, subType string) (config.SubscriptionProfile, error) {
	target := subType
	if target == "" {
		if v, ok := s.defaultByID[subID]; ok {
			target = v
		} else {
			target = s.defaultProfile
		}
	}
	if target == "" {
		return config.SubscriptionProfile{}, fmt.Errorf("没有配置默认订阅模板，请在 config.yml 中设置 subscriptions.default_profile")
	}
	if p, ok := s.profiles[target]; ok {
		return p, nil
	}
	for _, p := range s.profiles {
		for _, a := range p.Aliases {
			if a == target {
				return p, nil
			}
		}
	}
	return config.SubscriptionProfile{}, fmt.Errorf("type=%s 未在 config.yml 中定义", target)
}

// resolveUUID 依模板的 uuid_mode 返回节点 UUID：dynamic 走 UUIDService，static 用固定值。
func (s *SubscriptionService) resolveUUID(profile config.SubscriptionProfile) (string, error) {
	if profile.UUIDMode == "dynamic" {
		return s.uuidService.Get()
	}
	if profile.UUID != "" {
		return profile.UUID, nil
	}
	return "", fmt.Errorf("profile=%s 配置为 static，但没有提供 uuid", profile.Key)
}

// loadSubscriptionData 加载某订阅 ID 对应的数据源（返回拼接后的源文本与解析节点）。
func (s *SubscriptionService) loadSubscriptionData(subID string) (string, []SubItem, error) {
	source, ok := s.dataSources[subID]
	if !ok {
		return "", nil, fmt.Errorf("id=%s 没有配置数据源，请在 config.yml 中设置 data_sources.by_id", subID)
	}
	switch source.Kind {
	case "vless_file":
		yxIP := s.resultStore.GetFirstIP()
		var store *ConfigStore
		if source.Path == s.configStore.FilePath() {
			store = s.configStore
		} else {
			store = NewConfigStore(source.Path, s.nrtFile, s.configStore.Variables())
		}
		entries, err := store.Parse()
		if err != nil {
			return "", nil, err
		}
		items := make([]SubItem, 0, len(entries))
		for _, e := range entries {
			items = append(items, SubItem{
				Name: e.Name, IP: e.IP, YxIP: e.YxIP, YxHost: e.YxHost, YxPort: e.YxPort,
			})
		}
		return yxIP, items, nil
	case "json_file":
		yxIP := s.resultStore.GetFirstIP()
		data, err := os.ReadFile(source.Path)
		if err != nil {
			return "", nil, fmt.Errorf("读取 json 数据源失败: %w", err)
		}
		var items []SubItem
		if err := json.Unmarshal(data, &items); err != nil {
			return "", nil, fmt.Errorf("解析 json 数据源失败: %w", err)
		}
		return yxIP, items, nil
	case "preferred_result":
		pre, ip := s.preIPStore.Get()
		entries, err := s.resultStore.GetAllAsProxyEntries(pre, ip)
		if err != nil {
			return "", nil, err
		}
		items := make([]SubItem, 0, len(entries))
		for _, e := range entries {
			items = append(items, SubItem{Name: e.Name, IP: e.IP, YxIP: e.YxIP})
		}
		return "", items, nil
	}
	return "", nil, fmt.Errorf("id=%s 的数据源类型不支持: %s", subID, source.Kind)
}

// applyUniqueNames 为重名节点追加序号后缀，保证订阅内名称唯一。
func (s *SubscriptionService) applyUniqueNames(data []SubItem) []SubItem {
	names := make([]string, 0, len(data))
	for _, item := range data {
		names = append(names, item.Name)
	}
	unique := MarkDuplicates(names)
	for i := range data {
		data[i].Name = unique[i]
	}
	return data
}

// buildNodeLine 拼装单条订阅行（vless/trojan/ss 由生成配置决定）。
func (s *SubscriptionService) buildNodeLine(profile config.SubscriptionProfile, uuid, proxyIP, yxIP, name string) string {
	g := s.resolveGenSettings()
	return BuildNodeLink(g, profile.Format, LinkParams{
		UUID:      uuid,
		Authority: FormatAuthority(yxIP, s.port),
		Domain:    profile.Domain,
		ProxyIP:   proxyIP,
		Name:      name,
	})
}

// userinfoExpire 计算流量信息的过期时间戳（秒）。
func (s *SubscriptionService) userinfoExpire() string {
	// 从 profiles 任意一个的 userinfo 配置中取不到，用全局
	// 原项目从 RuntimeConfig 取，这里没有直接引用 RuntimeConfig，需要外部注入。
	// 暂时返回空，由 server 层在构造时通过 SetUserinfoExpire 注入。
	return s.userinfoExpireStr
}

// SetUserinfoExpire 注入 userinfo 过期时间字符串。
func (s *SubscriptionService) SetUserinfoExpire(v string) {
	s.userinfoExpireStr = v
}

// subconverterBaseURL 返回 subconverter 桥接的基地址（remote 地址或本地端口）。
func (s *SubscriptionService) subconverterBaseURL() (string, error) {
	switch s.subConverter.Mode {
	case "remote":
		if s.subConverter.Remote == "" {
			return "", fmt.Errorf("subconverter 远程地址未配置")
		}
		return strings.TrimRight(s.subConverter.Remote, "/"), nil
	case "local":
		if s.subConverter.LocalPort == 0 {
			s.subConverter.LocalPort = 25500
		}
		return fmt.Sprintf("http://127.0.0.1:%d", s.subConverter.LocalPort), nil
	default:
		return "", fmt.Errorf("subconverter 未启用（mode=off 或未配置）")
	}
}
