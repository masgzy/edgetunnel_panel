// Package server 提供 HTTP 路由与静态资源 embed。
package server

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"edt/internal/config"
	"edt/internal/module"
	"edt/internal/service"
)

//go:embed all:web
var webFS embed.FS

// Deps 服务器依赖。
type Deps struct {
	Cfg             *config.RuntimeConfig
	ConfigStore     *service.ConfigStore
	PreIPStore      *service.PreIPStore
	ResultStore     *service.ResultStore
	UUIDService     *service.UUIDService
	SubscriptionSvc *service.SubscriptionService
	Stats           *service.StatsService
	WebPassword     string // Web 控制台登录口令；空 = 关闭鉴权（旧行为）
	SCRestart       func() // 安装/更新 subconverter 后触发本地子进程重启（可 nil）

	convertSem chan struct{} // 转换并发闸门，New 时初始化
}

// maxConvertConc 单机 subconverter 后端普遍为轻量 C++ 服务，
// 并发洪峰易触发其崩溃（实测 60 并发可 segfault），因此限制同时转换数。
const maxConvertConc = 8

// New 构造 http.Handler：页面/静态路由 + API 路由 + 鉴权与安全头中间件。
func New(deps Deps) http.Handler {
	if deps.convertSem == nil {
		deps.convertSem = make(chan struct{}, maxConvertConc)
	}
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(webFS, "web")

	// 登录鉴权（口令为空时不启用，行为与旧版一致）
	auth := newWebAuth(deps.WebPassword)
	mux.HandleFunc("/login", auth.handleLogin)
	mux.HandleFunc("/logout", auth.handleLogout)

	// 实时日志 WebSocket：同源路由（原独立 3003 端口已移除）
	mux.HandleFunc("/ws", wsHandler)

	registerPageRoutes(mux, staticFS, &deps)
	registerAPIRoutes(mux, &deps)

	// 中间件链：安全头/recover → 请求日志 → 登录鉴权
	return withSecurityHeaders(withRequestLog(auth.middleware(mux)))
}

// registerPageRoutes 注册页面路由、静态资源服务与旧路径重定向。
func registerPageRoutes(mux *http.ServeMux, staticFS fs.FS, deps *Deps) {
	pageRoute := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			serveStatic(w, r, staticFS, name)
		}
	}
	// 新信息架构：/ 仪表盘 /nodes 节点配置 /selector 优选IP /settings 设置
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveStatic(w, r, staticFS, "index.html")
			return
		}
		// 尝试直接 serve 静态文件（assets/...）——走 serveStatic 以获得 ETag/gzip
		if f, err := staticFS.Open(path); err == nil {
			f.Close()
			serveStatic(w, r, staticFS, path)
			return
		}
		// 没有扩展名的当页面路由，回退 index（SPA 式回退）
		if !strings.Contains(path, ".") {
			serveStatic(w, r, staticFS, "index.html")
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/nodes", pageRoute("nodes.html"))
	mux.HandleFunc("/selector", pageRoute("selector.html"))
	mux.HandleFunc("/settings", pageRoute("settings.html"))

	// 旧路径 301 重定向到新路径（兼容老书签）
	mux.HandleFunc("/change", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/nodes", http.StatusMovedPermanently)
	})
	// 注意：/pre_ip 既是页面又是 API（type=get/set），只对无 query 的页面请求重定向
	mux.HandleFunc("/pre_ip", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "" {
			http.Redirect(w, r, "/selector", http.StatusMovedPermanently)
			return
		}
		deps.preIPHandler(w, r)
	})
}

// registerAPIRoutes 注册全部 JSON/订阅 API 端点。
func registerAPIRoutes(mux *http.ServeMux, deps *Deps) {
	routes := map[string]http.HandlerFunc{
		"/config":           deps.configHandler,
		"/clearuuid":        deps.clearUUIDHandler,
		"/getip":            deps.getIPHandler,
		"/getdomain":        deps.getDomainHandler,
		"/getuuid":          deps.getUUIDHandler,
		"/sub":              deps.subHandler,
		"/mihomo":           deps.mihomoHandler,
		"/convert":          deps.convertHandler,
		"/api/status":       deps.statusHandler,
		"/api/preip":        deps.preIPHandler,
		"/api/import":       deps.importHandler,
		"/api/export":       deps.exportHandler,
		"/api/configfile":   deps.configFileHandler,
		"/api/batch_delete": deps.batchDeleteHandler,
		"/api/backup":       deps.backupHandler,
		"/api/sc-config":    deps.scConfigHandler,
		"/api/sc-install":   deps.scInstallHandler,
		"/api/settings":     deps.settingsHandler,
		"/api/gen":          deps.genHandler,
		"/api/restore":      deps.restoreHandler,
		"/api/logs":         deps.logsHandler,
	}
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}
}

