// mihomo.go - Mihomo/Clash Meta 订阅生成。
//
// 移植 subconverter 的 Clash 导出语义（src/generator/config/subexport.cpp proxyToClash）：
//  1. 节点渲染：公共字段（name/server/port/udp）+ 按协议展开字段；
//     字段名遵循 mihomo (Clash.Meta) 官方规范（vless: servername + client-fingerprint，
//     trojan: password + sni，ss: cipher + v2ray-plugin）。
//     注：subconverter 至今不支持 VLESS，vless/xhttp 分支按 mihomo 官方文档补齐，
//     与订阅链接（gen_link.go）同构：trojan 密码 = sha224(UUID)（生态入口校验契约）。
//  2. 组生成：proxy-groups 直接列出节点名（groupGenerate 语义：正则匹配节点名、
//     []引用其他组、空组补 DIRECT），不再使用 proxy-providers file 自引用。
//  3. 规则：内置精简 ACL4SSR；或经 external config URL（ACL4SSR ini）解析生成
//     proxy-groups / rule-providers / rules（rulesetToClash 语义）。
//  4. base 通用段（port/dns 等）内嵌，无需任何外部模板文件。
package module

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"edt/internal/config"
)

// SubEntry mihomo 生成所需的单个节点信息。
type SubEntry struct {
	Name    string // 节点名（已加 emoji）
	UUID    string // 用户凭据（vless uuid / trojan、ss 密码来源）
	ProxyIP string // 可为空（DIRECT）
	YxIP    string // 优选 IP/域名（authority）
	Domain  string // SNI / Host
	Port    int    // 默认端口

	// Gen 非空时按生成配置渲染（协议/传输/证书校验/指纹等）；
	// nil 时保持历史行为：vless+ws+tls，路径固定带 ed=2560，指纹 chrome。
	Gen *config.GenSettings
}

