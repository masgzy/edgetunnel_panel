// gen_link.go - 订阅节点链接拼装器。
//
// 按「生成配置」（config.GenSettings，来源可为面板 config.json 自动获取或手动设置）
// 把优选 IP 条目渲染为 vless/trojan/ss 分享链接。
// 拼装规则与 edgetunnel 生态订阅端点保持一致：
//   - TLS 分片：Shadowrocket → fragment=1,40-60,30-50,tlshello；Happ → fragment=3,1,tlshello
//   - 0-RTT / SS enc / 随机伪装路径：见 config.GenSettings.TransportPath
//   - ECH：ech=SNI+DNS（URL 编码）
//   - gRPC：type=grpc&mode=gun|multi、serviceName 替代 path（无斜杠）、authority 替代 host
//   - xhttp：type=xhttp&mode=stream-one&extra=<UUID 派生混淆参数（对齐生态）>
//   - 证书校验：allowInsecure=1
//   - trojan 用户名 = sha224(UUID)（对齐生态运行时入口校验）；ss 密码 = UUID，
//     经 v2ray-plugin 承载 ws(+tls)，加密方式写路径 enc 参数；SS 非 TLS 时端口映射
package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"edt/internal/config"
)

// LinkParams 单节点链接拼装输入。
type LinkParams struct {
	UUID      string // 用户凭据（面板 UUID 或静态配置）
	Authority string // 连接目标 host[:port]（优选地址）；可已含端口
	Domain    string // SNI / Host 伪装域名
	ProxyIP   string // 反代 IP（写入路径模板）；空/DIRECT 时用根路径
	Name      string // 节点名称（fragment）
}

// FragmentParam 返回分片查询参数（形如 &fragment=...），未启用时返回空串。
func FragmentParam(g config.GenSettings) string {
	switch g.Fragment {
	case "shadowrocket":
		return "&fragment=" + url.QueryEscape("1,40-60,30-50,tlshello")
	case "happ":
		return "&fragment=" + url.QueryEscape("3,1,tlshello")
	}
	return ""
}

// ECHParam 返回 ECH 查询参数（形如 &ech=...），未启用时返回空串。
func ECHParam(g config.GenSettings) string {
	if !g.ECH || g.ECHDNS == "" {
		return ""
	}
	value := g.ECHDNS
	if g.ECHSNI != "" {
		value = g.ECHSNI + "+" + g.ECHDNS
	}
	return "&ech=" + url.QueryEscape(value)
}

// allowInsecureParam 证书校验开关对应的查询片段（带前导 &）。
func allowInsecureParam(g config.GenSettings) string {
	if g.SkipCertVerify {
		return "&allowInsecure=1"
	}
	return ""
}

// alpnParam 应用层协议协商参数（形如 &alpn=h2%2Chttp%2F1.1），未配置时返回空串。
// 对齐生态订阅输出：面板 config.json 的 ALPN 原样透传（逗号分隔列表整体编码）。
func alpnParam(g config.GenSettings) string {
	alpn := strings.TrimSpace(g.ALPN)
	if alpn == "" {
		return ""
	}
	return "&alpn=" + url.QueryEscape(alpn)
}

// transportView 返回 type 参数值与路径字段名（gRPC 用 serviceName 并以 authority 承载域名字段）。
func transportView(g config.GenSettings, uuid string) (typeVal, pathField, hostField string) {
	switch g.Transport {
	case "grpc":
		mode := g.GRPCMode
		if mode != "gun" && mode != "multi" {
			mode = "gun"
		}
		return "grpc&mode=" + mode, "serviceName", "authority"
	case "xhttp":
		return "xhttp&mode=stream-one" + xhttpExtraParam(uuid), "path", "host"
	default:
		return "ws", "path", "host"
	}
}

