// 文本处理工具（host:port 拆分、authority 格式化、去重等）。
// 对应原 Python 项目的 edt_app/utils/text.py。
package service

import (
	"net"
	"strconv"
	"strings"
)

// SplitHostPort 把 host、host:port、[ipv6]、[ipv6]:port 拆成 (host, port)。
// 不带端口时返回 (host, 0)；解析失败时原样返回 (value, 0)。
func SplitHostPort(value string) (string, int) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", 0
	}

	// IPv6 literal（可能带端口）：[..] 或 [..]:port
	if strings.HasPrefix(text, "[") {
		end := strings.Index(text, "]")
		if end == -1 {
			return text, 0
		}
		host := text[1:end]
		rest := text[end+1:]
		if strings.HasPrefix(rest, ":") {
			portStr := rest[1:]
			if port, err := strconv.Atoi(portStr); err == nil {
				return host, port
			}
			return text, 0
		}
		return host, 0
	}

	// IPv4 / 域名 + 可选端口
	rfind := strings.LastIndex(text, ":")
	if rfind != -1 {
		head := text[:rfind]
		portStr := text[rfind+1:]
		// 多个冒号说明是裸 IPv6（未加方括号），没有显式端口
		if strings.Contains(head, ":") {
			return text, 0
		}
		// 端口必须是纯数字
		isDigit := portStr != "" && allDigit(portStr)
		if isDigit {
			port, _ := strconv.Atoi(portStr)
			if port >= 1 && port <= 65535 {
				return head, port
			}
		}
		return text, 0
	}
	return text, 0
}

// allDigit 报告字符串是否全为数字（非空）。
func allDigit(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// FormatAuthority 返回可直接拼进 URL authority 的字符串，已处理 IPv6 方括号与端口覆盖。
func FormatAuthority(yxIP string, defaultPort int) string {
	if strings.TrimSpace(yxIP) == "" {
		return ":" + strconv.Itoa(defaultPort)
	}
	host, port := SplitHostPort(yxIP)
	if host == "" {
		p := defaultPort
		if port != 0 {
			p = port
		}
		return ":" + strconv.Itoa(p)
	}
	effectivePort := defaultPort
	if port != 0 {
		effectivePort = port
	}
	// 识别是否为 IPv6 literal，若是则包方括号
	if isIPv6(host) {
		return "[" + host + "]:" + strconv.Itoa(effectivePort)
	}
	return host + ":" + strconv.Itoa(effectivePort)
}

// isIPv6 粗判 host 是否为 IPv6 字面量（含冒号即视为 IPv6）。
func isIPv6(host string) bool {
	// 去掉可能的 zone id
	if i := strings.Index(host, "%"); i != -1 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}

// MarkDuplicates 对重名项加 (N) 后缀，使所有项唯一。
func MarkDuplicates(items []string) []string {
	seen := map[string]int{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		count := seen[item]
		seen[item] = count + 1
		if count == 0 {
			result = append(result, item)
		} else {
			result = append(result, item+" ("+strconv.Itoa(count)+")")
		}
	}
	return result
}

// DetectIPType 检测 IP 地址类型。
func DetectIPType(ipStr string) string {
	if ipStr == "" {
		return "invalid"
	}
	host, _ := SplitHostPort(strings.TrimSpace(ipStr))
	if host == "" {
		return "invalid"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "invalid"
	}
	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}