// yamlStr 输出 YAML 标量：白名单字符集之外的值加双引号（含 emoji 的节点名
// 不需要引号；含逗号/冒号/引号等则必须包裹）。strconv.Quote 的转义风格与
// YAML 双引号标量兼容（\n \t \" \\ 等）。
func yamlStr(s string) string {
	if s == "" {
		return `""`
	}
	for _, c := range s {
		safe := c == ' ' || c == '-' || c == '.' || c == '/' || c == '_' ||
			c == '?' || c == '=' || c == '&' || c == '+' || c == '(' || c == ')' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c > 127 // emoji 与 CJK 等非 ASCII 直接输出
		if !safe {
			return strconv.Quote(s)
		}
	}
	// 以空格开头/结尾或纯数字需要引号，避免 YAML 解析歧义
	trimmed := strings.TrimSpace(s)
	if trimmed != s || isAllDigits(trimmed) {
		return strconv.Quote(s)
	}
	return s
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// trojanPassword trojan 用户凭据 = sha224(UUID)（对齐生态运行时入口校验，
// 与订阅链接 gen_link.go 同构）。
func trojanPassword(uuid string) string {
	sum := sha256.Sum224([]byte(uuid))
	return hex.EncodeToString(sum[:])
}

// SubFull 生成单个节点的 mihomo proxies 片段（yaml 缩进 0）。
// 移植 subconverter proxyToClash 的字段语义；vless/xhttp 按 mihomo 官方字段。
//
// 输出形如：
//
//   - name: 🇯🇵 日本 NRT
//     server: example.com
//     port: 8443
//     type: vless
//     uuid: xxx
//     udp: true
//     tls: true
//     servername: example.com
//     network: ws
//     ws-opts:
//     path: /proxyip=1.2.3.4?ed=2560
//     headers:
//     Host: example.com
//     client-fingerprint: chrome
func SubFull(e SubEntry) string {
	serverHost := strings.Trim(e.YxIP, "[]")
	serverPort := e.Port
	if h, p, ok := splitHostPortStr(serverHost); ok && p != 0 {
		serverHost = h
		serverPort = p
	}
	g := e.Gen
	if g != nil {
		gn := g.Normalized()
		if gn.Protocol == "ss" && !gn.SSTLS {
			// SS 非 TLS：TLS 端口组映射为对应 noTLS 端口（对齐生态订阅输出）。
			serverPort = mappedSSPort(serverPort)
		}
	}

	var b strings.Builder
	b.WriteString("  - name: " + yamlStr(e.Name) + "\n")
	b.WriteString("    server: " + yamlStr(serverHost) + "\n")
	b.WriteString("    port: " + strconv.Itoa(serverPort) + "\n")
	b.WriteString("    udp: true\n")

	if g == nil {
		// 历史行为：vless + ws + tls + ed=2560 + chrome
		path := fmt.Sprintf("/proxyip=%s?ed=2560", e.ProxyIP)
		if e.ProxyIP == "" || e.ProxyIP == "DIRECT" {
			path = "/?ed=2560"
		}
		b.WriteString("    type: vless\n")
		b.WriteString("    uuid: " + e.UUID + "\n")
		b.WriteString("    tls: true\n")
		b.WriteString("    servername: " + yamlStr(e.Domain) + "\n")
		b.WriteString("    network: ws\n")
		b.WriteString("    ws-opts:\n")
		b.WriteString("      path: " + yamlStr(path) + "\n")
		b.WriteString("      headers:\n")
		b.WriteString("        Host: " + yamlStr(e.Domain) + "\n")
		b.WriteString("    client-fingerprint: chrome\n")
		return b.String()
	}

	gn := g.Normalized()
	path := gn.TransportPath("edt", e.ProxyIP) // 与订阅链接同款路径组装
	switch gn.Protocol {
	case "ss":
		// ss 以 v2ray-plugin 承载 ws(+tls)（与订阅链接同构；type:ss 不存在 network:ws 形式）。
		b.WriteString("    type: ss\n")
		b.WriteString("    cipher: " + yamlStr(gn.SSCipher) + "\n")
		b.WriteString("    password: " + yamlStr(e.UUID) + "\n")
		b.WriteString("    plugin: v2ray-plugin\n")
		b.WriteString("    plugin-opts:\n")
		b.WriteString("      mode: websocket\n")
		b.WriteString("      host: " + yamlStr(e.Domain) + "\n")
		b.WriteString("      path: " + yamlStr(path) + "\n")
		b.WriteString("      tls: " + boolStr(gn.SSTLS) + "\n")
		if gn.SkipCertVerify {
			b.WriteString("      skip-cert-verify: true\n")
		}
		return b.String()
	case "trojan":
		// subconverter: password + sni；密码 = sha224(UUID)（生态入口校验契约）。
		b.WriteString("    type: trojan\n")
		b.WriteString("    password: " + trojanPassword(e.UUID) + "\n")
		b.WriteString("    tls: true\n")
		b.WriteString("    sni: " + yamlStr(e.Domain) + "\n")
	default: // vless
		b.WriteString("    type: vless\n")
		b.WriteString("    uuid: " + e.UUID + "\n")
		b.WriteString("    tls: true\n")
		b.WriteString("    servername: " + yamlStr(e.Domain) + "\n")
	}

	switch gn.Transport {
	case "ws":
		b.WriteString("    network: ws\n")
		b.WriteString("    ws-opts:\n")
		b.WriteString("      path: " + yamlStr(path) + "\n")
		b.WriteString("      headers:\n")
		b.WriteString("        Host: " + yamlStr(e.Domain) + "\n")
	case "grpc":
		// 生态契约：serviceName 无斜杠（gRPC 入口按 serviceName 匹配）。
		serviceName := strings.TrimPrefix(strings.SplitN(path, "?", 2)[0], "/")
		b.WriteString("    network: grpc\n")
		b.WriteString("    grpc-opts:\n")
		b.WriteString("      grpc-service-name: " + yamlStr(serviceName) + "\n")
	case "xhttp":
		// mihomo xhttp 传输层（mode 按生态默认 stream-one）。
		host := strings.SplitN(path, "?", 2)[0]
		b.WriteString("    network: xhttp\n")
		b.WriteString("    xhttp-opts:\n")
		b.WriteString("      mode: stream-one\n")
		b.WriteString("      path: " + yamlStr(host) + "\n")
		b.WriteString("      host: " + yamlStr(e.Domain) + "\n")
	}

	if gn.SkipCertVerify {
		b.WriteString("    skip-cert-verify: true\n")
	}
	if gn.Fingerprint != "" && gn.Protocol != "ss" {
		b.WriteString("    client-fingerprint: " + yamlStr(gn.Fingerprint) + "\n")
	}
	return b.String()
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// FullConfig 生成完整可用的 mihomo 配置（proxies + proxy-groups + rules）。
// 组语义移植 subconverter groupGenerate：节点名直接列出，空组补 DIRECT。
func FullConfig(entries []SubEntry) string {
	names := entryNames(entries)
	var b strings.Builder
	b.WriteString(mihomoBaseHeader)
	b.WriteString("proxies:\n")
	for _, e := range entries {
		b.WriteString(SubFull(e))
	}
	b.WriteString("\n")
	b.WriteString(defaultProxyGroups(names))
	b.WriteString("\n")
	b.WriteString(acl4ssrRuleProviders)
	b.WriteString("\n")
	b.WriteString(defaultRules)
	return b.String()
}

// BuildSub 根据 entries 生成 mihomo 订阅。
func BuildSub(entries []SubEntry) string {
	return FullConfig(entries)
}

// BuildSubWithConfig 根据 entries 生成 mihomo 订阅，可指定 ACL4SSR 规则集 URL。
// configURL 为空时走默认 FullConfig（内嵌精简规则）；
// 非空时生成扩展版配置：external config（ACL4SSR ini）解析生成 proxy-groups +
// rule-providers + rules（subconverter external config 语义）；
// fetch/解析失败时回退到内置标准组与规则。
func BuildSubWithConfig(entries []SubEntry, configURL string) string {
	if configURL == "" {
		return FullConfig(entries)
	}

	var b strings.Builder
	b.WriteString("# Mihomo / Clash Meta 配置 - 由 edt_panel 生成（ACL4SSR 规则集模式）\n")
	b.WriteString(fmt.Sprintf("# 规则集来源: %s\n", configURL))
	b.WriteString(mihomoBaseBody)
	b.WriteString("proxies:\n")
	for _, e := range entries {
		b.WriteString(SubFull(e))
	}

	names := entryNames(entries)
	var pg, rp, rules string
	parsed := false
	if strings.HasPrefix(configURL, "http") {
		if iniContent, err := FetchACL4SSRIni(configURL); err == nil {
			cfg := ParseACL4SSRIni(iniContent)
			if len(cfg.RuleSets) > 0 || len(cfg.ProxyGroups) > 0 {
				pg, rp, rules = BuildFromACL4SSRConfig(cfg, entries)
				b.WriteString("\n# 以下 proxy-groups/rule-providers/rules 由 external config 解析生成\n")
				parsed = true
			}
		}
	}
	// 回退到内置标准 ACL4SSR 组与规则
	if !parsed {
		pg = defaultProxyGroupsYAML(names)
		rp = acl4ssrRuleProviders
		rules = defaultRules
	}

	b.WriteString("\n")
	b.WriteString(pg)
	b.WriteString("\n")
	if rp != "" {
		b.WriteString(rp)
		b.WriteString("\n")
	}
	b.WriteString(rules)
	return b.String()
}

// entryNames 提取节点名列表（供组展开）。
func entryNames(entries []SubEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

// defaultProxyGroups 内置默认组（对齐 subconverter ACL4SSR_Online.ini 中文模板
// 的组结构，节点名直接列出）。
func defaultProxyGroups(names []string) string {
	return defaultProxyGroupsYAML(names)
}

// defaultProxyGroupsYAML 输出 proxy-groups 段。
// 组引用关系与 subconverter ACL4SSR 中文模板一致：
// 🚀 节点选择 / ♻️ 自动选择 / 🌍 国外媒体 / 📲 电报信息 / Ⓜ️ 微软服务 /
// 🍎 苹果服务 / 📢 谷歌FCM / 🎯 全球直连 / 🛑 全球拦截 / 🍃 应用净化 / 🐟 漏网之鱼。
func defaultProxyGroupsYAML(names []string) string {
	var b strings.Builder
	b.WriteString("proxy-groups:\n")

	// 🚀 节点选择：select [自动选择, DIRECT] + 全部节点（subconverter: `select`[]♻️ 自动选择`[]DIRECT`.*）
	writeGroupHeader(&b, "🚀 节点选择", "select", "")
	b.WriteString("    proxies:\n")
	b.WriteString("      - ♻️ 自动选择\n")
	b.WriteString("      - DIRECT\n")
	writeNodeRefs(&b, names)
	b.WriteString("\n")

	// ♻️ 自动选择：url-test 全部节点（subconverter: `url-test`.*`url`300,50）
	writeGroupHeader(&b, "♻️ 自动选择", "url-test", "http://www.gstatic.com/generate_204")
	b.WriteString("    interval: 300\n")
	b.WriteString("    tolerance: 50\n")
	b.WriteString("    proxies:\n")
	writeNodeRefsOrDIRECT(&b, names)
	b.WriteString("\n")

	// 间接组：只引用其他组，不引用节点
	selectGroups := [][2]string{
		{"🌍 国外媒体", "🚀 节点选择,♻️ 自动选择,🎯 全球直连"},
		{"📲 电报信息", "🚀 节点选择,🎯 全球直连"},
		{"Ⓜ️ 微软服务", "🎯 全球直连,🚀 节点选择"},
		{"🍎 苹果服务", "🚀 节点选择,🎯 全球直连"},
		{"📢 谷歌FCM", "🚀 节点选择,🎯 全球直连,♻️ 自动选择"},
		{"🎯 全球直连", "DIRECT,🚀 节点选择"},
		{"🛑 全球拦截", "REJECT,DIRECT"},
		{"🍃 应用净化", "REJECT,DIRECT"},
		{"🐟 漏网之鱼", "🚀 节点选择,🎯 全球直连"},
	}
	for _, g := range selectGroups {
		writeGroupHeader(&b, g[0], "select", "")
		b.WriteString("    proxies:\n")
		for _, ref := range strings.Split(g[1], ",") {
			b.WriteString("      - " + yamlStr(ref) + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func writeGroupHeader(b *strings.Builder, name, typ, url string) {
	b.WriteString("  - name: " + yamlStr(name) + "\n")
	b.WriteString("    type: " + typ + "\n")
	if url != "" {
		b.WriteString("    url: " + url + "\n")
	}
}

// writeNodeRefs 列出全部节点名。
func writeNodeRefs(b *strings.Builder, names []string) {
	for _, n := range names {
		b.WriteString("      - " + yamlStr(n) + "\n")
	}
}

// writeNodeRefsOrDIRECT 无节点时补 DIRECT（subconverter groupGenerate 语义：
// filtered_nodelist 为空时 emplace_back("DIRECT")）。
func writeNodeRefsOrDIRECT(b *strings.Builder, names []string) {
	if len(names) == 0 {
		b.WriteString("      - DIRECT\n")
		return
	}
	writeNodeRefs(b, names)
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
# 节点与分组动态生成；规则为内置精简 ACL4SSR（rule-providers 运行时拉取）。
# 可通过 /mihomo?config=<ACL4SSR ini URL> 切换规则集模板。
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

const acl4ssrRuleProviders = `rule-providers:
  LocalAreaNetwork:
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

const defaultRules = `rules:
# ACL4SSR 精简版规则（内嵌，rule-providers 运行时自动拉取）
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