// xhttpExtraParam 叉HTTP（xhttp）混淆 extra 参数：
// padding 头/键由 UUID 派生（对齐生态运行时校验逻辑），UUID 非标准长度时不追加。
func xhttpExtraParam(uuid string) string {
	if len(uuid) < 31 {
		return ""
	}
	b, err := json.Marshal(struct {
		ObfsMode  bool   `json:"xPaddingObfsMode"`
		Method    string `json:"xPaddingMethod"`
		Placement string `json:"xPaddingPlacement"`
		Header    string `json:"xPaddingHeader"`
		Key       string `json:"xPaddingKey"`
	}{true, "tokenish", "queryInHeader", uuid[1:7], "_" + uuid[25:31]})
	if err != nil {
		return ""
	}
	return "&extra=" + url.QueryEscape(string(b))
}

// pluginPathEscape v2ray-plugin path 参数转义：= 与 , 前加反斜杠（对齐生态订阅输出）。
func pluginPathEscape(path string) string {
	path = strings.ReplaceAll(path, "=", `\=`)
	return strings.ReplaceAll(path, ",", `\,`)
}

// BuildNodeLink 按生成配置渲染单个分享链接。
// format 为订阅模板格式（edt|snippets），仅影响反代路径模板的选择。
func BuildNodeLink(g config.GenSettings, format string, p LinkParams) string {
	g = g.Normalized()

	typeVal, pathField, hostField := transportView(g, p.UUID)
	rawPath := g.TransportPath(format, p.ProxyIP)

	frag := FragmentParam(g)
	ech := ECHParam(g)
	insec := allowInsecureParam(g)
	alpn := alpnParam(g)

	switch g.Protocol {
	case "ss":
		// SIP002 + v2ray-plugin（承载 ws(+tls)）：密码即用户凭据，host 取连接目标主机部分。
		userInfo := base64.StdEncoding.EncodeToString(
			[]byte(g.SSCipher + ":" + p.UUID))
		addr := p.Authority
		hostPart := addr
		if i := strings.LastIndex(hostPart, ":"); i != -1 && !strings.Contains(hostPart[i:], "]") {
			hostPart = hostPart[:i]
		}
		tlsPart := ";tls"
		if !g.SSTLS {
			// 非 TLS：TLS 端口组映射为对应 noTLS 端口（对齐生态订阅输出）。
			tlsPart = ""
			if i := strings.LastIndex(addr, ":"); i != -1 && !strings.Contains(addr[i:], "]") {
				addr = addr[:i+1] + config.SSMapPort(addr[i+1:])
			} else {
				addr += ":80"
			}
		}
		plugin := fmt.Sprintf("ray-plugin;mode=websocket;host=%s;path=%s%s;mux=0",
			hostPart, pluginPathEscape(rawPath), tlsPart)
		return fmt.Sprintf("ss://%s@%s?plugin=v2%s#%s",
			userInfo, addr, url.QueryEscape(plugin), url.QueryEscape(p.Name))

	case "trojan":
		sum := sha256.Sum224([]byte(p.UUID))
		return fmt.Sprintf(
			"trojan://%s@%s/?security=tls&type=%s&%s=%s&sni=%s&fp=%s&%s=%s%s%s%s&encryption=none#%s",
			hex.EncodeToString(sum[:]), p.Authority, typeVal,
			hostField, url.QueryEscape(p.Domain),
			url.QueryEscape(p.Domain),
			g.Fingerprint,
			pathField, url.QueryEscape(rawPath),
			frag+alpn, ech, insec,
			url.QueryEscape(p.Name),
		)

	default: // vless：保持历史模板字段骨架，按配置注入新参数
		packet := ""
		flow := ""
		trail := "/"
		if g.Transport == "ws" {
			if format != "edt" {
				packet = "&packetEncoding=xudp"
			}
			flow = "&flow="
		} else if g.Transport == "grpc" {
			trail = ""
		}
		var b strings.Builder
		fmt.Fprintf(&b,
			"vless://%s@%s%s?type=%s&encryption=none%s&%s=%s&%s=%s&security=tls&sni=%s%s&fp=%s#%s",
			p.UUID, p.Authority, trail, typeVal, flow,
			hostField, url.QueryEscape(p.Domain),
			pathField, url.QueryEscape(rawPath),
			url.QueryEscape(p.Domain),
			packet+frag+ech+insec+alpn,
			g.Fingerprint,
			url.QueryEscape(p.Name))
		return b.String()
	}
}
