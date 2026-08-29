// acl4ssr.go - ACL4SSR ini 解析器与组展开。
// 解析 ACL4SSR ini 格式的 ruleset= 和 custom_proxy_group= 行，
// 按 subconverter groupGenerate 语义展开 proxy-groups：
//   - []组名  → 直接引用其他组
//   - .*      → 匹配全部节点
//   - 正则    → 大小写不敏感匹配节点名（如 港|HK）
//   - !!TYPE=SS|VMESS|TROJAN!!regex → 按节点类型过滤后再匹配
//   - 空组    → 补 DIRECT
package module

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ACL4SSRConfig 解析后的 ACL4SSR 配置。
type ACL4SSRConfig struct {
	RuleSets      []RuleSetEntry
	ProxyGroups   []ProxyGroupEntry
	EnableRuleGen bool
}

// RuleSetEntry ruleset=行。
type RuleSetEntry struct {
	Group string // 分组名（如 🎯 全球直连）
	URL   string // list 文件 URL；[]开头（IsGeo）时为内置规则内容（如 GEOIP,CN）
	IsGeo bool   // 是否是 [] 前缀的内置规则（GEOIP/FINAL/DOMAIN-SUFFIX 等）
}

// ProxyGroupEntry custom_proxy_group=行。
type ProxyGroupEntry struct {
	Name      string
	Type      string   // select / url-test / fallback / load-balance
	Rules     []string // 组引用（[]前缀）或节点名匹配规则（正则/.*）
	URL       string   // url-test/fallback/load-balance 的测试 URL
	Interval  int      // 测速间隔（秒）
	Tolerance int      // url-test 容差
}

// ParseACL4SSRIni 从 ini 内容解析 ACL4SSR 配置。
func ParseACL4SSRIni(content string) *ACL4SSRConfig {
	cfg := &ACL4SSRConfig{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "ruleset=") {
			val := strings.TrimPrefix(line, "ruleset=")
			parts := strings.SplitN(val, ",", 2)
			if len(parts) == 2 {
				group := strings.TrimSpace(parts[0])
				url := strings.TrimSpace(parts[1])
				entry := RuleSetEntry{Group: group, URL: url}
				// []GEOIP,CN / []FINAL / []DOMAIN-SUFFIX,xx 等内置规则
				if strings.HasPrefix(url, "[]") {
					entry.IsGeo = true
					entry.URL = strings.TrimPrefix(url, "[]")
				}
				cfg.RuleSets = append(cfg.RuleSets, entry)
			}
		} else if strings.HasPrefix(line, "custom_proxy_group=") {
			val := strings.TrimPrefix(line, "custom_proxy_group=")
			if pg := parseProxyGroup(val); pg != nil {
				cfg.ProxyGroups = append(cfg.ProxyGroups, *pg)
			}
		} else if strings.HasPrefix(line, "enable_rule_generator=") {
			val := strings.TrimPrefix(line, "enable_rule_generator=")
			cfg.EnableRuleGen = strings.TrimSpace(val) == "true"
		}
	}
	return cfg
}

// parseProxyGroup 解析 custom_proxy_group= 值。
// 格式：名称`type`规则1`规则2`...（url-test 类：名称`type`规则`url`interval,tolerance）
func parseProxyGroup(val string) *ProxyGroupEntry {
	parts := strings.Split(val, "`")
	if len(parts) < 2 {
		return nil
	}
	pg := &ProxyGroupEntry{
		Name:  strings.TrimSpace(parts[0]),
		Type:  strings.TrimSpace(parts[1]),
		Rules: []string{},
	}
	for i := 2; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			pg.URL = p
			continue
		}
		// interval,tolerance（如 300,50 / 300）
		if n, err := parseIntervalTolerance(p, pg); err == nil {
			if n {
				continue
			}
		}
		// 其余段均为组引用或节点匹配规则
		pg.Rules = append(pg.Rules, p)
	}
	return pg
}

