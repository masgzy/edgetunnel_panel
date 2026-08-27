package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	out := g.Normalized()
	return out, saw
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
