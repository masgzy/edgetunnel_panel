// webauth.go - Web 控制台登录鉴权。
//
// 复用 config.yml 中 auth.login_password 作为管理口令：
//   - 口令为空字符串时完全禁用鉴权（保持旧行为，适合纯内网环境）；
//   - 口令非空时，除登录页与静态资源外，所有页面/API/WebSocket 均需携带会话 Cookie；
//   - 会话令牌为 HMAC-SHA256 签名的到期载荷，密钥随进程启动随机生成，
//     无状态校验、重启后统一失效（管理员重新登录一次即可）。
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 会话与限流参数。
const (
	sessionCookieName = "edt_session"
	sessionTTL        = 7 * 24 * time.Hour
	loginRateWindow   = 5 * time.Minute
	loginRateMax      = 8 // 每 IP 每 5 分钟最多 8 次失败尝试
)

// webAuth Web 登录鉴权器。
type webAuth struct {
	password string // 管理口令（明文，本地单用户工具可接受）；空 = 不启用
	secret   []byte // HMAC 密钥，进程启动时随机生成

	mu    sync.Mutex
	fails map[string]*failRecord // key: 客户端 IP
}

// failRecord 单个客户端 IP 的失败计数（滑动窗口起点）。
type failRecord struct {
	count int
	first time.Time // 窗口起点
}

// newWebAuth 构造鉴权器；随机生成 HMAC 会话签名密钥。
func newWebAuth(password string) *webAuth {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// crypto/rand 失败极罕见，退化为时间种子派生
		secret = []byte(fmt.Sprintf("edt-fallback-%d", time.Now().UnixNano()))
	}
	return &webAuth{password: password, secret: secret, fails: map[string]*failRecord{}}
}

// SetPassword 热更新管理口令（config.yml 保存后的配置重载调用）。
// 同时轮换会话签名密钥并清空失败计数：所有已签发会话立即失效，
// 必须用新口令重新登录——避免旧口令的持有者保留有效会话。
func (a *webAuth) SetPassword(password string) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		secret = []byte(fmt.Sprintf("edt-fallback-%d", time.Now().UnixNano()))
	}
	a.mu.Lock()
	a.password = password
	a.secret = secret
	a.fails = map[string]*failRecord{}
	a.mu.Unlock()
}

// enabled 报告是否启用了控制台鉴权（口令非空）。
func (a *webAuth) enabled() bool { return a.password != "" }

// ----- 会话令牌 -----

// issueSession 生成形如 base64url(payload).hex(hmac) 的令牌。
func (a *webAuth) issueSession() string {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	payload := fmt.Sprintf("%d|%s", time.Now().Add(sessionTTL).Unix(), hex.EncodeToString(nonce))
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifySession 校验令牌完整性与有效期（常数时间比较）。
func (a *webAuth) verifySession(token string) bool {
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return false
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return false
	}
	payload := string(payloadRaw)
	sig, err := hex.DecodeString(token[dot+1:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	var exp int64
	nonceHex := ""
	if n, _ := fmt.Sscanf(payload, "%d|%24s", &exp, &nonceHex); n < 1 {
		return false
	}
	return time.Now().Unix() < exp
}

// ----- 登录频率限制 -----

// clientIP 提取客户端真实 IP（信任反向代理头，本地部署场景可接受）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

// rateLimited 判断该 IP 是否被限制登录；失败返回 true。
func (a *webAuth) rateLimited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.fails[ip]
	if !ok || time.Since(rec.first) > loginRateWindow {
		delete(a.fails, ip)
		return false
	}
	return rec.count >= loginRateMax
}

// recordFail 记录一次登录失败并维护 5 分钟滑动窗口。
func (a *webAuth) recordFail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.fails[ip]
	if !ok || time.Since(rec.first) > loginRateWindow {
		// 防止极端场景下 map 无限增长
		if len(a.fails) > 4096 {
			a.fails = map[string]*failRecord{}
		}
		a.fails[ip] = &failRecord{count: 1, first: time.Now()}
		return
	}
	rec.count++
}