// withSecurityHeaders 统一注入 iframe/CSP 响应头，并 recover 业务 panic 防止进程崩溃。
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "ALLOWALL")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' *")
		defer func() {
			if rec := recover(); rec != nil {
				// 对外只返回通用文案；细节进服务端日志（防信息泄漏）
				LogLine("PANIC %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 捕获响应状态码供请求日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// withRequestLog 把页面/API/订阅请求写入日志文件（含状态码与耗时），
// 为设置页实时日志提供内容。静态资源与 WS 升级不入日志，避免刷屏。
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/assets/") || path == "/favicon.svg" ||
			path == "/manifest.webmanifest" || path == "/sw.js" || path == "/ws" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		LogLine("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), rec.status,
			time.Since(start).Round(time.Millisecond))
	})
}

// configHandler GET/POST /config —— 节点增删改查/排序的多路分发。
// action 取自 ?type= 或表单；除 get 外均要求 POST。
func (d *Deps) configHandler(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("type")
	if action == "" {
		action = r.FormValue("type")
	}
	var h func(*http.Request) response
	switch action {
	case "get":
		h = d.configGet
	case "add":
		h = d.configAdd
	case "mod":
		h = d.configMod
	case "move":
		h = d.configSwap
	case "move_to":
		h = d.configMoveTo
	case "del":
		h = d.configDelete
	default:
		http.NotFound(w, r)
		return
	}
	res := h(r)
	res.apply(w)
}

// response 统一封装 handler 的响应：状态码 + JSON 体或纯状态。
type response struct {
	status int
	body   interface{} // nil 表示只写状态码
}

func (res response) apply(w http.ResponseWriter) {
	if res.body == nil {
		w.WriteHeader(res.status)
		return
	}
	writeJSON(w, res.status, res.body)
}

func okResponse() response { return response{status: http.StatusNoContent} }
func errResponse(status int, err error) response {
	return response{status: status, body: map[string]string{"error": err.Error()}}
}

// requirePOST 非 POST 请求时返回 405 响应。
func requirePOST(r *http.Request) (response, bool) {
	if r.Method != http.MethodPost {
		return response{status: http.StatusMethodNotAllowed, body: map[string]string{"error": "GET not supported"}}, false
	}
	return response{}, true
}

// configGet type=get 返回节点列表（剔除内部字段）。
func (d *Deps) configGet(r *http.Request) response {
	entries, err := d.ConfigStore.Parse()
	if err != nil {
		return errResponse(http.StatusInternalServerError, err)
	}
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]interface{}{
			"name":    e.Name,
			"ip":      e.IP,
			"yx_ip":   e.YxIP,
			"yx_host": e.YxHost,
			"yx_port": e.YxPort,
		})
	}
	return response{status: http.StatusOK, body: out}
}

// configAdd type=add 新增节点。
func (d *Deps) configAdd(r *http.Request) response {
	if res, ok := requirePOST(r); !ok {
		return res
	}
	if err := d.ConfigStore.Add(r.FormValue("ip"), r.FormValue("name"), r.FormValue("yx_ip")); err != nil {
		return errResponse(http.StatusInternalServerError, err)
	}
	return okResponse()
}

// configMod type=mod 按可见行号修改节点（空字段保持原值）。
func (d *Deps) configMod(r *http.Request) response {
	if res, ok := requirePOST(r); !ok {
		return res
	}
	visibleLine, _ := strconv.Atoi(r.FormValue("line"))
	var ip, name, yxIP *string
	if v := r.FormValue("ip"); v != "" {
		ip = &v
	}
	if v := r.FormValue("name"); v != "" {
		name = &v
	}
	if v := r.FormValue("yx_ip"); v != "" {
		yxIP = &v
	}
	if err := d.ConfigStore.UpdateVisible(visibleLine, ip, name, yxIP); err != nil {
		return errResponse(http.StatusBadRequest, err)
	}
	return okResponse()
}

