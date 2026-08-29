package service

import (
	"os"
	"path/filepath"
	"strings"
)

// PreIPStore 管理 data/pre_ip.txt（两行：前缀 + IP）。
type PreIPStore struct {
	filePath string
}

// NewPreIPStore 构造。
func NewPreIPStore(filePath string) *PreIPStore {
	return &PreIPStore{filePath: filePath}
}

// Get 返回 (pre, ip)。
func (p *PreIPStore) Get() (string, string) {
	data, err := os.ReadFile(p.filePath)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	pre := ""
	ip := ""
	if len(lines) > 0 {
		pre = lines[0]
	}
	if len(lines) > 1 {
		ip = lines[1]
	}
	return pre, ip
}

// Set 写入 pre 和 ip。
func (p *PreIPStore) Set(pre, ip string) error {
	// 确保父目录存在
	if dir := filepath.Dir(p.filePath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(p.filePath, []byte(pre+"\n"+ip), 0o644)
}

// Reload 热更新 pre_ip.txt 路径（配置重载时调用；低频，读写风格与构造约定一致）。
func (p *PreIPStore) Reload(filePath string) {
	p.filePath = filePath
}
