// mihomo.go - Mihomo/Clash Meta 订阅生成。
//
// 改进原 Python module/mihomo.py：
// 原实现仅做模板字符串替换（[域名] -> domain 等），依赖外部 template.yaml。
// 本实现借鉴 subconverter subexport.cpp 思路，提供两种模式：
//  1. Template 模式：兼容原项目，读取 data/mihomo/template.yaml 做替换（保留 [域名][名称][UUID][代理IP][优选IP]）
//  2. Full 模式：直接生成完整可用的 mihomo 配置（proxies + proxy-groups + rules），无需外部模板
package module

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"edt/internal/config"
)

// MihomoConfig mihomo 模块依赖。
type MihomoConfig struct {
	TemplatePath string // data/mihomo/template.yaml（Template 模式用）
}

// SubEntry mihomo 生成所需的单个节点信息。
type SubEntry struct {
	Name    string // 节点名（已加 emoji）
	UUID    string
	ProxyIP string // 可为空（DIRECT）
	YxIP    string // 优选 IP/域名（authority）
	Domain  string // SNI / Host
	Port    int    // 默认端口

	// Gen 非空时按生成配置渲染（协议/传输/证书校验/指纹等）；
	// nil 时保持历史行为：vless+ws+tls，路径固定带 ed=2560，指纹 chrome。
	Gen *config.GenSettings
}

// SubWithTemplate 用模板替换生成单个节点 yaml 片段。
// 占位符：[域名] [名称] [UUID] [代理IP] [优选IP]
func SubWithTemplate(templatePath string, e SubEntry) (string, error) {
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("读取 mihomo 模板失败: %w", err)
	}
	content := string(data)
	content = strings.ReplaceAll(content, "[域名]", e.Domain)
	content = strings.ReplaceAll(content, "[名称]", e.Name)
	content = strings.ReplaceAll(content, "[UUID]", e.UUID)
	content = strings.ReplaceAll(content, "[代理IP]", e.ProxyIP)
	content = strings.ReplaceAll(content, "[优选IP]", e.YxIP)
	return content, nil
}

