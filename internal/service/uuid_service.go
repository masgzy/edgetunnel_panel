package service

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"edt/internal/config"
	"edt/internal/module"
)

// UUIDService 负责获取/缓存 UUID 与面板生成配置。
//
// 对接 EDT-Pages / BPB 型管理面板（remote.admin_url 指向 admin/config.json）：
//   - UUID：订阅凭据（dynamic 模式）；
//   - CF.Usage：Cloudflare 真实用量，供 Subscription-Userinfo 头；
//   - 协议/传输/证书/0RTT/分片/随机路径/ECH/指纹/ALPN 等生成设置快照，
//     供「自动获取配置」开关（gen.auto_from_panel）使用。
//
// 缓存策略（对齐 edgetunnel _worker.js 的 UUID 派生语义）：
// _worker.js 中 UUID 由 管理员密码+KEY 经 MD5 派生——凭据不变则 UUID 恒定，
// 因此面板侧无需按天强制重拉，而是：
//  1. 拉取成功即持久化到 data/uuid_cache.json（重启零等待直接可用）；
//  2. 内存/磁盘缓存超过 uuid_cache_ttl（缺省 24h）后触发后台静默刷新，
//     刷新期间请求继续使用旧值（双缓冲），失败保留旧值并按 5 分钟退避重试；
//  3. 启动时 Prewarm 预热：有磁盘缓存立即可用，无缓存则后台预取，
//     避免首个订阅请求阻塞在远程面板上。
type UUIDService struct {
	adminURL    string
	runTimeFile string
	cachePath   string // 磁盘持久化缓存文件（runTimeFile 同目录 uuid_cache.json）
	domain      string
	timeout     time.Duration
	ttl         time.Duration // 缓存有效期；超龄后台刷新（凭据派生语义下旧值通常仍有效）
	authConfig  module.AuthConfig

	mu            sync.Mutex
	cachedUUID    string
	fetchedAt     time.Time // 最近一次成功拉取（含磁盘缓存恢复）
	refreshing    bool      // 后台刷新进行中（防重复调度）
	lastAttemptAt time.Time // 最近一次刷新尝试（失败退避）

	fetchMu sync.Mutex // 串行化网络拉取（首次并发请求只发一次）

	usagePages   int64
	usageWorkers int64
	usageMax     int64
	usageOK      bool

	genSettings config.GenSettings // 面板生成配置快照（已 Normalized）
	genHost     string             // 面板 HOST（Worker 部署域名，订阅令牌计算依据）
	genHosts    []string           // 面板 HOST/HOSTS 域名池
	genOK       bool               // 面板是否提供了可识别的生成设置字段
}

// uuidCacheTTLDefault 缓存有效期缺省值（对齐 _worker.js 按天派生的刷新节奏）。
const uuidCacheTTLDefault = 24 * time.Hour

// uuidRefreshBackoff 刷新失败后的重试退避窗口。
const uuidRefreshBackoff = 5 * time.Minute

// uuidDiskCache 磁盘持久化缓存结构（data/uuid_cache.json）。
type uuidDiskCache struct {
	UUID      string              `json:"uuid"`
	FetchedAt time.Time           `json:"fetched_at"`
	Usage     *uuidDiskCacheUsage `json:"usage,omitempty"`
	Gen       *config.GenSettings `json:"gen,omitempty"`
	Host      string              `json:"host,omitempty"`
	Hosts     []string            `json:"hosts,omitempty"`
}

// uuidDiskCacheUsage 用量快照。
type uuidDiskCacheUsage struct {
	Pages   int64 `json:"pages"`
	Workers int64 `json:"workers"`
	Max     int64 `json:"max"`
	OK      bool  `json:"ok"`
}

