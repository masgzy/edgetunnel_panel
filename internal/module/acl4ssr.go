// acl4ssr.go - ACL4SSR ini 解析器。
// 解析 ACL4SSR ini 格式的 ruleset= 和 custom_proxy_group= 行，
// 生成 mihomo 的 proxy-groups 和 rule-providers 配置。
package module

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	URL   string // list 文件 URL
	IsGeo bool   // 是否是 GEOIP/IP 等内置规则（[]GEOIP,CN）
}

// ProxyGroupEntry custom_proxy_group=行。
type ProxyGroupEntry struct {
	Name    string
	Type    string // select / url-test / fallback / load-balance
	Proxies []string
	URL     string // url-test 的测试 URL
	Options string // 其他选项
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
				// []GEOIP,CN 或 []FINAL 等内置规则
				if strings.HasPrefix(url, "[]") {
					entry.IsGeo = true
					entry.URL = strings.TrimPrefix(url, "[]")
				}
				cfg.RuleSets = append(cfg.RuleSets, entry)
			}
		} else if strings.HasPrefix(line, "custom_proxy_group=") {
			val := strings.TrimPrefix(line, "custom_proxy_group=")
			pg := parseProxyGroup(val)
			if pg != nil {
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
// 格式：名称`type`proxy1`proxy2`...
func parseProxyGroup(val string) *ProxyGroupEntry {
	// 用 ` 分隔
	parts := strings.Split(val, "`")
	if len(parts) < 2 {
		return nil
	}
	pg := &ProxyGroupEntry{
		Name:    strings.TrimSpace(parts[0]),
		Type:    strings.TrimSpace(parts[1]),
		Proxies: []string{},
	}
	for i := 2; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		// url-test 格式：.*`http://...`300,,50
		if pg.Type == "url-test" || pg.Type == "fallback" || pg.Type == "load-balance" {
			if i == 2 && (strings.HasPrefix(p, ".*") || p == ".*") {
				// 匹配所有节点的通配符
				pg.Proxies = append(pg.Proxies, ".*")
				continue
			}
			if strings.HasPrefix(p, "http") {
				pg.URL = p
				continue
			}
			// 剩余是选项（interval/tolerance）
			pg.Options = p
			continue
		}
		pg.Proxies = append(pg.Proxies, p)
	}
	return pg
}

// FetchACL4SSRIni 从 URL 获取 ini 内容（超时 10 秒），带本地缓存。
// 缓存路径：data/acl4ssr-cache/<md5(url)>.ini，有效期 24 小时。
func FetchACL4SSRIni(url string) (string, error) {
	// 计算缓存路径
	cacheDir := filepath.Join("data", "acl4ssr-cache")
	cacheFile := filepath.Join(cacheDir, fmt.Sprintf("%x.ini", md5.Sum([]byte(url))))

	// 检查缓存是否存在且未过期（24 小时）
	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < 24*time.Hour {
			if data, err := os.ReadFile(cacheFile); err == nil {
				return string(data), nil
			}
		}
	}

	// fetch 远程
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
	// 写入缓存
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(cacheFile, body, 0o644)
	return content, nil
}

// BuildFromACL4SSRConfig 根据 ACL4SSR 配置生成 mihomo proxy-groups 和 rule-providers。
func BuildFromACL4SSRConfig(cfg *ACL4SSRConfig) (proxyGroups string, ruleProviders string, rules string) {
	if cfg == nil {
		return
	}
	// proxy-groups
	var pgBuf strings.Builder
	pgBuf.WriteString("proxy-groups:\n")
	for _, pg := range cfg.ProxyGroups {
		pgBuf.WriteString(fmt.Sprintf("  - name: %s\n", pg.Name))
		pgBuf.WriteString(fmt.Sprintf("    type: %s\n", pg.Type))
		if pg.Type == "url-test" || pg.Type == "fallback" || pg.Type == "load-balance" {
			if pg.URL != "" {
				pgBuf.WriteString(fmt.Sprintf("    url: %s\n", pg.URL))
				pgBuf.WriteString("    interval: 300\n")
			}
		}
		// proxies 列表
		var proxies []string
		for _, p := range pg.Proxies {
			if p == ".*" {
				continue // 通配符，用 proxy-providers 代替
			}
			proxies = append(proxies, p)
		}
		if len(proxies) > 0 {
			pgBuf.WriteString("    proxies:\n")
			for _, p := range proxies {
				pgBuf.WriteString(fmt.Sprintf("      - %s\n", p))
			}
		}
	}
	proxyGroups = pgBuf.String()

	// rule-providers
	var rpBuf strings.Builder
	rpBuf.WriteString("rule-providers:\n")
	var rulesBuf strings.Builder
	rulesBuf.WriteString("rules:\n")
	for i, rs := range cfg.RuleSets {
		if rs.IsGeo {
			// GEOIP 或 FINAL 等内置规则
			rulesBuf.WriteString(fmt.Sprintf("  - %s,%s\n", rs.URL, rs.Group))
			continue
		}
		// 从 URL 提取名称
		name := extractRuleName(rs.URL, i)
		rpBuf.WriteString(fmt.Sprintf("  %s:\n", name))
		rpBuf.WriteString("    type: http\n")
		rpBuf.WriteString("    behavior: classical\n")
		rpBuf.WriteString(fmt.Sprintf("    url: %s\n", rs.URL))
		rpBuf.WriteString(fmt.Sprintf("    path: ./ruleset/%s.list\n", name))
		rpBuf.WriteString("    interval: 86400\n")
		rulesBuf.WriteString(fmt.Sprintf("  - RULE-SET,%s,%s\n", name, rs.Group))
	}
	ruleProviders = rpBuf.String()
	rules = rulesBuf.String()
	return
}

// extractRuleName 从 URL 提取规则集名称。
func extractRuleName(url string, idx int) string {
	// 取 URL 最后一段文件名（去 .list 后缀）
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		name = strings.TrimSuffix(name, ".list")
		name = strings.TrimSuffix(name, ".yaml")
		if name != "" {
			return name
		}
	}
	return fmt.Sprintf("rule%d", idx)
}