// configSwap type=move 交换两行位置。
func (d *Deps) configSwap(r *http.Request) response {
	if res, ok := requirePOST(r); !ok {
		return res
	}
	line1, _ := strconv.Atoi(r.FormValue("line1"))
	line2, _ := strconv.Atoi(r.FormValue("line2"))
	if err := d.ConfigStore.SwapVisible(line1, line2); err != nil {
		return errResponse(http.StatusBadRequest, err)
	}
	return okResponse()
}

// configMoveTo type=move_to 将一行移动到目标位置。
func (d *Deps) configMoveTo(r *http.Request) response {
	if res, ok := requirePOST(r); !ok {
		return res
	}
	fromLine, _ := strconv.Atoi(r.FormValue("from"))
	toLine, _ := strconv.Atoi(r.FormValue("to"))
	if err := d.ConfigStore.MoveTo(fromLine, toLine); err != nil {
		return errResponse(http.StatusBadRequest, err)
	}
	return okResponse()
}

// configDelete type=del 删除指定可见行。
func (d *Deps) configDelete(r *http.Request) response {
	if res, ok := requirePOST(r); !ok {
		return res
	}
	line, _ := strconv.Atoi(r.FormValue("line"))
	if err := d.ConfigStore.DeleteVisible(line); err != nil {
		return errResponse(http.StatusBadRequest, err)
	}
	return okResponse()
}

func (d *Deps) clearUUIDHandler(w http.ResponseWriter, r *http.Request) {
	if err := d.UUIDService.Clear(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) getIPHandler(w http.ResponseWriter, r *http.Request) {
	d.Stats.IncGetIP()
	data, err := d.ResultStore.GetFirst()
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (d *Deps) getDomainHandler(w http.ResponseWriter, r *http.Request) {
	subID := r.URL.Query().Get("id")
	if subID == "" {
		subID = "1"
	}
	subType := r.URL.Query().Get("type")
	domain, err := d.SubscriptionSvc.GetDomain(subID, subType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, domain)
}

func (d *Deps) getUUIDHandler(w http.ResponseWriter, r *http.Request) {
	d.Stats.IncGetUUID()
	uuid, err := d.UUIDService.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, uuid)
}

// subHandler /sub - vless 订阅（兼容原接口）
func (d *Deps) subHandler(w http.ResponseWriter, r *http.Request) {
	d.Stats.IncSub()
	// clash/mihomo 直接走 mihomo 生成
	if r.URL.Query().Has("clash") || r.URL.Query().Has("mihomo") {
		body, err := d.SubscriptionSvc.LoadClashFile()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Subscription-Userinfo", d.SubscriptionSvc.GetUserinfo())
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		fmt.Fprint(w, body)
		return
	}

	subID := r.URL.Query().Get("id")
	if subID == "" {
		subID = "1"
	}
	subType := r.URL.Query().Get("type")
	body, err := d.SubscriptionSvc.BuildSubscription(subID, subType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Subscription-Userinfo", d.SubscriptionSvc.GetUserinfo())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, body)
}

// mihomoHandler /mihomo - 完整 mihomo 配置
// 可选 config 参数指定 ACL4SSR 规则集 URL，生成器据此切换 proxy-groups/rules 模板
func (d *Deps) mihomoHandler(w http.ResponseWriter, r *http.Request) {
	d.Stats.IncMihomo()
	subID := r.URL.Query().Get("id")
	if subID == "" {
		subID = "1"
	}
	subType := r.URL.Query().Get("type")
	configURL := r.URL.Query().Get("config")
	body, err := d.SubscriptionSvc.BuildMihomoSubscription(subID, subType, configURL)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Subscription-Userinfo", d.SubscriptionSvc.GetUserinfo())
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	fmt.Fprint(w, body)
}

// convertHandler /convert - subconverter 桥接，转 singbox/surge/quanx 等
//
// 参数: target, id, type, config
// 流程: EDT 先生成 vless 订阅源 URL，交给 subconverter 转换
func (d *Deps) convertHandler(w http.ResponseWriter, r *http.Request) {
	d.Stats.IncConvert()
	// 并发闸门：保护轻量后端不被洪峰打崩；满载时快速拒绝而非堆积
	select {
	case d.convertSem <- struct{}{}:
		defer func() { <-d.convertSem }()
	default:
		writeJSONError(w, http.StatusTooManyRequests, "转换并发已满，请稍后重试")
		return
	}
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "clash"
	}
	subID := r.URL.Query().Get("id")
	if subID == "" {
		subID = "1"
	}
	subType := r.URL.Query().Get("type")
	externalConfig := r.URL.Query().Get("config")

	// EDT 自身的 vless 订阅 URL（subconverter 抓取源）
	// 注意：subconverter 需要能访问到 EDT，这里用 Host 头构造
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	sourceURL := fmt.Sprintf("%s://%s/sub?id=%s&type=%s", scheme, host, subID, subType)

	body, err := d.SubscriptionSvc.ConvertViaSubConverter(target, sourceURL, externalConfig)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Subscription-Userinfo", d.SubscriptionSvc.GetUserinfo())
	// 根据目标设置 content-type
	ct := "text/plain; charset=utf-8"
	switch target {
	case "clash", "clashr":
		ct = "text/yaml; charset=utf-8"
	case "surge":
		ct = "text/plain; charset=utf-8"
	case "singbox":
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	fmt.Fprint(w, body)
}

// statusHandler /api/status - 供前端查询服务状态（含 subconverter + runtime + stats 信息）
func (d *Deps) statusHandler(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"subconverter": d.SubscriptionSvc.SubConverterStatus(),
		"runtime":      d.SubscriptionSvc.RuntimeStatus(),
		"stats":        d.Stats.Snapshot(),
	}
	writeJSON(w, http.StatusOK, status)
}

// preIPHandler /api/preip?type=get|set - 优选 IP 读写（原 /pre_ip 的 API 部分）
func (d *Deps) preIPHandler(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("type")
	switch action {
	case "get":
		pre, ip := d.PreIPStore.Get()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s\n%s", pre, ip)
	case "set":
		pre := r.URL.Query().Get("pre")
		ip := r.URL.Query().Get("ip")
		if pre == "" || ip == "" {
			http.Error(w, "missing parameters", http.StatusBadRequest)
			return
		}
		if err := d.PreIPStore.Set(pre, ip); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "success")
	default:
		http.NotFound(w, r)
	}
}