// panelConfig admin/config.json 中本项目消费的字段子集。
// 面板实际返回远多于这些的字段（LINK/TG/反代…），按需忽略；
// 生成相关键名与面板保持同构，缺失时逐项回落到缺省值。
type panelConfig struct {
	UUID  string   `json:"UUID"`
	HOST  string   `json:"HOST"`
	HOSTS []string `json:"HOSTS"`

	ProtocolType  string `json:"协议类型"`
	TransportType string `json:"传输协议"`
	GRPCModeKey   string `json:"gRPC模式"`
	GRPCUserAgent string `json:"gRPCUserAgent"`
	SkipCert      bool   `json:"跳过证书验证"`
	Enable0RTTP   bool   `json:"启用0RTT"`
	TLSShard      string `json:"TLS分片"`
	RandomPathP   bool   `json:"随机路径"`
	ECHOn         bool   `json:"ECH"`
	ECHConfig     struct {
		DNS string  `json:"DNS"`
		SNI *string `json:"SNI"`
	} `json:"ECHConfig"`
	Fingerprint string `json:"Fingerprint"`
	ALPN        string `json:"ALPN"`

	SS struct {
		Cipher string `json:"加密方式"`
		TLS    *bool  `json:"TLS"`
	} `json:"SS"`

	PATH string `json:"PATH"`

	// 反代节：生态代码中以特征码字典拆写 PROXYIP 键，实际 JSON 键为 "PROXYIP"。
	ProxySec struct {
		ProxyIP string `json:"PROXYIP"`
		TplSec  struct {
			ProxyIP string `json:"PROXYIP"`
		} `json:"路径模板"`
	} `json:"反代"`

	CF struct {
		Usage struct {
			Success bool  `json:"success"`
			Pages   int64 `json:"pages"`
			Workers int64 `json:"workers"`
			Total   int64 `json:"total"`
			Max     int64 `json:"max"`
		} `json:"Usage"`
	} `json:"CF"`
}

// CFUsage 单次用量快照（来自面板 config.json 的 CF.Usage 字段）。
type CFUsage struct {
	Pages   int64
	Workers int64
	Max     int64
}

// UUIDInfo UUID 缓存元信息（供面板展示缓存来源与新鲜度）。
type UUIDInfo struct {
	UUID      string    `json:"uuid"`
	FetchedAt time.Time `json:"fetched_at"`
	Age       string    `json:"age"`
	Stale     bool      `json:"stale"` // 已超过 TTL，等待后台刷新
}

// UsageSnapshot 返回最近一次面板拉取得到的 CF 用量；ok=false 表示不可用
// （尚未拉取过 / 面板未提供该字段 / success=false）。
func (u *UUIDService) UsageSnapshot() (CFUsage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.usageOK {
		return CFUsage{}, false
	}
	return CFUsage{Pages: u.usagePages, Workers: u.usageWorkers, Max: u.usageMax}, true
}

// GenPanelSnapshot 返回最近一次面板拉取得到的生成配置与域名池；
// ok=false 表示不可用（尚未拉取过 / 面板未提供协议或传输字段）。
func (u *UUIDService) GenPanelSnapshot() (config.GenSettings, []string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.genOK {
		return config.GenSettings{}, nil, false
	}
	hosts := append([]string(nil), u.genHosts...)
	return u.genSettings, hosts, true
}

// Info 返回当前 UUID 缓存元信息（无缓存时 ok=false）。
func (u *UUIDService) Info() (UUIDInfo, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cachedUUID == "" {
		return UUIDInfo{}, false
	}
	return UUIDInfo{
		UUID:      u.cachedUUID,
		FetchedAt: u.fetchedAt,
		Age:       time.Since(u.fetchedAt).Round(time.Second).String(),
		Stale:     u.ttl > 0 && time.Since(u.fetchedAt) > u.ttl,
	}, true
}

// NewUUIDService 构造。
func NewUUIDService(adminURL, runTimeFile, domain string, timeoutSec int) *UUIDService {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	u := &UUIDService{
		adminURL:    adminURL,
		runTimeFile: runTimeFile,
		cachePath:   filepath.Join(filepath.Dir(runTimeFile), "uuid_cache.json"),
		domain:      domain,
		timeout:     time.Duration(timeoutSec) * time.Second,
		ttl:         uuidCacheTTLDefault,
	}
	u.loadDiskCache() // 重启零等待：上次拉取的 UUID/快照立即可用
	return u
}

// SetCacheTTL 注入缓存有效期（config remote.uuid_cache_ttl，秒）。
// <=0 时回落缺省 24h。
func (u *UUIDService) SetCacheTTL(seconds int) {
	if seconds <= 0 {
		seconds = int(uuidCacheTTLDefault.Seconds())
	}
	u.mu.Lock()
	u.ttl = time.Duration(seconds) * time.Second
	u.mu.Unlock()
}

// Prewarm 启动预热：后台触发一次 Get。
// 有磁盘缓存时立即返回缓存并按 TTL 决定是否后台刷新；
// 无缓存时预取，避免首个订阅请求阻塞在远程面板上。
func (u *UUIDService) Prewarm() {
	go func() {
		if _, err := u.Get(); err != nil {
			log.Printf("[uuid] 预热拉取失败（订阅请求时会重试）: %v", err)
		}
	}()
}