// clearFails 登录成功后清除该 IP 的失败记录。
func (a *webAuth) clearFails(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}

// ----- 登录/登出处理器 -----

// handleLogin GET 显示登录页，POST 校验口令并签发会话 Cookie。
// 支持 ?next= 跳转（仅接受本站路径，防开放重定向）。
func (a *webAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := sanitizeNextPath(r.FormValue("next"))
	if r.Method == http.MethodPost {
		if a.rateLimited(clientIP(r)) {
			http.Error(w, "尝试过于频繁，请 5 分钟后再试", http.StatusTooManyRequests)
			return
		}
		pw := r.FormValue("password")
		// ConstantTimeCompare 相等时返回 1；长度不等直接返回 0，同样恒定时间
		ok := subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
		if !ok {
			a.recordFail(clientIP(r))
			time.Sleep(300 * time.Millisecond) // 轻微减速暴力破解
			http.Redirect(w, r, "/login?err=1&next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		a.clearFails(clientIP(r))
		http.SetCookie(w, a.sessionCookie(r, a.issueSession(), int(sessionTTL.Seconds())))
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	// 已登录访问登录页直接回首页
	if c, err := r.Cookie(sessionCookieName); err == nil && a.verifySession(c.Value) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, loginPageHTML(next, ""))
}

// handleLogout 清除会话 Cookie 并回到登录页。
func (a *webAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, a.sessionCookie(r, "", -1)) // 立即过期
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// sessionCookie 构造会话 Cookie（TLS 或 X-Forwarded-Proto=https 时加 Secure）。
func (a *webAuth) sessionCookie(r *http.Request, token string, maxAge int) *http.Cookie {
	secure := r.TLS != nil
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
		secure = true
	}
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// middleware 包裹整个 mux，未认证请求跳转登录页或返回 401。
func (a *webAuth) middleware(next http.Handler) http.Handler {
	if !a.enabled() {
		return next
	}
	isPublic := func(r *http.Request) bool {
		switch {
		case r.URL.Path == "/login", r.URL.Path == "/logout":
			return true
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			return false
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			return true
		case r.URL.Path == "/manifest.json", r.URL.Path == "/sw.js",
			r.URL.Path == "/favicon.svg", r.URL.Path == "/favicon.ico":
			return true
		case r.URL.Path == "/sub" || strings.HasPrefix(r.URL.Path, "/sub/"),
			r.URL.Path == "/convert":
			// 订阅端点与转换端点必须匿名可达：subconverter 与代理客户端
			// 无法携带控制台会话 Cookie。其安全性依赖订阅 URL 中 UUID 的
			// 保密性（见 README「鉴权与安全」）。
			return true
		}
		return false
	}
	authed := func(r *http.Request) bool {
		c, err := r.Cookie(sessionCookieName)
		return err == nil && a.verifySession(c.Value)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublic(r) || authed(r) {
			next.ServeHTTP(w, r)
			return
		}
		// API 与 WebSocket 返回 401（不重定向）
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/sub") ||
			strings.HasPrefix(r.URL.Path, "/ws") ||
			strings.Contains(r.Header.Get("Accept"), "application/json") ||
			r.Header.Get("Upgrade") == "websocket" {
			writeJSONError(w, http.StatusUnauthorized, "未登录或会话已过期，请重新登录")
			return
		}
		target := "/login?next=" + url.QueryEscape(sanitizeNextPath(r.URL.RequestURI()))
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// sanitizeNextPath 防开放重定向：仅允许站内以单个 "/" 开头的路径。
func sanitizeNextPath(n string) string {
	n = strings.TrimSpace(n)
	if n == "" || !strings.HasPrefix(n, "/") ||
		strings.HasPrefix(n, "//") || strings.ContainsRune(n, '\\') {
		return "/"
	}
	for _, c := range n {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	return n
}

// ----- 登录页（自包含 HTML/CSS，无外部依赖）-----

// loginPageHTML 渲染自包含的 MD3 登录页（明暗自适应，无外部依赖）。
func loginPageHTML(next string, errMsg string) string {
	errBox := ""
	if errMsg != "" {
		errBox = `<div class="err" role="alert">` + htmlEscape(errMsg) + `</div>`
	}
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>edt_panel · 登录</title>
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<style>
:root{
  --bg:#141218; --card:#211f26; --primary:#d0bcff; --on-primary:#381e72;
  --surface-hi:#36343b; --outline:#938f99; --on-surface:#e6e0e9; --on-var:#cac4d0;
  --error:#ffb4ab;
}
@media (prefers-color-scheme: light){
  :root{
    --bg:#fef7ff; --card:#fff; --primary:#6750a4; --on-primary:#fff;
    --surface-hi:#e8def8; --outline:#79747e; --on-surface:#1d1b20; --on-var:#49454f;
    --error:#b3261e;
  }
}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{
  font-family:"Segoe UI",system-ui,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
  background:var(--bg); color:var(--on-surface);
  display:flex;align-items:center;justify-content:center;padding:16px;
}
.card{
  width:100%;max-width:360px;background:var(--card);
  border-radius:28px;padding:40px 32px 32px;
  box-shadow:0 4px 12px rgba(0,0,0,.35);
}
.logo{
  display:flex;flex-direction:column;align-items:center;gap:8px;margin-bottom:28px;
}
.logo .mark{
  width:72px;height:72px;border-radius:22px;display:flex;align-items:center;justify-content:center;
  background:linear-gradient(135deg,var(--primary),var(--surface-hi));
  color:var(--on-primary);font-size:30px;font-weight:700;letter-spacing:.5px;
}
.logo h1{font-size:22px;font-weight:500}
.logo p{font-size:13px;color:var(--on-var)}
label{display:block;font-size:12px;color:var(--on-var);margin:18px 2px 6px}
input[type=password]{
  width:100%;padding:14px 16px;font-size:15px;color:var(--on-surface);
  background:color-mix(in srgb,var(--surface-hi) 55%,transparent);
  border:1px solid var(--outline);border-radius:14px;outline:none;
  transition:border-color .2s,box-shadow .2s;
}
input[type=password]:focus{border-color:var(--primary);box-shadow:0 0 0 1px var(--primary)}
.err{
  margin-top:16px;padding:10px 14px;border-radius:12px;font-size:13px;
  background:color-mix(in srgb,var(--error) 18%,transparent);
  color:var(--error);
}
button{
  width:100%;margin-top:24px;padding:14px;border:none;border-radius:9999px;
  font-size:15px;font-weight:600;letter-spacing:.3px;cursor:pointer;
  background:var(--primary);color:var(--on-primary);
  transition:filter .2s,transform .1s;
}
button:hover{filter:brightness(1.08)}
button:active{transform:scale(.98)}
.hint{margin-top:20px;font-size:12px;line-height:1.7;color:var(--on-var);text-align:center}
.hint code{background:color-mix(in srgb,var(--outline) 25%,transparent);padding:1px 5px;border-radius:5px}
</style>
</head>
<body>
<main class="card">
  <div class="logo">
    <div class="mark">edt_panel</div>
    <h1>edt_panel 控制台</h1>
    <p>VLess 订阅管理服务</p>
  </div>
  ` + errBox + `
  <form method="post" action="/login">
    <input type="hidden" name="next" value="` + htmlEscape(next) + `">
    <label for="pw">管理口令</label>
    <input id="pw" name="password" type="password" required
           autocomplete="current-password" autofocus placeholder="请输入口令">
    <button type="submit">登 录</button>
  </form>
  <p class="hint">口令在 <code>config.yml</code> 的 <code>auth.login_password</code><br>留空表示关闭控制台鉴权。</p>
</main>
</body>
</html>`
}

// htmlEscape 最小 HTML 转义（登录页插值用）。
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}
