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

	"edt/internal/module"
)

// UUIDService 负责获取/缓存 UUID。
//
// 对接 EDT-Pages / BPB 型管理面板（remote.admin_url 指向 admin/config.json），
// 同一次请求顺带提取面板上报的 Cloudflare Workers/Pages 用量（CF.Usage），
// 供 Subscription-Userinfo 头展示真实用量而非伪造数字。
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
}

// panelConfig admin/config.json 中本项目消费的字段子集。
// 面板实际返回远多于这些的字段（HOST/HOSTS/LINK/TG/反代…），按需忽略。
type panelConfig struct {
	UUID string `json:"UUID"`
	CF   struct {
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
	return os.WriteFile(u.runTimeFile, []byte("0"), 0o644)
}

// Get 返回 UUID（带缓存）。
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
	u.mu.Unlock()
	return cfg.UUID, nil
}

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

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