// Reload 热更新远程面板对接参数（配置重载时调用）；
// 清除内存快照与磁盘缓存（远端变了，旧缓存不再可信），
// 下一次请求按新配置重新拉取（磁盘 run_time 轮换记录保留）。
func (u *UUIDService) Reload(adminURL, runTimeFile, domain string, timeoutSec int) {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.adminURL = adminURL
	u.runTimeFile = runTimeFile
	u.cachePath = filepath.Join(filepath.Dir(runTimeFile), "uuid_cache.json")
	u.domain = domain
	u.timeout = time.Duration(timeoutSec) * time.Second
	u.cachedUUID = ""
	u.fetchedAt = time.Time{}
	u.genOK = false
	u.usageOK = false
	u.refreshing = false
	u.lastAttemptAt = time.Time{}
	_ = os.Remove(u.cachePath)
}

// SetAuthConfig 注入 auth 模块依赖的配置。
func (u *UUIDService) SetAuthConfig(cfg module.AuthConfig) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.authConfig = cfg
}

// Clear 清除内存与磁盘缓存。
func (u *UUIDService) Clear() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cachedUUID = ""
	u.fetchedAt = time.Time{}
	u.genOK = false
	u.usageOK = false
	_ = os.Remove(u.cachePath)
	return os.WriteFile(u.runTimeFile, []byte("0"), 0o644)
}

// Get 返回 UUID（带缓存）。
// 命中缓存立即返回（不阻塞网络）；超过 TTL 时触发后台静默刷新；
// 无缓存时同步拉取（仅启动后首个请求会等网络）。
func (u *UUIDService) Get() (string, error) {
	u.mu.Lock()
	if u.cachedUUID != "" {
		stale := u.ttl > 0 && time.Since(u.fetchedAt) > u.ttl
		if stale {
			u.tryScheduleRefreshLocked()
		}
		count := u.readRunCount()
		u.writeRunCount(maxi(count, 0) + 1)
		uuid := u.cachedUUID
		u.mu.Unlock()
		return uuid, nil
	}
	u.mu.Unlock()

	// 无缓存：同步拉取（fetchMu 保证并发首请求只发一次网络）
	return u.fetchAndStore()
}

// RefreshNow 强制同步重拉一次面板 config.json 并落盘（设置页「刷新」按钮）。
func (u *UUIDService) RefreshNow() error {
	_, err := u.fetchAndStore()
	return err
}

// tryScheduleRefreshLocked 调度一次后台刷新（调用方需持有 mu）。
// 已在刷新中 / 退避窗口内时不重复调度。
func (u *UUIDService) tryScheduleRefreshLocked() {
	if u.refreshing {
		return
	}
	if !u.lastAttemptAt.IsZero() && time.Since(u.lastAttemptAt) < uuidRefreshBackoff {
		return
	}
	u.refreshing = true
	u.lastAttemptAt = time.Now()
	go func() {
		if _, err := u.fetchAndStore(); err != nil {
			log.Printf("[uuid] 后台刷新失败（保留旧值，%s 后可重试）: %v", uuidRefreshBackoff, err)
		}
		u.mu.Lock()
		u.refreshing = false
		u.mu.Unlock()
	}()
}