// importHandler POST /api/import - 批量导入 vless:// 链接
// 请求体：text/plain，每行一个 vless:// 链接（支持 base64 整体编码）
// query: mode=skip（默认，跳过重复）| overwrite（覆盖重复）| duplicate（允许重复）
// 响应：JSON {success, failed, skipped, duplicates, total}
func (d *Deps) importHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST method only")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportSize))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "skip"
	}
	nodes, failed := service.ParseVlessBatch(string(body))
	success := 0
	skipped := 0
	duplicates := 0
	for _, node := range nodes {
		entry := service.VlessNodeToConfigEntry(node)
		res, dup := d.importVlessEntry(entry, mode)
		switch res {
		case importOK:
			success++
		case importFailed:
			failed++
		case importSkipped:
			skipped++
		}
		if dup {
			duplicates++
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    success,
		"failed":     failed,
		"skipped":    skipped,
		"duplicates": duplicates,
		"total":      success + failed + skipped,
	})
}

// 导入单条结果。
type importResult int

const (
	importOK      importResult = iota // 新增或覆盖成功
	importFailed                      // 读写失败
	importSkipped                     // 重复且按 skip 策略跳过
)

// importVlessEntry 按重复策略导入单个节点：
// skip 跳过重复；overwrite 覆盖原行；duplicate 直接追加（允许重复）。
// 第二个返回值标记该条目是否与现有行重复（无论后续如何处理）。
func (d *Deps) importVlessEntry(entry service.ConfigEntry, mode string) (importResult, bool) {
	dupLine, err := d.ConfigStore.FindDuplicate(entry.IP, entry.Name)
	if err != nil {
		return importFailed, false
	}
	if dupLine > 0 {
		switch mode {
		case "skip":
			return importSkipped, true
		case "overwrite":
			// 覆盖：用 UpdateVisible 更新该行
			ip, name, yx := entry.IP, entry.Name, entry.YxIP
			if err := d.ConfigStore.UpdateVisible(dupLine, &ip, &name, &yx); err != nil {
				return importFailed, true
			}
			return importOK, true
		}
		// mode=duplicate：落到下方直接添加（允许重复）
	}
	if err := d.ConfigStore.Add(entry.IP, entry.Name, entry.YxIP); err != nil {
		return importFailed, dupLine > 0
	}
	return importOK, dupLine > 0
}

// exportHandler GET /api/export - 导出 vless.txt 原文
// 直接读取文件返回，保持原始格式（含 DIRECT/变量等）
func (d *Deps) exportHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(d.ConfigStore.FilePath())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=vless.txt")
	w.Write(data)
}