// SubFull 生成单个节点的 mihomo proxies 片段（yaml 缩进 0）。
// 借鉴 subconverter 的节点结构，确保 mihomo 可直接加载；
// e.Gen 非 nil 时协议/传输/证书/指纹跟随生成配置，否则维持历史 vless+ws 行为。
//
// 输出形如：
//   - name: "🇯🇵 日本 NRT"
//     type: vless
//     server: example.com
//     port: 8443
//     uuid: xxx
//     tls: true
//     servername: example.com
//     network: ws
//     ws-opts:
//     path: /proxyip=1.2.3.4?ed=2560
//     headers:
//     Host: example.com
//     client-fingerprint: chrome
func SubFull(e SubEntry) string {
	server := e.YxIP
	if server == "" {
		server = ""
	}
	// 若 yx_ip 含端口，拆分
	serverHost := server
	serverPort := e.Port
	if h, p, ok := splitHostPortStr(server); ok && p != 0 {
		serverHost = h
		serverPort = p
	}
	if e.Gen != nil {
		if gn := e.Gen.Normalized(); gn.Protocol == "ss" && !gn.SSTLS {
			// SS 非 TLS：TLS 端口组映射为对应 noTLS 端口（对齐生态订阅输出）。
			serverPort = mappedSSPort(serverPort)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  - name: %q\n", e.Name))
	b.WriteString(fmt.Sprintf("    server: %s\n", serverHost))
	b.WriteString(fmt.Sprintf("    port: %d\n", serverPort))
	b.WriteString("    udp: true\n")

	g := e.Gen
	if g == nil {
		// 历史行为：vless + ws + tls + ed=2560 + chrome
		path := fmt.Sprintf("/proxyip=%s?ed=2560", e.ProxyIP)
		if e.ProxyIP == "" || e.ProxyIP == "DIRECT" {
			path = "/?ed=2560"
		}
		b.WriteString("    type: vless\n")
		b.WriteString(fmt.Sprintf("    uuid: %s\n", e.UUID))
		b.WriteString("    tls: true\n")
		b.WriteString(fmt.Sprintf("    servername: %s\n", e.Domain))
		b.WriteString("    network: ws\n")
		b.WriteString("    ws-opts:\n")
		b.WriteString(fmt.Sprintf("      path: %s\n", path))
		b.WriteString("      headers:\n")
		b.WriteString(fmt.Sprintf("        Host: %s\n", e.Domain))
		b.WriteString("    client-fingerprint: chrome\n")
		return b.String()
	}

	gn := g.Normalized()
	path := gn.TransportPath("edt", e.ProxyIP) // 与订阅链接同款路径组装
	switch gn.Protocol {
	case "ss":
		// ss 以 v2ray-plugin 承载 ws(+tls)（与订阅链接同构；type:ss 不存在 network:ws 形式）。
		b.WriteString("    type: ss\n")
		b.WriteString(fmt.Sprintf("    cipher: %s\n", gn.SSCipher))
		b.WriteString(fmt.Sprintf("    password: %s\n", e.UUID))
		b.WriteString("    plugin: v2ray-plugin\n")
		b.WriteString("    plugin-opts:\n")
		b.WriteString("      mode: websocket\n")
		b.WriteString(fmt.Sprintf("      host: %s\n", e.Domain))
		b.WriteString(fmt.Sprintf("      path: %s\n", path))
		b.WriteString(fmt.Sprintf("      tls: %t\n", gn.SSTLS))
		if gn.SkipCertVerify {
			b.WriteString("      skip-cert-verify: true\n")
		}
		return b.String()
	case "trojan":
		b.WriteString("    type: trojan\n")
		b.WriteString(fmt.Sprintf("    password: %s\n", e.UUID))
		b.WriteString("    tls: true\n")
		b.WriteString(fmt.Sprintf("    servername: %s\n", e.Domain))
	default: // vless
		b.WriteString("    type: vless\n")
		b.WriteString(fmt.Sprintf("    uuid: %s\n", e.UUID))
		b.WriteString("    tls: true\n")
		b.WriteString(fmt.Sprintf("    servername: %s\n", e.Domain))
	}

	transportName := gn.Transport
	switch gn.Transport {
	case "grpc":
		serviceName, _, _ := strings.Cut(path, "?")
		if serviceName == "" || serviceName == "/" {
			serviceName = "/"
		}
		b.WriteString("    network: grpc\n")
		b.WriteString("    grpc-opts:\n")
		b.WriteString(fmt.Sprintf("      grpc-service-name: %s\n", serviceName))
	default:
		b.WriteString(fmt.Sprintf("    network: %s\n", transportName))
		if transportName == "ws" {
			b.WriteString("    ws-opts:\n")
			b.WriteString(fmt.Sprintf("      path: %s\n", path))
			b.WriteString("      headers:\n")
			b.WriteString(fmt.Sprintf("        Host: %s\n", e.Domain))
		}
	}

	if gn.SkipCertVerify {
		b.WriteString("    skip-cert-verify: true\n")
	}
	fp := gn.Fingerprint
	if fp != "" {
		b.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", fp))
	}
	return b.String()
}

// FullConfig 生成完整可用的 mihomo 配置（proxies + proxy-groups + rules）。
// 借鉴 subconverter GeneralClashConfig.yml + ACL4SSR_Online.ini 的标准结构。
func FullConfig(entries []SubEntry) string {
	var b strings.Builder
	b.WriteString(mihomoBaseHeader)
	b.WriteString("proxies:\n")
	for _, e := range entries {
		b.WriteString(SubFull(e))
	}
	b.WriteString("\nproxy-groups:\n")
	// 标准分组：节点选择 / 自动选择 / 国外媒体 / 苹果服务 / 微软服务 / 电报信息 / 谷歌FCM / 全球直连 / 全球拦截 / 漏网之鱼
	b.WriteString(proxyGroupsSection(len(entries) > 0))
	b.WriteString("\nrules:\n")
	b.WriteString(defaultRules)
	return b.String()
}

// BuildSub 根据 entries 生成 mihomo 订阅。
// 优先用 FullConfig（完整可用），无需外部模板。
func BuildSub(entries []SubEntry) string {
	return FullConfig(entries)
}

// BuildSubWithConfig 根据 entries 生成 mihomo 订阅，可指定 ACL4SSR 规则集 URL。
// configURL 为空时走默认 FullConfig（内嵌精简规则）；
// 非空时生成扩展版配置：更完整的 proxy-groups + rule-providers 引用 ACL4SSR list。
func BuildSubWithConfig(entries []SubEntry, configURL string) string {
	if configURL == "" {
		return FullConfig(entries)
	}
	return fullConfigWithACL4SSR(entries, configURL)
}

// fullConfigWithACL4SSR 生成引用 ACL4SSR 规则集的扩展版配置。
// 优先尝试 fetch 并解析 configURL 指向的 ACL4SSR ini，生成对应 proxy-groups/rules；
// fetch 失败时回退到内置标准 ACL4SSR 规则集。
func fullConfigWithACL4SSR(entries []SubEntry, configURL string) string {
	var b strings.Builder
	b.WriteString("# Mihomo / Clash Meta 配置 - 由 edt_panel 生成（ACL4SSR 规则集模式）\n")
	b.WriteString(fmt.Sprintf("# 规则集来源: %s\n", configURL))
	b.WriteString(mihomoBaseBody)
	b.WriteString("proxies:\n")
	for _, e := range entries {
		b.WriteString(SubFull(e))
	}

	// 尝试 fetch 并解析 ACL4SSR ini
	var pg, rp, rules string
	parsed := false
	if strings.HasPrefix(configURL, "http") {
		if iniContent, err := FetchACL4SSRIni(configURL); err == nil {
			cfg := ParseACL4SSRIni(iniContent)
			if len(cfg.RuleSets) > 0 || len(cfg.ProxyGroups) > 0 {
				pg, rp, rules = BuildFromACL4SSRConfig(cfg)
				b.WriteString("\n# 以下 proxy-groups/rule-providers/rules 由 ACL4SSR ini 解析生成\n")
				parsed = true
			}
		}
	}
	// 回退到内置标准 ACL4SSR
	if !parsed {
		pg = acl4ssrProxyGroups(len(entries) > 0)
		rp = acl4ssrRuleProviders
		rules = acl4ssrRules
	}

	b.WriteString("\n")
	b.WriteString(pg)
	b.WriteString("\n")
	b.WriteString(rp)
	b.WriteString("\n")
	b.WriteString(rules)
	return b.String()
}

// mihomoBaseBody 不含注释的 base header（避免重复注释）。
const mihomoBaseBody = `mixed-port: 7890
allow-lan: true
mode: Rule
log-level: info
ipv6: true
external-controller: 127.0.0.1:9090

dns:
  enable: true
  ipv6: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - localhost.ptlogin2.qq.com
  nameserver:
    - https://223.5.5.5/dns-query
    - https://1.12.12.12/dns-query
  fallback:
    - https://8.8.8.8/dns-query
    - https://1.1.1.1/dns-query
  fallback-filter:
    geoip: true
    geoip-code: CN

`

const acl4ssrRuleProviders = `  LocalAreaNetwork:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/LocalAreaNetwork.list
    path: ./ruleset/LocalAreaNetwork.list
    interval: 86400
  UnBan:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/UnBan.list
    path: ./ruleset/UnBan.list
    interval: 86400
  BanAD:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/BanAD.list
    path: ./ruleset/BanAD.list
    interval: 86400
  BanProgramAD:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/BanProgramAD.list
    path: ./ruleset/BanProgramAD.list
    interval: 86400
  GoogleFCM:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Ruleset/GoogleFCM.list
    path: ./ruleset/GoogleFCM.list
    interval: 86400
  GoogleCN:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/GoogleCN.list
    path: ./ruleset/GoogleCN.list
    interval: 86400
  Microsoft:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Microsoft.list
    path: ./ruleset/Microsoft.list
    interval: 86400
  Apple:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Apple.list
    path: ./ruleset/Apple.list
    interval: 86400
  Telegram:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/Telegram.list
    path: ./ruleset/Telegram.list
    interval: 86400
  ProxyMedia:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/ProxyMedia.list
    path: ./ruleset/ProxyMedia.list
    interval: 86400
  ProxyLite:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/ProxyLite.list
    path: ./ruleset/ProxyLite.list
    interval: 86400
  ChinaDomain:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/ChinaDomain.list
    path: ./ruleset/ChinaDomain.list
    interval: 86400
  ChinaCompanyIp:
    type: http
    behavior: classical
    url: https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/ChinaCompanyIp.list
    path: ./ruleset/ChinaCompanyIp.list
    interval: 86400
`

const acl4ssrRules = `# ACL4SSR 规则集（rule-providers 引用，运行时自动拉取）
- RULE-SET,LocalAreaNetwork,🎯 全球直连
- RULE-SET,UnBan,🎯 全球直连
- RULE-SET,BanAD,🛑 全球拦截
- RULE-SET,BanProgramAD,🍃 应用净化
- RULE-SET,GoogleFCM,📢 谷歌FCM
- RULE-SET,GoogleCN,🎯 全球直连
- RULE-SET,Microsoft,Ⓜ️ 微软服务
- RULE-SET,Apple,🍎 苹果服务
- RULE-SET,Telegram,📲 电报信息
- RULE-SET,ProxyMedia,🌍 国外媒体
- RULE-SET,ProxyLite,🚀 节点选择
- RULE-SET,ChinaDomain,🎯 全球直连
- RULE-SET,ChinaCompanyIp,🎯 全球直连
- GEOIP,CN,🎯 全球直连
- MATCH,🐟 漏网之鱼
`

// acl4ssrProxyGroups 生成 ACL4SSR 规则集对应的 proxy-groups 段（依是否有节点切换占位策略）。
func acl4ssrProxyGroups(hasEntries bool) string {
	if !hasEntries {
		return `  - name: 🚀 节点选择
    type: select
    proxies: [DIRECT]
`
	}
	return `  - name: 🚀 节点选择
    type: select
    proxies: [♻️ 自动选择, DIRECT]
  - name: ♻️ 自动选择
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    use: [edt_panel]
  - name: 🌍 国外媒体
    type: select
    proxies: [🚀 节点选择, ♻️ 自动选择, 🎯 全球直连]
  - name: 📲 电报信息
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连]
  - name: Ⓜ️ 微软服务
    type: select
    proxies: [🎯 全球直连, 🚀 节点选择]
  - name: 🍎 苹果服务
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连]
  - name: 📢 谷歌FCM
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连, ♻️ 自动选择]
  - name: 🎯 全球直连
    type: select
    proxies: [DIRECT, 🚀 节点选择, ♻️ 自动选择]
  - name: 🛑 全球拦截
    type: select
    proxies: [REJECT, DIRECT]
  - name: 🍃 应用净化
    type: select
    proxies: [REJECT, DIRECT]
  - name: 🐟 漏网之鱼
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连, ♻️ 自动选择]
proxy-providers:
  edt_panel:
    type: file
    path: ./proxy_providers/edt.yaml
    health-check:
      enable: true
      url: http://www.gstatic.com/generate_204
      interval: 300
`
}

// BuildSubWithTemplate 兼容原项目：用 template.yaml 做替换。
func BuildSubWithTemplate(templatePath string, entries []SubEntry) (string, error) {
	var b strings.Builder
	for _, e := range entries {
		snippet, err := SubWithTemplate(templatePath, e)
		if err != nil {
			return "", err
		}
		b.WriteString(snippet)
	}
	return b.String(), nil
}

// splitHostPortStr 简单拆分 host:port（不做 IPv6 复杂处理，mihomo 节点 server 一般是单 host）。
func splitHostPortStr(s string) (string, int, bool) {
	if s == "" {
		return "", 0, false
	}
	idx := strings.LastIndex(s, ":")
	if idx == -1 {
		return s, 0, false
	}
	port := 0
	for _, c := range s[idx+1:] {
		if c < '0' || c > '9' {
			return s, 0, false
		}
		port = port*10 + int(c-'0')
	}
	return s[:idx], port, true
}

const mihomoBaseHeader = `# Mihomo / Clash Meta 配置 - 由 edt_panel 生成
# 生成时间由服务端动态填充
mixed-port: 7890
allow-lan: true
mode: Rule
log-level: info
ipv6: true
external-controller: 127.0.0.1:9090

dns:
  enable: true
  ipv6: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - localhost.ptlogin2.qq.com
  nameserver:
    - https://223.5.5.5/dns-query
    - https://1.12.12.12/dns-query
  fallback:
    - https://8.8.8.8/dns-query
    - https://1.1.1.1/dns-query
  fallback-filter:
    geoip: true
    geoip-code: CN

`

// proxyGroupsSection 生成精简内嵌规则对应的 proxy-groups 段。
func proxyGroupsSection(hasEntries bool) string {
	if !hasEntries {
		// 没节点时给一个占位，避免空 group 报错
		return `  - name: 🚀 节点选择
    type: select
    proxies: [DIRECT]
`
	}
	return `  - name: 🚀 节点选择
    type: select
    proxies: [♻️ 自动选择, DIRECT]
    use: []
  - name: ♻️ 自动选择
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    proxies: []
    use: [edt_panel]
  - name: 🌍 国外媒体
    type: select
    proxies: [🚀 节点选择, ♻️ 自动选择, 🎯 全球直连]
  - name: 📲 电报信息
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连]
  - name: Ⓜ️ 微软服务
    type: select
    proxies: [🎯 全球直连, 🚀 节点选择]
  - name: 🍎 苹果服务
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连]
  - name: 📢 谷歌FCM
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连, ♻️ 自动选择]
  - name: 🎯 全球直连
    type: select
    proxies: [DIRECT, 🚀 节点选择, ♻️ 自动选择]
  - name: 🛑 全球拦截
    type: select
    proxies: [REJECT, DIRECT]
  - name: 🍃 应用净化
    type: select
    proxies: [REJECT, DIRECT]
  - name: 🐟 漏网之鱼
    type: select
    proxies: [🚀 节点选择, 🎯 全球直连, ♻️ 自动选择]

proxy-providers:
  edt_panel:
    type: file
    path: ./proxy_providers/edt.yaml
    health-check:
      enable: true
      url: http://www.gstatic.com/generate_204
      interval: 300
`

}

const defaultRules = `# ACL4SSR 精简版规则（内嵌）
- RULE-SET,LocalAreaNetwork,🎯 全球直连
- RULE-SET,UnBan,🎯 全球直连
- RULE-SET,BanAD,🛑 全球拦截
- RULE-SET,BanProgramAD,🍃 应用净化
- RULE-SET,GoogleFCM,📢 谷歌FCM
- RULE-SET,GoogleCN,🎯 全球直连
- RULE-SET,Microsoft,Ⓜ️ 微软服务
- RULE-SET,Apple,🍎 苹果服务
- RULE-SET,Telegram,📲 电报信息
- RULE-SET,ProxyMedia,🌍 国外媒体
- RULE-SET,ProxyLite,🚀 节点选择
- RULE-SET,ChinaDomain,🎯 全球直连
- RULE-SET,ChinaCompanyIp,🎯 全球直连
- GEOIP,CN,🎯 全球直连
- MATCH,🐟 漏网之鱼
`

// mappedSSPort SS 非 TLS 时的端口映射（TLS 组 → noTLS 组，对齐生态订阅输出）。
func mappedSSPort(port int) int {
	orig := strconv.Itoa(port)
	if mapped := config.SSMapPort(orig); mapped != orig {
		if n, err := strconv.Atoi(mapped); err == nil {
			return n
		}
	}
	return port
}