// fetchAndStore 拉取面板 config.json，更新内存快照并落盘。
// 同一时刻仅一个网络拉取在途；拿到结果后其余等待者直接复用缓存。
func (u *UUIDService) fetchAndStore() (string, error) {
	u.fetchMu.Lock()
	defer u.fetchMu.Unlock()

	// 双检：等待期间别的请求可能已完成拉取
	u.mu.Lock()
	if u.cachedUUID != "" && time.Since(u.fetchedAt) < u.ttl {
		uuid := u.cachedUUID
		u.mu.Unlock()
		return uuid, nil
	}
	// 锁内快照远端请求参数，避免锁外读字段与 Reload/SetAuthConfig 竞态
	authCfg := u.authConfig
	adminURL := u.adminURL
	domain := u.domain
	timeout := u.timeout
	u.mu.Unlock()

	u.mu.Lock()
	count := u.readRunCount()
	u.writeRunCount(maxi(count, 0) + 1)
	u.mu.Unlock()

	authToken, err := module.GetAuth(authCfg)
	if err != nil {
		return "", fmt.Errorf("获取 auth 失败: %w", err)
	}

	req, err := http.NewRequest("GET", adminURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Referer", fmt.Sprintf("https://%s/admin", domain))
	req.AddCookie(&http.Cookie{Name: "auth", Value: authToken})

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("远程请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var cfg panelConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", fmt.Errorf("解析 UUID 响应失败: %w", err)
	}
	if cfg.UUID == "" {
		return "", fmt.Errorf("远程未返回 UUID")
	}

	// 更新内存快照 + 落盘
	u.mu.Lock()
	u.cachedUUID = cfg.UUID
	u.fetchedAt = time.Now()
	u.usagePages = cfg.CF.Usage.Pages
	u.usageWorkers = cfg.CF.Usage.Workers
	u.usageMax = cfg.CF.Usage.Max
	u.usageOK = cfg.CF.Usage.Success && cfg.CF.Usage.Max > 0
	if gs, ok := panelGenSettings(cfg); ok {
		u.genSettings = gs
		u.genOK = true
	}
	if cfg.HOST != "" {
		u.genHost = cfg.HOST
	}
	if len(cfg.HOSTS) > 0 {
		u.genHosts = append([]string(nil), cfg.HOSTS...)
	} else if cfg.HOST != "" {
		u.genHosts = []string{cfg.HOST}
	}
	cache := u.buildDiskCacheLocked()
	u.mu.Unlock()

	u.saveDiskCache(cache)
	return cfg.UUID, nil
}

// buildDiskCacheLocked 组装磁盘缓存快照（调用方需持有 mu）。
func (u *UUIDService) buildDiskCacheLocked() *uuidDiskCache {
	c := &uuidDiskCache{
		UUID:      u.cachedUUID,
		FetchedAt: u.fetchedAt,
		Host:      u.genHost,
		Hosts:     append([]string(nil), u.genHosts...),
	}
	if u.usageOK {
		c.Usage = &uuidDiskCacheUsage{
			Pages:   u.usagePages,
			Workers: u.usageWorkers,
			Max:     u.usageMax,
			OK:      true,
		}
	}
	if u.genOK {
		gs := u.genSettings
		c.Gen = &gs
	}
	return c
}

// saveDiskCache 原子落盘缓存（tmp + rename；失败仅记日志，不影响主流程）。
func (u *UUIDService) saveDiskCache(c *uuidDiskCache) {
	if c == nil || u.cachePath == "" {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := u.cachePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[uuid] 缓存落盘失败: %v", err)
		return
	}
	if err := os.Rename(tmp, u.cachePath); err != nil {
		_ = os.Remove(tmp)
		log.Printf("[uuid] 缓存替换失败: %v", err)
	}
}

// loadDiskCache 启动时从磁盘恢复缓存（文件缺失/损坏时静默跳过）。
func (u *UUIDService) loadDiskCache() {
	if u.cachePath == "" {
		return
	}
	data, err := os.ReadFile(u.cachePath)
	if err != nil {
		return
	}
	var c uuidDiskCache
	if err := json.Unmarshal(data, &c); err != nil || c.UUID == "" {
		return
	}
	// 缓存明显超龄时仍恢复：请求立即用旧值（后台按退避刷新），
	// 避免重启后首个订阅请求阻塞在远程面板。
	u.mu.Lock()
	u.cachedUUID = c.UUID
	u.fetchedAt = c.FetchedAt
	if c.Usage != nil && c.Usage.OK {
		u.usagePages = c.Usage.Pages
		u.usageWorkers = c.Usage.Workers
		u.usageMax = c.Usage.Max
		u.usageOK = true
	}
	if c.Gen != nil {
		u.genSettings = c.Gen.Normalized()
		u.genOK = true
	}
	if c.Host != "" {
		u.genHost = c.Host
	}
	if len(c.Hosts) > 0 {
		u.genHosts = append([]string(nil), c.Hosts...)
	} else if c.Host != "" {
		u.genHosts = []string{c.Host}
	}
	u.mu.Unlock()
	log.Printf("[uuid] 已从磁盘缓存恢复 UUID（拉取于 %s）", c.FetchedAt.Format("2006-01-02 15:04:05"))
}

func (u *UUIDService) readRunCount() int {
	data, err := os.ReadFile(u.runTimeFile)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func (u *UUIDService) writeRunCount(n int) {
	_ = os.WriteFile(u.runTimeFile, []byte(strconv.Itoa(n)), 0o644)
}

// panelGenSettings 把面板字段映射为归一化后的生成配置。
// 布尔开关以面板值为准；枚举类非法值由 Normalized 收敛；
// 未见到任何协议/传输字段时视为面板不可用（ok=false）。
func panelGenSettings(cfg panelConfig) (config.GenSettings, bool) {
	g := config.DefaultGenSettings()
	saw := false
	if cfg.ProtocolType != "" {
		g.Protocol = cfg.ProtocolType
		saw = true
	}
	if cfg.TransportType != "" {
		g.Transport = cfg.TransportType
		saw = true
	}
	if cfg.GRPCModeKey != "" {
		g.GRPCMode = cfg.GRPCModeKey
	}
	g.GRPCUserAgent = cfg.GRPCUserAgent
	g.SkipCertVerify = cfg.SkipCert
	g.Enable0RTT = cfg.Enable0RTTP || (!saw && g.Enable0RTT)
	switch strings.ToLower(strings.TrimSpace(cfg.TLSShard)) {
	case "shadowrocket":
		g.Fragment = "shadowrocket"
	case "happ":
		g.Fragment = "happ"
	}
	g.RandomPath = cfg.RandomPathP
	g.ECH = cfg.ECHOn
	g.ECHDNS = cfg.ECHConfig.DNS
	if cfg.ECHConfig.SNI != nil {
		g.ECHSNI = *cfg.ECHConfig.SNI
	}
	if cfg.Fingerprint != "" {
		g.Fingerprint = cfg.Fingerprint
	}
	g.ALPN = cfg.ALPN
	if cfg.SS.Cipher != "" {
		g.SSCipher = cfg.SS.Cipher
	}
	if cfg.SS.TLS != nil {
		g.SSTLS = *cfg.SS.TLS
	}
	// 反代路径设置：面板提供 PATH 前缀或反代节时注入快照；
	// 路径模板缺省时补齐生态默认（"proxyip={{IP:PORT}}"）。
	if cfg.PATH != "" || cfg.ProxySec.ProxyIP != "" || cfg.ProxySec.TplSec.ProxyIP != "" {
		tpl := strings.TrimSpace(cfg.ProxySec.TplSec.ProxyIP)
		if tpl == "" {
			tpl = "proxyip=" + config.ProxyIPPlaceholder
		}
		g.ProxyPath = &config.GenProxyPath{
			ProxyIP:    strings.TrimSpace(cfg.ProxySec.ProxyIP),
			PathPrefix: strings.TrimSpace(cfg.PATH),
			PathTpl:    tpl,
		}
	}
	out := g.Normalized()
	return out, saw
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// md5Hex 十六进制小写 MD5。
func md5Hex(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}

// workerSubToken 计算面板订阅令牌：
// token = md5( md5(HOST+UUID) 十六进制的第 7~27 位 )，对齐生态 /sub 鉴权。
func workerSubToken(host, uuid string) string {
	return md5Hex(md5Hex(host + uuid)[7:27])
}

// FetchWorkerSub 拉取面板（Worker）原生订阅，返回已解码的节点链接行。
// 仅 gen.aggregate_worker_sub 开启时由订阅构建方调用；面板不可用时报错。
func (u *UUIDService) FetchWorkerSub() ([]string, error) {
	u.mu.Lock()
	host := u.genHost
	if host == "" && len(u.genHosts) > 0 {
		host = u.genHosts[0]
	}
	uuid := u.cachedUUID
	adminURL := u.adminURL
	timeout := u.timeout
	u.mu.Unlock()
	if host == "" || uuid == "" {
		return nil, fmt.Errorf("面板 host/uuid 不可用")
	}
	// 订阅端点与面板同 Worker 部署：从 adminURL 派生 /sub 地址（兼容 http 测试部署）。
	base := strings.TrimSuffix(adminURL, "/admin/config.json")
	req, err := http.NewRequest("GET",
		fmt.Sprintf("%s/sub?token=%s&target=mixed", base, url.QueryEscape(workerSubToken(host, uuid))), nil)
	if err != nil {
		return nil, err
	}
	// UA 不得含 subconverter/mozilla 关键词，生态按此返回 base64 节点列表。
	req.Header.Set("User-Agent", "edt_panel")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("面板订阅拉取失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("面板订阅返回 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	text := string(body)
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text)); err == nil && utf8.Valid(raw) {
		text = string(raw)
	}
	seen := map[string]bool{}
	rows := make([]string, 0, 32)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		rows = append(rows, line)
	}
	return rows, nil
}

// filepath_joinDir 目录 + 文件名拼接（避免本文件直接依赖 path/filepath 的命名噪音）。
func filepath_joinDir(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}