// configFileHandler GET/POST /api/configfile - 读取或保存 config.yml
// GET：返回 config.yml 内容
// POST：保存 config.yml（请求体为纯文本），需重启生效
func (d *Deps) configFileHandler(w http.ResponseWriter, r *http.Request) {
	configPath := filepath.Join(d.Cfg.RootDir, "config.yml")
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 先备份
		if old, err := os.ReadFile(configPath); err == nil {
			os.WriteFile(configPath+".bak", old, 0o644)
		}
		if err := os.WriteFile(configPath, body, 0o644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message": "saved, restart to take effect",
		})
		return
	}
	// GET
	data, err := os.ReadFile(configPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

// scConfigHandler GET/POST /api/sc-config - 读取或保存 subconverter 配置
// GET：返回当前 subconverter 配置 + 远程地址列表
// POST：保存 subconverter 配置到 config.yml（修改 subconverter: 节）
func (d *Deps) scConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		d.scConfigSave(w, r)
		return
	}
	// GET：返回当前配置 + 远程地址列表
	sc := d.SubscriptionSvc.SubConverterStatus()
	// 检查本地 subconverter 是否已安装
	binPath := filepath.Join(d.Cfg.RootDir, "bin", "subconverter", "subconverter")
	_, binErr := os.Stat(binPath)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"current":   sc,
		"installed": binErr == nil,
		"bin_path":  binPath,
		"remotes":   scRemoteList,
	})
}

// scConfigSave 处理 POST：更新 config.yml 的 subconverter 节（写前自动备份）。
func (d *Deps) scConfigSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode   string `json:"mode"`
		Remote string `json:"remote"`
		Bin    string `json:"bin"`
		Port   int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	configPath, raw, cfgMap, err := d.readConfigYAMLMap()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scSection := map[string]interface{}{
		"mode": req.Mode,
		"bin":  req.Bin,
		"port": req.Port,
	}
	if req.Remote != "" {
		scSection["remote"] = req.Remote
	}
	if req.Bin == "" {
		scSection["bin"] = "bin/subconverter/subconverter"
	}
	if req.Port <= 0 {
		scSection["port"] = 25500
	}
	cfgMap["subconverter"] = scSection
	if err := writeConfigYAMLMap(configPath, raw, cfgMap); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "subconverter config saved, restart to take effect",
		"config":  scSection,
	})
}

// settingsHandler GET/POST /api/settings - 读取或保存通用设置（订阅端口、默认模板、历史数量、日志行数）
func (d *Deps) settingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		d.settingsSave(w, r)
		return
	}
	// GET：返回当前设置
	rt := d.SubscriptionSvc.RuntimeStatus()
	// 前端偏好（localStorage 不由后端管理，但返回默认值）
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscription_port":     rt["subscription_port"],
		"default_profile":       rt["default_profile"],
		"profiles":              rt["profiles"],
		"encode_base64":         rt["encode_base64"],
		"history_limit_default": 10,
		"log_lines_default":     200,
	})
}

