package service

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
//   - UUID：订阅凭据（dynamic 模式），按天缓存；
//   - CF.Usage：Cloudflare 真实用量，供 Subscription-Userinfo 头；
//   - 协议/传输/证书/0RTT/分片/随机路径/ECH/指纹等生成设置快照，
//     供「自动获取配置」开关（gen.auto_from_panel）使用。
type UUIDService struct {
	adminURL    string
	runTimeFile string
	domain      string
	timeout     time.Duration
	authConfig  module.AuthConfig

	mu         sync.Mutex
	cachedUUID string
	cachedDate string // YYYY-MM-DD

	usagePages   int64
	usageWorkers int64
	usageMax     int64
	usageOK      bool

	genSettings config.GenSettings // 面板生成配置快照（已 Normalized）
	genHost     string             // 面板 HOST（Worker 部署域名，订阅令牌计算依据）
	genHosts    []string           // 面板 HOST/HOSTS 域名池
	genOK       bool               // 面板是否提供了可识别的生成设置字段
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

// NewUUIDService 构造。
func NewUUIDService(adminURL, runTimeFile, domain string, timeoutSec int) *UUIDService {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	return &UUIDService{
		adminURL:    adminURL,
		runTimeFile: runTimeFile,
		domain:      domain,
		timeout:     time.Duration(timeoutSec) * time.Second,
		cachedDate:  "1970-01-01",
	}
}

// Reload 热更新远程面板对接参数（配置重载时调用）；
// 同时清除内存快照，下一次请求按新配置重新拉取（磁盘 run_time 轮换记录保留）。
func (u *UUIDService) Reload(adminURL, runTimeFile, domain string, timeoutSec int) {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.adminURL = adminURL
	u.runTimeFile = runTimeFile
	u.domain = domain
	u.timeout = time.Duration(timeoutSec) * time.Second
	u.cachedUUID = ""
	u.cachedDate = "1970-01-01"
	u.genOK = false
	u.usageOK = false
}

// SetAuthConfig 注入 auth 模块依赖的配置。
func (u *UUIDService) SetAuthConfig(cfg module.AuthConfig) {
	u.authConfig = cfg
}

// Clear 清除内存与磁盘缓存。
func (u *UUIDService) Clear() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cachedUUID = ""
	u.cachedDate = "1970-01-01"
	u.genOK = false
	return os.WriteFile(u.runTimeFile, []byte("0"), 0o644)
}

// Get 返回 UUID（带缓存），同一次拉取顺带刷新用量与生成配置快照。
func (u *UUIDService) Get() (string, error) {
	u.mu.Lock()
	if u.cachedUUID != "" && u.isSameOrNextDay() {
		count := u.readRunCount()
		u.writeRunCount(maxi(count, 0) + 1)
		uuid := u.cachedUUID
		u.mu.Unlock()
		return uuid, nil
	}
	u.mu.Unlock()

	// 需要远程拉取（释放锁后进行网络请求）
	count := u.readRunCount()
	u.writeRunCount(maxi(count, 0) + 1)

	authToken, err := module.GetAuth(u.authConfig)
	if err != nil {
		return "", fmt.Errorf("获取 auth 失败: %w", err)
	}

	req, err := http.NewRequest("GET", u.adminURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Referer", fmt.Sprintf("https://%s/admin", u.domain))
	req.AddCookie(&http.Cookie{Name: "auth", Value: authToken})

	client := &http.Client{Timeout: u.timeout}
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
	u.mu.Lock()
	u.cachedUUID = cfg.UUID
	u.cachedDate = time.Now().Format("2006-01-02")
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
	u.mu.Unlock()
	return cfg.UUID, nil
}

// isSameOrNextDay 判断缓存是否仍然有效（同一天或跨天 48h 内）。
func (u *UUIDService) isSameOrNextDay() bool {
	target, err := time.Parse("2006-01-02", u.cachedDate)
	if err != nil {
		return false
	}
	now := time.Now()
	delta := now.Sub(target)
	return delta <= 48*time.Hour && delta >= -24*time.Hour
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
	client := &http.Client{Timeout: u.timeout}
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
