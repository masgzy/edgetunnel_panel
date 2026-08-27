// vless.go - VLESS 链接解析与生成。
// 用于节点导入（解析 vless:// 文本批量导入）和导出（生成 vless:// 文本）。
package service

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// VlessNode 表示一个 vless 节点的关键字段（用于导入）。
type VlessNode struct {
	UUID     string
	Host     string // authority 中的 host（可能含端口）
	Port     string
	Path     string
	Host2    string // ws-opts headers Host（即 SNI 域名）
	SNI      string
	Security string
	Name     string // fragment（# 后部分，URL 解码后）
	Raw      string // 原始链接
}

// ParseVlessLink 解析单条 vless:// 链接。
// 格式：vless://uuid@host:port/?params#name
func ParseVlessLink(line string) (VlessNode, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "vless://") {
		return VlessNode{}, fmt.Errorf("不是 vless:// 链接")
	}
	body := strings.TrimPrefix(line, "vless://")

	// 分离 fragment（#name，可能 URL 编码）
	var fragment string
	if idx := strings.LastIndex(body, "#"); idx != -1 {
		fragment = body[idx+1:]
		body = body[:idx]
	}
	// URL 解码 fragment
	if dec, err := url.QueryUnescape(fragment); err == nil {
		fragment = dec
	}

	// 分离 query（?params）
	var queryStr string
	if idx := strings.Index(body, "?"); idx != -1 {
		queryStr = body[idx+1:]
		body = body[:idx]
	}

	// body 剩余 = uuid@authority/path
	// 分离 authority 和 path（path 部分在 query 已处理，这里只取 authority）
	var authority string
	if idx := strings.Index(body, "/"); idx != -1 {
		authority = body[:idx]
	} else {
		authority = body
	}

	// 分离 uuid 和 host:port
	var uuid, hostPort string
	if idx := strings.LastIndex(authority, "@"); idx != -1 {
		uuid = authority[:idx]
		hostPort = authority[idx+1:]
	} else {
		return VlessNode{}, fmt.Errorf("链接缺少 @ 分隔符")
	}

	// 解析 query
	q, _ := url.ParseQuery(queryStr)

	node := VlessNode{
		UUID:     uuid,
		Host:     hostPort,
		Path:     q.Get("path"),
		Host2:    q.Get("host"),
		SNI:      q.Get("sni"),
		Security: q.Get("security"),
		Name:     fragment,
		Raw:      line,
	}
	// 拆分 host:port
	if h, p, ok := splitHostPortSimple(hostPort); ok {
		node.Host = h
		node.Port = p
	}
	// path 可能 URL 编码（%2Fproxyip...），解码用于写入 vless.txt
	if node.Path != "" {
		if dec, err := url.QueryUnescape(node.Path); err == nil {
			node.Path = dec
		}
	}
	return node, nil
}

// ParseVlessBatch 批量解析，跳过空行/注释/无效行，返回成功与失败计数。
func ParseVlessBatch(text string) (nodes []VlessNode, failed int) {
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 支持 base64 整体编码的订阅
		if !strings.HasPrefix(line, "vless://") && !strings.HasPrefix(line, "ss://") {
			// 尝试 base64 解码
			if dec, err := base64.StdEncoding.DecodeString(line); err == nil {
				line = string(dec)
			} else if dec, err := base64.RawStdEncoding.DecodeString(line); err == nil {
				line = string(dec)
			}
		}
		if !strings.HasPrefix(line, "vless://") {
			failed++
			continue
		}
		node, err := ParseVlessLink(line)
		if err != nil {
			failed++
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, failed
}

// splitHostPortSimple 拆分 host:port（简单版，host 不含冒号除非是 IPv6 [..]）。
func splitHostPortSimple(s string) (string, string, bool) {
	if s == "" {
		return "", "", false
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end == -1 {
			return s, "", false
		}
		host := s[1:end]
		rest := s[end+1:]
		if strings.HasPrefix(rest, ":") {
			return host, rest[1:], true
		}
		return host, "", true
	}
	idx := strings.LastIndex(s, ":")
	if idx == -1 {
		return s, "", true
	}
	return s[:idx], s[idx+1:], true
}

// VlessNodeToConfigEntry 把 vless 节点转为 ConfigEntry 便于显示。
// 主要提取 ip（来自 path 的 proxyip 参数）、yx_ip（host:port）、name。
func VlessNodeToConfigEntry(node VlessNode) ConfigEntry {
	entry := ConfigEntry{
		Name: node.Name,
		YxIP: node.Host + ":" + node.Port,
	}
	// 从 path 提取 proxyip：/proxyip=1.2.3.4?ed=2560 或 /snippets/ip=1.2.3.4
	if node.Path != "" {
		path := node.Path
		// 去掉前导 /
		path = strings.TrimPrefix(path, "/")
		// 找 proxyip= 或 ip=
		for _, prefix := range []string{"proxyip=", "ip="} {
			if idx := strings.Index(path, prefix); idx != -1 {
				rest := path[idx+len(prefix):]
				// 截到 ? 或 & 或结尾
				end := len(rest)
				for i, c := range rest {
					if c == '?' || c == '&' {
						end = i
						break
					}
				}
				entry.IP = rest[:end]
				break
			}
		}
	}
	return entry
}