// settingsSave 处理 POST：按需修改 remote.subscription_port、
// subscriptions.default_profile / encode_base64 三个配置项。
func (d *Deps) settingsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SubscriptionPort int    `json:"subscription_port"`
		DefaultProfile   string `json:"default_profile"`
		EncodeBase64     *bool  `json:"encode_base64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	configPath, raw, cfgMap, err := d.readConfigYAMLMap()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.SubscriptionPort > 0 {
		if remote, ok := cfgMap["remote"].(map[string]interface{}); ok {
			remote["subscription_port"] = req.SubscriptionPort
		}
	}
	if subs, ok := cfgMap["subscriptions"].(map[string]interface{}); ok {
		if req.DefaultProfile != "" {
			subs["default_profile"] = req.DefaultProfile
		}
		if req.EncodeBase64 != nil {
			subs["encode_base64"] = *req.EncodeBase64
		}
	}
	if err := writeConfigYAMLMap(configPath, raw, cfgMap); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "settings saved, restart to take effect",
	})
}

// genHandler GET/POST /api/gen - 生成配置（自动获取开关 + 手动默认值 + 面板快照）。
//
// GET：返回磁盘配置态（gen 节）、面板快照态与运行时生效态；
// POST：保存 auto_from_panel / manual 到 config.yml（写前备份，重启后生效），
// 带 refresh=true 时顺带强制重拉一次面板 config.json 刷新快照。
func (d *Deps) genHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		d.genSave(w, r)
		return
	}

	// 磁盘态
	_, _, cfgMap, err := d.readConfigYAMLMap()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	auto, manual, _ := readGenSectionFromMap(cfgMap)

	resp := map[string]interface{}{
		"auto":         auto,
		"manual":       manual,
		"effective":    d.SubscriptionSvc.EffectiveGenSettings(),
		"panel_ok":     d.SubscriptionSvc.PanelGenAvailable(),
		"need_restart": true,
	}
	if panelGS, hosts, ok := d.UUIDService.GenPanelSnapshot(); ok {
		resp["panel"] = panelGS
		resp["panel_hosts"] = hosts
	}
	writeJSON(w, http.StatusOK, resp)
}

// genSave 处理 POST：把生成配置写入 config.yml 的 gen 节。
func (d *Deps) genSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Auto    *bool               `json:"auto"`
		Refresh bool                `json:"refresh"`
		Manual  *config.GenSettings `json:"manual"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	refreshed := false
	if req.Refresh && d.UUIDService != nil {
		// 清掉日缓存强制重拉；UUID 与快照同源一并刷新。
		if err := d.UUIDService.Clear(); err == nil {
			if _, gerr := d.UUIDService.Get(); gerr == nil {
				refreshed = true
			}
		}
	}
	if req.Auto == nil && req.Manual == nil {
		panelGS, hosts, ok := d.UUIDService.GenPanelSnapshot()
		resp := map[string]interface{}{"message": "nothing to save", "refreshed": refreshed}
		if ok {
			resp["panel"] = panelGS
			resp["panel_hosts"] = hosts
		}
		resp["panel_ok"] = ok
		writeJSON(w, http.StatusOK, resp)
		return
	}

	configPath, raw, cfgMap, err := d.readConfigYAMLMap()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 在现有磁盘值基础上做增量修改（缺省补齐默认行）。
	currentAuto, currentManual, haveGen := readGenSectionFromMap(cfgMap)
	if !haveGen {
		def := config.DefaultGenSettings()
		currentAuto = true
		currentManual = def
	}
	if req.Auto != nil {
		currentAuto = *req.Auto
	}
	if req.Manual != nil {
		currentManual = req.Manual.Normalized()
	}

	section := map[string]interface{}{
		"auto_from_panel": currentAuto,
		"manual": map[string]interface{}{
			"protocol":         currentManual.Protocol,
			"transport":        currentManual.Transport,
			"grpc_mode":        currentManual.GRPCMode,
			"grpc_user_agent":  currentManual.GRPCUserAgent,
			"skip_cert_verify": currentManual.SkipCertVerify,
			"enable_0rtt":      currentManual.Enable0RTT,
			"fragment":         currentManual.Fragment,
			"random_path":      currentManual.RandomPath,
			"ech":              currentManual.ECH,
			"ech_dns":          currentManual.ECHDNS,
			"ech_sni":          currentManual.ECHSNI,
			"fingerprint":      currentManual.Fingerprint,
		},
	}
	cfgMap["gen"] = section

	if err := writeConfigYAMLMap(configPath, raw, cfgMap); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "gen settings saved, restart to take effect",
		"auto":      currentAuto,
		"manual":    currentManual,
		"refreshed": refreshed,
	})
}

// readGenSectionFromMap 从 config.yml 通用映射中提取 gen 节（宽松：缺失/非法回落默认）。
func readGenSectionFromMap(cfgMap map[string]interface{}) (auto bool, manual config.GenSettings, ok bool) {
	manual = config.DefaultGenSettings()
	auto = true
	raw, present := cfgMap["gen"]
	if !present || raw == nil {
		return auto, manual, false
	}
	m, isMap := raw.(map[string]interface{})
	if !isMap {
		return auto, manual, false
	}
	if v, has := m["auto_from_panel"]; has {
		if b, err := yamlToBool(v); err == nil {
			auto = b
		}
	}
	mm, has := m["manual"].(map[string]interface{})
	if !has {
		return auto, manual, true
	}
	gs := config.DefaultGenSettings()
	str := func(key string, dst *string) {
		if v, has := mm[key]; has && v != nil {
			*dst = fmt.Sprintf("%v", v)
		}
	}
	bl := func(key string, dst *bool) {
		if v, has := mm[key]; has {
			if b, err := yamlToBool(v); err == nil {
				*dst = b
			}
		}
	}
	str("protocol", &gs.Protocol)
	str("transport", &gs.Transport)
	str("grpc_mode", &gs.GRPCMode)
	str("grpc_user_agent", &gs.GRPCUserAgent)
	str("ech_dns", &gs.ECHDNS)
	str("ech_sni", &gs.ECHSNI)
	str("fragment", &gs.Fragment)
	str("fingerprint", &gs.Fingerprint)
	bl("skip_cert_verify", &gs.SkipCertVerify)
	bl("enable_0rtt", &gs.Enable0RTT)
	bl("random_path", &gs.RandomPath)
	bl("ech", &gs.ECH)
	manual = gs.Normalized()
	return auto, manual, true
}

