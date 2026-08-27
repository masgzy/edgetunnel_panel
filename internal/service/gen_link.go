// gen_link.go - 订阅节点链接拼装器。
//
// 按「生成配置」（config.GenSettings，来源可为面板 config.json 自动获取或手动设置）
// 把优选 IP 条目渲染为 vless/trojan/ss 分享链接。
// 拼装规则与 edgetunnel 生态订阅端点保持一致：
//   - TLS 分片：Shadowrocket → fragment=1,40-60,30-50,tlshello；Happ → fragment=3,1,tlshello
//   - 0-RTT / SS enc / 随机伪装路径：见 config.GenSettings.TransportPath
//   - ECH：ech=SNI+DNS（URL 编码）
//   - gRPC：type=grpc&mode=gun|multi、serviceName 替代 path（无斜杠）、authority 替代 host
//   - xhttp：type=xhttp&mode=stream-one
//   - 证书校验：allowInsecure=1
//   - trojan 用户名 = sha224(UUID)（对齐生态运行时入口校验）；ss 密码 = UUID，
//     经 v2ray-plugin 承载 ws+tls，加密方式写路径 enc 参数
package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// transportView 返回 type 参数值与路径字段名（gRPC 用 serviceName 并以 authority 承载域名字段）。
func transportView(g config.GenSettings) (typeVal, pathField, hostField string) {
	switch g.Transport {
	case "grpc":
		mode := g.GRPCMode
		if mode != "gun" && mode != "multi" {
			mode = "gun"
		}
		return "grpc&mode=" + mode, "serviceName", "authority"
	case "xhttp":
		return "xhttp&mode=stream-one", "path", "host"
	default:
		return "ws", "path", "host"
	}
}

// BuildNodeLink 按生成配置渲染单个分享链接。
// format 为订阅模板格式（edt|snippets），仅影响反代路径模板的选择。
func BuildNodeLink(g config.GenSettings, format string, p LinkParams) string {
	g = g.Normalized()

	typeVal, pathField, hostField := transportView(g)
	rawPath := g.TransportPath(format, p.ProxyIP)

	frag := FragmentParam(g)
	ech := ECHParam(g)
	insec := allowInsecureParam(g)

	switch g.Protocol {
	case "ss":
		// SIP002 + v2ray-plugin（承载 ws+tls）：密码即用户凭据，host 取连接目标主机部分。
		userInfo := base64.StdEncoding.EncodeToString(
			[]byte(config.SSCipher + ":" + p.UUID))
		hostPart := p.Authority
		if i := strings.LastIndex(hostPart, ":"); i != -1 && !strings.Contains(hostPart[i:], "]") {
			hostPart = hostPart[:i]
		}
		return fmt.Sprintf(
			"ss://%s@%s?plugin=v2%s#%s",
			userInfo,
			p.Authority,
			url.QueryEscape(fmt.Sprintf(
				"ray-plugin;mode=websocket;host=%s;path=%s;tls;mux=0",
				hostPart, rawPath)),
			url.QueryEscape(p.Name),
		)

	case "trojan":
		sum := sha256.Sum224([]byte(p.UUID))
		return fmt.Sprintf(
			"trojan://%s@%s/?security=tls&type=%s&%s=%s&sni=%s&fp=%s&%s=%s%s%s%s&encryption=none#%s",
			hex.EncodeToString(sum[:]), p.Authority, typeVal,
			hostField, url.QueryEscape(p.Domain),
			url.QueryEscape(p.Domain),
			g.Fingerprint,
			pathField, url.QueryEscape(rawPath),
			frag, ech, insec,
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
			packet+frag+ech+insec,
			g.Fingerprint,
			url.QueryEscape(p.Name))
		return b.String()
	}
}
