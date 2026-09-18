// trace.go - 通过 Cloudflare /cdn-cgi/trace 探测出口区域码（cfcolo）。
//
// 用途：节点名称中不含区域码时，为「分区域 ProxyIP」匹配提供区域依据。
// trace 响应为 key=value 文本，其中 cfcolo=HKG 即边缘节点所在机场码；
// 也可从响应头 cf-ray 尾部取到同样的三字码（此处以 trace 为主）。
package module

import (
	"crypto/tls"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// reCfColo 匹配 trace 文本中的 cfcolo=XXX（XXX 为大写三字码）。
var reCfColo = regexp.MustCompile(`(?m)^cfcolo=([A-Z]{3})\s*$`)

// traceTimeout 单次探测的连接+响应超时。
const traceTimeout = 3 * time.Second

// FetchCfColo 请求 http(s)://host/cdn-cgi/trace 解析 cfcolo。
// 依次尝试 http:80 与 https:443（证书跳过校验：优选 IP 多为裸 IP 直连），
// 任一成功即返回大写三字码；全部失败返回空串。
// host 允许携带端口（如 1.2.3.4:8443），探测前会剥离，仅保留主机名/裸 IP。
func FetchCfColo(host string) string {
	host = stripPort(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	client := &http.Client{
		Timeout: traceTimeout,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: traceTimeout,
		},
	}
	// 依次尝试 http 与 https
	for _, scheme := range []string{"http", "https"} {
		code := tryTrace(client, scheme+"://"+host+"/cdn-cgi/trace")
		if code != "" {
			return code
		}
	}
	return ""
}

// tryTrace 单次 trace 请求与解析。
func tryTrace(client *http.Client, url string) string {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "edt-panel/1.0 (+https://github.com/masgzy/edgetunnel_panel)")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return ""
	}
	if m := reCfColo.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// stripPort 去掉 host 上的端口（支持 IPv6 [..]:port 形式）。
func stripPort(host string) string {
	if host == "" {
		return ""
	}
	// IPv6 字面量 [..]:port
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end != -1 {
			return host[1:end]
		}
		return host
	}
	if strings.Count(host, ":") == 1 {
		// 单个冒号视为 host:port；多个冒号为裸 IPv6（无端口）保持原样
		return host[:strings.Index(host, ":")]
	}
	return host
}