// yamlToBool 宽松布尔转换（兼容 true/"1"/bool 等）。
func yamlToBool(v interface{}) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off":
			return false, nil
		}
	case int:
		return t != 0, nil
	}
	return false, fmt.Errorf("invalid bool: %v", v)
}

func (d *Deps) readConfigYAMLMap() (configPath string, raw []byte, cfgMap map[string]interface{}, err error) {
	configPath = filepath.Join(d.Cfg.RootDir, "config.yml")
	raw, err = os.ReadFile(configPath)
	if err != nil {
		return configPath, nil, nil, err
	}
	if err := yaml.Unmarshal(raw, &cfgMap); err != nil {
		return configPath, nil, nil, err
	}
	return configPath, raw, cfgMap, nil
}

// writeConfigYAMLMap 序列化写回 config.yml，写前先落一份 .bak 备份。
func writeConfigYAMLMap(configPath string, raw []byte, cfgMap map[string]interface{}) error {
	out, err := yaml.Marshal(cfgMap)
	if err != nil {
		return err
	}
	os.WriteFile(configPath+".bak", raw, 0o644)
	return os.WriteFile(configPath, out, 0o644)
}

// scRemoteList 常用远程 subconverter 后端地址。
var scRemoteList = []map[string]string{
	{"label": "🔄 CM负载均衡后端", "value": "https://subapi.cmliussss.net"},
	{"label": "🛟 CM应急备用后端", "value": "https://subapi.fxxk.dedyn.io"},
	{"label": "⚡ 肥羊增强型后端", "value": "https://api.v1.mk"},
	{"label": "🛡️ 肥羊备用后端", "value": "https://url.v1.mk"},
	{"label": "🎬 周润发后端", "value": "https://subapi.zrfme.com"},
}

