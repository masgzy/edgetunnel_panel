// stats.go - 订阅调用统计（内存计数器 + 文件持久化，重启恢复）。
package service

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// StatsService 统计服务。
type StatsService struct {
	mu           sync.RWMutex
	subCount     int64
	mihomoCount  int64
	convertCount int64
	getIPCount   int64
	getUUIDCount int64
	startTime    time.Time
	lastSub      time.Time
	lastMihomo   time.Time
	filePath     string
	stopCh       chan struct{}
}

// NewStatsService 构造。filePath 为持久化文件路径，空则不持久化。
func NewStatsService(filePath string) *StatsService {
	s := &StatsService{
		startTime: time.Now(),
		filePath:  filePath,
		stopCh:    make(chan struct{}),
	}
	s.load()
	// 每 30 秒落盘一次
	if filePath != "" {
		go s.persistLoop()
	}
	return s
}

// load 从文件恢复统计（仅恢复计数，startTime 重置为当前）。
func (s *StatsService) load() {
	if s.filePath == "" {
		return
	}
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var saved struct {
		Sub     int64 `json:"sub"`
		Mihomo  int64 `json:"mihomo"`
		Convert int64 `json:"convert"`
		GetIP   int64 `json:"getip"`
		GetUUID int64 `json:"getuuid"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return
	}
	s.mu.Lock()
	s.subCount = saved.Sub
	s.mihomoCount = saved.Mihomo
	s.convertCount = saved.Convert
	s.getIPCount = saved.GetIP
	s.getUUIDCount = saved.GetUUID
	s.mu.Unlock()
}

// save 落盘。
func (s *StatsService) save() {
	if s.filePath == "" {
		return
	}
	s.mu.RLock()
	data, _ := json.Marshal(map[string]int64{
		"sub":     s.subCount,
		"mihomo":  s.mihomoCount,
		"convert": s.convertCount,
		"getip":   s.getIPCount,
		"getuuid": s.getUUIDCount,
	})
	s.mu.RUnlock()
	_ = os.WriteFile(s.filePath, data, 0o644)
}

// persistLoop 定期落盘。
func (s *StatsService) persistLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.save()
		case <-s.stopCh:
			s.save()
			return
		}
	}
}

// Stop 停止并最后落盘。
func (s *StatsService) Stop() {
	close(s.stopCh)
}

// IncSub /sub 调用 +1。
func (s *StatsService) IncSub() {
	s.mu.Lock()
	s.subCount++
	s.lastSub = time.Now()
	s.mu.Unlock()
}

// IncMihomo /mihomo 调用 +1。
func (s *StatsService) IncMihomo() {
	s.mu.Lock()
	s.mihomoCount++
	s.lastMihomo = time.Now()
	s.mu.Unlock()
}

// IncConvert /convert 调用 +1。
func (s *StatsService) IncConvert() {
	s.mu.Lock()
	s.convertCount++
	s.mu.Unlock()
}

// IncGetIP /getip 调用 +1。
func (s *StatsService) IncGetIP() {
	s.mu.Lock()
	s.getIPCount++
	s.mu.Unlock()
}

// IncGetUUID /getuuid 调用 +1。
func (s *StatsService) IncGetUUID() {
	s.mu.Lock()
	s.getUUIDCount++
	s.mu.Unlock()
}

// Snapshot 返回统计快照。
func (s *StatsService) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := map[string]interface{}{
		"sub":     s.subCount,
		"mihomo":  s.mihomoCount,
		"convert": s.convertCount,
		"getip":   s.getIPCount,
		"getuuid": s.getUUIDCount,
		"uptime":  int(time.Since(s.startTime).Seconds()),
	}
	if !s.lastSub.IsZero() {
		result["last_sub"] = s.lastSub.Format("15:04:05")
	}
	if !s.lastMihomo.IsZero() {
		result["last_mihomo"] = s.lastMihomo.Format("15:04:05")
	}
	return result
}
