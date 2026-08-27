// Package module 实现遗留的独立功能模块（auth/flag/mihomo/userinfo）。
// 对应原 Python 项目的 module/ 目录。
package module

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AuthConfig auth 模块依赖的配置。
type AuthConfig struct {
	LoginURL       string
	LoginPassword  string
	ControlDomain  string
	RequestTimeout time.Duration
	AuthCacheFile  string
}

// cacheLine auth.txt 文件结构：第一行 auth token，第二行过期时间。
type cacheLine struct {
	auth     string
	expireAt time.Time
}

// GetAuth 返回有效的 auth token：先查缓存，过期则登录刷新。
func GetAuth(cfg AuthConfig) (string, error) {
	cached, ok := readAuthCache(cfg.AuthCacheFile)
	if ok && time.Now().Before(cached.expireAt) {
		return cached.auth, nil
	}
	return loginAndCache(cfg)
}

// readAuthCache 读取本地 auth 缓存文件；损坏或不存在视为无缓存。
func readAuthCache(path string) (cacheLine, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheLine{}, false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 || lines[0] == "" || lines[1] == "" {
		return cacheLine{}, false
	}
	expireAt, err := parseCookieExpire(lines[1])
	if err != nil {
		return cacheLine{}, false
	}
	return cacheLine{auth: lines[0], expireAt: expireAt}, true
}

// loginAndCache 登录远端面板换取 auth token，写入缓存后返回。
// 登录基于 Set-Cookie 响应头解析 token 与过期时间。
func loginAndCache(cfg AuthConfig) (string, error) {
	body := "password=" + cfg.LoginPassword
	req, err := http.NewRequest("POST", cfg.LoginURL, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("构造登录请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Origin", "https://"+cfg.ControlDomain)
	req.Header.Set("Referer", cfg.LoginURL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: cfg.RequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	// 即便响应非 2xx，也读取 body 以便错误信息
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("登录返回非成功状态码: %d", resp.StatusCode)
	}

	setCookie := resp.Header.Get("Set-Cookie")
	if setCookie == "" {
		return "", fmt.Errorf("登录成功但未返回 Set-Cookie，无法缓存 auth")
	}

	cookie := parseSetCookie(setCookie)
	if cookie.value == "" {
		return "", fmt.Errorf("Set-Cookie 头解析失败")
	}
	maxAge := 0
	if v, err := strconv.Atoi(cookie.maxAge); err == nil {
		maxAge = v
	}
	expireAt := time.Now().Add(time.Duration(maxAge) * time.Second)

	// 写缓存
	if dir := filepath.Dir(cfg.AuthCacheFile); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	expireStr := expireAt.Format("2006-01-02 15:04:05-07:00")
	_ = os.WriteFile(cfg.AuthCacheFile, []byte(cookie.value+"\n"+expireStr), 0o644)
	return cookie.value, nil
}

// setCookieParts 从 Set-Cookie 头解析出的关键字段。
type setCookieParts struct {
	name   string
	value  string
	maxAge string
}

// parseSetCookie 解析单条 Set-Cookie 头（取 name/value/expires）。
func parseSetCookie(setCookieStr string) setCookieParts {
	parts := strings.Split(setCookieStr, "; ")
	if len(parts) == 0 {
		return setCookieParts{}
	}
	result := setCookieParts{}
	nameValue := strings.SplitN(parts[0], "=", 2)
	if len(nameValue) == 2 {
		result.name = nameValue[0]
		result.value = nameValue[1]
	}
	for _, part := range parts[1:] {
		if eq := strings.Index(part, "="); eq != -1 {
			key := strings.ToLower(part[:eq])
			val := part[eq+1:]
			if key == "max-age" {
				result.maxAge = val
			}
		}
	}
	return result
}

// parseCookieExpire 解析缓存文件里的过期时间（带时区偏移，如 +08:00）。
func parseCookieExpire(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("解析过期时间失败 %q: %w", s, lastErr)
}