// parseIntervalTolerance 尝试把段解析为 "interval[,tolerance]"。
// 命中时写入 pg 并返回 true。
func parseIntervalTolerance(p string, pg *ProxyGroupEntry) (bool, error) {
	segs := strings.Split(p, ",")
	if len(segs) > 2 {
		return false, fmt.Errorf("not interval")
	}
	nums := make([]int, 0, len(segs))
	for _, s := range segs {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return false, err
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 || nums[0] <= 0 {
		return false, fmt.Errorf("not interval")
	}
	pg.Interval = nums[0]
	if len(nums) == 2 {
		pg.Tolerance = nums[1]
	}
	return true, nil
}

// FetchACL4SSRIni 从 URL 获取 ini 内容（超时 10 秒），带本地缓存。
// 缓存路径：data/acl4ssr-cache/<md5(url)>.ini，有效期 24 小时。
func FetchACL4SSRIni(url string) (string, error) {
	cacheDir := filepath.Join("data", "acl4ssr-cache")
	cacheFile := filepath.Join(cacheDir, fmt.Sprintf("%x.ini", md5.Sum([]byte(url))))

	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < 24*time.Hour {
			if data, err := os.ReadFile(cacheFile); err == nil {
				return string(data), nil
			}
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		// fetch 失败但有过期缓存，仍用过期缓存
		if data, e := os.ReadFile(cacheFile); e == nil {
			return string(data), nil
		}
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	content := string(body)
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(cacheFile, body, 0o644)
	return content, nil
}

// BuildFromACL4SSRConfig 根据 ACL4SSR 配置生成 mihomo proxy-groups / rule-providers / rules。
// 组展开按 subconverter groupGenerate 语义，entries 提供节点名与类型。
func BuildFromACL4SSRConfig(cfg *ACL4SSRConfig, entries []SubEntry) (proxyGroups string, ruleProviders string, rules string) {
	if cfg == nil {
		return
	}

	// ---- proxy-groups ----
	var pgBuf strings.Builder
	pgBuf.WriteString("proxy-groups:\n")
	for _, pg := range cfg.ProxyGroups {
		pgBuf.WriteString("  - name: " + yamlStr(pg.Name) + "\n")
		pgBuf.WriteString("    type: " + pg.Type + "\n")
		if pg.URL != "" {
			pgBuf.WriteString("    url: " + pg.URL + "\n")
		}
		if pg.Interval > 0 {
			pgBuf.WriteString("    interval: " + strconv.Itoa(pg.Interval) + "\n")
		}
		if pg.Tolerance > 0 {
			pgBuf.WriteString("    tolerance: " + strconv.Itoa(pg.Tolerance) + "\n")
		}

		refs := expandGroupRules(pg.Rules, entries)
		if len(refs) == 0 {
			refs = []string{"DIRECT"} // subconverter：空组补 DIRECT
		}
		pgBuf.WriteString("    proxies:\n")
		for _, ref := range refs {
			pgBuf.WriteString("      - " + yamlStr(ref) + "\n")
		}
	}
	proxyGroups = pgBuf.String()

	// ---- rule-providers + rules ----
	var rpBuf strings.Builder
	rpBuf.WriteString("rule-providers:\n")
	rpHasProvider := false
	var rulesBuf strings.Builder
	rulesBuf.WriteString("rules:\n")
	for i, rs := range cfg.RuleSets {
		if rs.IsGeo {
			// 内置规则：GEOIP,CN 原样；FINAL 转 MATCH（mihomo 用 MATCH 收尾）
			rule := rs.URL
			if rule == "FINAL" || strings.HasPrefix(rule, "FINAL,") {
				rule = strings.Replace(rule, "FINAL", "MATCH", 1)
			}
			rulesBuf.WriteString(fmt.Sprintf("- %s,%s\n", rule, rs.Group))
			continue
		}
		name := extractRuleName(rs.URL, i)
		rpBuf.WriteString(fmt.Sprintf("  %s:\n", name))
		rpBuf.WriteString("    type: http\n")
		appendBehaviorFormat(&rpBuf, rs.URL)
		rpBuf.WriteString(fmt.Sprintf("    url: %s\n", rs.URL))
		rpBuf.WriteString(fmt.Sprintf("    path: ./ruleset/%s.list\n", name))
		rpBuf.WriteString("    interval: 86400\n")
		rpHasProvider = true
		rulesBuf.WriteString(fmt.Sprintf("- RULE-SET,%s,%s\n", name, rs.Group))
	}
	if !rpHasProvider {
		// 无外部规则集时给出空段，避免 mihomo 解析空 map
		ruleProviders = ""
	} else {
		ruleProviders = rpBuf.String()
	}
	rules = rulesBuf.String()
	return
}

// appendBehaviorFormat 按规则集 URL 后缀写 behavior（与 format）。
// .list → classical（mihomo 默认 text）；.yaml/.yml → domain + yaml；.mrs → domain + mrs。
func appendBehaviorFormat(b *strings.Builder, url string) {
	switch {
	case strings.HasSuffix(url, ".mrs"):
		b.WriteString("    behavior: domain\n")
		b.WriteString("    format: mrs\n")
	case strings.HasSuffix(url, ".yaml"), strings.HasSuffix(url, ".yml"):
		b.WriteString("    behavior: domain\n")
		b.WriteString("    format: yaml\n")
	default:
		b.WriteString("    behavior: classical\n")
	}
}

// expandGroupRules 展开组规则（subconverter groupGenerate 移植）。
// 规则类型：
//   - []组名  → 引用其他组
//   - .*      → 全部节点
//   - !!TYPE=SS|VMESS|TROJAN!!regex → 类型过滤 + 正则
//   - 正则    → 大小写不敏感匹配节点名
//
// 结果去重保序。
func expandGroupRules(rules []string, entries []SubEntry) []string {
	refs := make([]string, 0, len(entries))
	seen := make(map[string]bool)
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if strings.HasPrefix(rule, "[]") {
			ref := strings.TrimSpace(rule[2:])
			if ref != "" && !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
			continue
		}
		for _, name := range matchNodes(rule, entries, seen) {
			refs = append(refs, name)
		}
	}
	return refs
}

// matchNodes 对单条规则匹配节点，返回去重后的节点名。
func matchNodes(rule string, entries []SubEntry, seen map[string]bool) []string {
	typeFilter := ""
	if strings.HasPrefix(rule, "!!TYPE=") {
		rest := strings.TrimPrefix(rule, "!!TYPE=")
		if idx := strings.Index(rest, "!!"); idx != -1 {
			typeFilter = strings.ToUpper(rest[:idx])
			rule = rest[idx+2:]
		} else {
			typeFilter = strings.ToUpper(rest)
			rule = ""
		}
	}
	if rule == ".*" || rule == "*" {
		rule = "" // 匹配全部
	}

	var re *regexp.Regexp
	if rule != "" {
		compiled, err := regexp.Compile("(?i)" + rule)
		if err != nil {
			// 非法正则退化为大小写不敏感包含匹配
			compiled = regexp.MustCompile("(?i)" + regexp.QuoteMeta(rule))
		}
		re = compiled
	}

	var out []string
	for _, e := range entries {
		if typeFilter != "" && nodeTypeName(e) != typeFilter {
			continue
		}
		if re != nil && !re.MatchString(e.Name) {
			continue
		}
		if !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e.Name)
		}
	}
	return out
}

// nodeTypeName 节点类型名（!!TYPE= 过滤用，对齐 subconverter 类型枚举）。
func nodeTypeName(e SubEntry) string {
	if e.Gen == nil {
		return "VLESS" // 历史缺省行为：vless
	}
	switch e.Gen.Normalized().Protocol {
	case "ss":
		return "SS"
	case "trojan":
		return "TROJAN"
	default:
		return "VLESS"
	}
}

// extractRuleName 从 URL 提取规则集名称。
func extractRuleName(url string, idx int) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		name = strings.TrimSuffix(name, ".list")
		name = strings.TrimSuffix(name, ".yaml")
		name = strings.TrimSuffix(name, ".yml")
		name = strings.TrimSuffix(name, ".mrs")
		if name != "" {
			return name
		}
	}
	return fmt.Sprintf("rule%d", idx)
}