// batchDeleteHandler POST /api/batch_delete - 批量删除节点
// 请求体 JSON: {"lines":[1,2,3]}
// 响应: {"success":N,"failed":N}
func (d *Deps) batchDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST method only")
		return
	}
	var req struct {
		Lines []int `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析请求失败: "+err.Error())
		return
	}
	if len(req.Lines) == 0 {
		writeJSONError(w, http.StatusBadRequest, "lines cannot be empty")
		return
	}
	success, failed := d.ConfigStore.BatchDelete(req.Lines)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": success,
		"failed":  failed,
		"total":   len(req.Lines),
	})
}

// backupHandler GET /api/backup - 打包 data 目录为 zip 下载
func (d *Deps) backupHandler(w http.ResponseWriter, r *http.Request) {
	dataDir := filepath.Join(d.Cfg.RootDir, "data")
	// 检查目录存在
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		writeJSONError(w, http.StatusNotFound, "data directory not found")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=edt-backup-"+time.Now().Format("20060102-150405")+".zip")
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()
	_ = filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(d.Cfg.RootDir, path)
		writer, err := zipWriter.Create(rel)
		if err != nil {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		// 显式作用域关闭，避免 defer 遗留在 Walk 回调循环中
		func() {
			defer file.Close()
			_, _ = io.Copy(writer, file)
		}()
		return nil
	})
}

// restoreHandler POST /api/restore - 上传 zip 恢复 data 目录
// 接收 multipart/form-data 文件字段 "file"
func (d *Deps) restoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST method only")
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析表单失败: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "未找到文件: "+err.Error())
		return
	}
	defer file.Close()
	// 先读到内存（限制大小已由 ParseMultipartForm 处理）
	buf, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "读取文件失败: "+err.Error())
		return
	}
	zipReader, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "zip 解析失败: "+err.Error())
		return
	}
	_ = os.MkdirAll(filepath.Join(d.Cfg.RootDir, "data"), 0o755)
	restored, failed := restoreZipEntries(d.Cfg.RootDir, zipReader)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"restored": restored,
		"failed":   failed,
		"message":  "恢复完成，建议刷新页面",
	})
}

// restoreZipEntries 将备份 zip 中 data/ 前缀的条目解包回根目录。
// 返回 (成功数, 跳过/失败数)；含 Zip Slip 防护：清理后路径必须仍在根目录内。
func restoreZipEntries(rootDir string, zr *zip.Reader) (restored, failed int) {
	rootClean := filepath.Clean(rootDir) // Zip Slip 防护基准
	for _, f := range zr.File {
		if !strings.HasPrefix(filepath.ToSlash(f.Name), "data/") {
			failed++
			continue
		}
		targetClean := filepath.Clean(filepath.Join(rootClean, f.Name))
		if targetClean != rootClean && !strings.HasPrefix(targetClean, rootClean+string(filepath.Separator)) {
			failed++
			continue
		}
		targetPath := filepath.Join(rootDir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0o755)
			continue
		}
		if err := writeZipEntry(targetPath, f); err != nil {
			failed++
			continue
		}
		restored++
	}
	return restored, failed
}

// writeZipEntry 解包单个文件条目到目标路径（自动创建父目录）。
func writeZipEntry(targetPath string, f *zip.File) error {
	if dir := filepath.Dir(targetPath); dir != "" {
		os.MkdirAll(dir, 0o755)
	}
	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		out.Close()
		return err
	}
	_, err = io.Copy(out, rc)
	out.Close()
	rc.Close()
	return err
}

// logsHandler GET /api/logs?lines=N - 读取日志尾部（默认 200 行，上限 2000）
func (d *Deps) logsHandler(w http.ResponseWriter, r *http.Request) {
	lines := clampInt(atoiOr(r.URL.Query().Get("lines"), 200), 10, 2000)
	data, err := os.ReadFile(logFilePath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "log file not found")
		return
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := 0
	if len(all) > lines {
		start = len(all) - lines
	}
	tail := strings.Join(all[start:], "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, tail)
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// maxImportSize 批量导入请求体上限（16MB 足够数千条链接）。
const maxImportSize = 16 << 20

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// 错误码常量
const (
	CodeInternal       = "INTERNAL"
	CodeBadRequest     = "BAD_REQUEST"
	CodeNotFound       = "NOT_FOUND"
	CodeMethodNotAllow = "METHOD_NOT_ALLOWED"
	CodeBadGateway     = "BAD_GATEWAY"
	CodeUnauthorized   = "UNAUTHORIZED"
)

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	code := CodeInternal
	switch status {
	case http.StatusBadRequest:
		code = CodeBadRequest
	case http.StatusNotFound:
		code = CodeNotFound
	case http.StatusMethodNotAllowed:
		code = CodeMethodNotAllow
	case http.StatusBadGateway:
		code = CodeBadGateway
	case http.StatusUnauthorized:
		code = CodeUnauthorized
	}
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// 注入 auth 配置（避免循环依赖，由 main 调用）
var _ = module.AuthConfig{}

// mimeByExt 按扩展名返回 Content-Type。
func mimeByExt(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".txt"):
		return "text/plain; charset=utf-8"
	case strings.HasSuffix(name, ".webmanifest"):
		return "application/manifest+json"
	default:
		return "text/html; charset=utf-8"
	}
}

// compressible 该 Content-Type 是否值得 gzip。
func compressible(ct string) bool {
	for _, p := range []string{"text/", "application/javascript", "application/json", "image/svg+xml", "application/manifest"} {
		if strings.Contains(ct, p) {
			return true
		}
	}
	return false
}

// contentHash 计算资源 ETag（sha1 前 16 位 hex）。
func contentHash(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:8])
}

// etagMatch 宽松匹配 If-None-Match（支持列表与 *）。
func etagMatch(inm, etag string) bool {
	if inm == "*" {
		return true
	}
	for _, part := range strings.Split(inm, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
}

// gzipCache 缓存 embed 资源的 gzip 结果（embed 内容不可变，ETag 即 key）。
var gzipCache sync.Map

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	zw.Write(data)
	zw.Close()
	return buf.Bytes()
}

// serveStatic 从 embed FS 读取文件并返回。
// 带 ETag 协商缓存（304）+ 按 Accept-Encoding gzip 压缩；
// vendor bundle 内容随二进制发布，使用 immutable 强缓存。
func serveStatic(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mimeByExt(name)
	etag := `"` + contentHash(data) + `"`
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", etag)
	if strings.HasPrefix(name, "assets/vendor/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if compressible(ct) && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		var body []byte
		if cached, ok := gzipCache.Load(etag); ok {
			body = cached.([]byte)
		} else {
			body = gzipBytes(data)
			gzipCache.Store(etag, body)
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}
