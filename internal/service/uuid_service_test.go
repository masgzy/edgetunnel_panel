// uuid_service_test.go - UUIDService 单元测试：
// 覆盖 Reload/SetAuthConfig 写端与快照读端的并发安全（配合 go test -race）、
// 缓存清理、运行计数文件、订阅令牌公式以及面板生成配置映射。
package service

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"edt/internal/config"
	"edt/internal/module"
)

// TestUUIDServiceConcurrentReloadSetAuthConfig 并发回归：
// Reload / SetAuthConfig / Clear 写端与 UsageSnapshot / GenPanelSnapshot 读端
// 并发执行不得产生数据竞争。该用例需在 go test -race 下运行方有意义。
func TestUUIDServiceConcurrentReloadSetAuthConfig(t *testing.T) {
	dir := t.TempDir()
	runTimeFile := filepath.Join(dir, "run_time.txt")
	u := NewUUIDService("https://panel.example.com/admin/config.json", runTimeFile, "example.com", 5)

	const workers = 6
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(workers)

	go func() { // Reload 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			u.Reload("https://panel"+string(rune('a'+i%26))+".example.com/admin/config.json",
				runTimeFile, "example.com", 5+i%10)
		}
	}()
	go func() { // SetAuthConfig 写端（修复点：此前无锁写）
		defer wg.Done()
		for i := 0; i < iters; i++ {
			u.SetAuthConfig(module.AuthConfig{
				LoginURL:       "https://auth.example.com/login",
				LoginPassword:  "p",
				RequestTimeout: 1,
			})
		}
	}()
	go func() { // Clear 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if err := u.Clear(); err != nil {
				t.Errorf("Clear: %v", err)
				return
			}
		}
	}()
	go func() { // 用量快照读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = u.UsageSnapshot()
		}
	}()
	go func() { // 生成配置快照读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _, _ = u.GenPanelSnapshot()
		}
	}()
	go func() { // 混合快照读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = u.UsageSnapshot()
			_, _, _ = u.GenPanelSnapshot()
		}
	}()
	wg.Wait()
}

// TestUUIDServiceClearAndRunCount 运行计数往返与 Clear 语义。
func TestUUIDServiceClearAndRunCount(t *testing.T) {
	dir := t.TempDir()
	runTimeFile := filepath.Join(dir, "run_time.txt")
	u := NewUUIDService("", runTimeFile, "example.com", 5)

	// 缺失文件计 0
	if got := u.readRunCount(); got != 0 {
		t.Errorf("缺失文件 readRunCount = %d, 期望 0", got)
	}
	// 写入并回读
	u.writeRunCount(42)
	if got := u.readRunCount(); got != 42 {
		t.Errorf("readRunCount = %d, 期望 42", got)
	}
	// 非法内容计 0
	if err := os.WriteFile(runTimeFile, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if got := u.readRunCount(); got != 0 {
		t.Errorf("非法内容 readRunCount = %d, 期望 0", got)
	}
	// Clear 清零并重置快照可用性
	if err := os.WriteFile(runTimeFile, []byte("7"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := u.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	if got := u.readRunCount(); got != 0 {
		t.Errorf("Clear 后 readRunCount = %d, 期望 0", got)
	}
	if data, err := os.ReadFile(runTimeFile); err != nil || string(data) != "0" {
		t.Errorf("Clear 后文件内容 = (%q,%v), 期望 (\"0\",nil)", data, err)
	}
	// 无缓存时 Info 不可用；缓存恢复后 fetchedAt 应为最近拉取时间
	if _, ok := u.Info(); ok {
		t.Error("Clear 后不应有 UUID 缓存信息")
	}
	u.mu.Lock()
	u.cachedUUID = "test-uuid"
	u.fetchedAt = time.Now()
	u.mu.Unlock()
	if info, ok := u.Info(); !ok || info.UUID != "test-uuid" || info.Stale {
		t.Errorf("Info = %+v, ok=%v, 期望新鲜缓存", info, ok)
	}
}

// TestUUIDServiceReloadResetsState Reload 后内存快照必须失效。
func TestUUIDServiceReloadResetsState(t *testing.T) {
	dir := t.TempDir()
	runTimeFile := filepath.Join(dir, "run_time.txt")
	u := NewUUIDService("https://p.example.com/admin/config.json", runTimeFile, "d.example.com", 5)

	// 直接注入内部快照状态，验证 Reload 的失效语义
	u.mu.Lock()
	u.cachedUUID = "cached-uuid"
	u.fetchedAt = time.Now() // 新鲜缓存窗口内
	u.genOK = true
	u.usageOK = true
	u.mu.Unlock()

	if _, ok := u.Info(); !ok {
		t.Fatal("预热：应有 UUID 缓存信息")
	}
	u.Reload("https://p2.example.com/admin/config.json", runTimeFile, "d2.example.com", 3)

	if _, ok := u.UsageSnapshot(); ok {
		t.Error("Reload 后用量快照应失效")
	}
	if _, _, ok := u.GenPanelSnapshot(); ok {
		t.Error("Reload 后生成配置快照应失效")
	}
	if _, ok := u.Info(); ok {
		t.Error("Reload 后 UUID 缓存信息应失效")
	}
	u.mu.Lock()
	cachedUUID := u.cachedUUID
	u.mu.Unlock()
	if cachedUUID != "" {
		t.Errorf("Reload 后 UUID 缓存应清空, 实际 %q", cachedUUID)
	}
}

// TestWorkerSubToken 订阅令牌公式：token = md5( md5(HOST+UUID) 十六进制的第 7~27 位 )。
func TestWorkerSubToken(t *testing.T) {
	// 独立重算公式，锁定实现契约
	want := func(host, uuid string) string {
		inner := md5.Sum([]byte(host + uuid))
		innerHex := hex.EncodeToString(inner[:])
		outer := md5.Sum([]byte(innerHex[7:27]))
		return hex.EncodeToString(outer[:])
	}
	host, uuid := "panel.example.com", "233a3a24-35a5-4a1d-9ebe-3e0bda3b3f3b"
	if got := workerSubToken(host, uuid); got != want(host, uuid) {
		t.Errorf("workerSubToken = %q, 期望 %q", got, want(host, uuid))
	}
	// 同输入同输出；不同输入不同输出
	if workerSubToken(host, uuid) != workerSubToken(host, uuid) {
		t.Error("同输入应产生同令牌")
	}
	if workerSubToken(host, uuid) == workerSubToken("other.example.com", uuid) {
		t.Error("不同 host 应产生不同令牌")
	}
}

// TestPanelGenSettings 面板字段到生成配置的映射：
// 协议/传输存在时 ok=true；完全缺失时 ok=false；反代节注入快照。
func TestPanelGenSettings(t *testing.T) {
	// 完全空白：ok=false
	if _, ok := panelGenSettings(panelConfig{}); ok {
		t.Error("空白面板配置应返回 ok=false")
	}

	// 有协议/传输：ok=true 且值透传
	cfg := panelConfig{
		ProtocolType:  "vless",
		TransportType: "grpc",
		GRPCModeKey:   "multi",
		Fingerprint:   "chrome",
	}
	g, ok := panelGenSettings(cfg)
	if !ok {
		t.Fatal("含协议/传输的面板配置应返回 ok=true")
	}
	if g.Protocol != "vless" || g.Transport != "grpc" || g.GRPCMode != "multi" || g.Fingerprint != "chrome" {
		t.Errorf("面板字段透传不符: %+v", g)
	}

	// TLS 分片枚举收敛
	cfg.TLSShard = "Shadowrocket"
	g, _ = panelGenSettings(cfg)
	if g.Fragment != "shadowrocket" {
		t.Errorf("TLSShard=Shadowrocket 应收敛为 shadowrocket, 实际 %q", g.Fragment)
	}
	cfg.TLSShard = "未知值"
	g, _ = panelGenSettings(cfg)
	if g.Fragment != "" {
		t.Errorf("未知 TLSShard 应收敛为空, 实际 %q", g.Fragment)
	}

	// 反代节：PATH/PROXYIP 提供时注入快照，模板缺省补生态默认
	cfg.PATH = "/sub"
	cfg.ProxySec.ProxyIP = "1.2.3.4"
	g, _ = panelGenSettings(cfg)
	if g.ProxyPath == nil {
		t.Fatal("提供 PATH/PROXYIP 时应注入 ProxyPath 快照")
	}
	if g.ProxyPath.PathPrefix != "/sub" || g.ProxyPath.ProxyIP != "1.2.3.4" {
		t.Errorf("ProxyPath 快照不符: %+v", g.ProxyPath)
	}
	if g.ProxyPath.PathTpl != "proxyip="+config.ProxyIPPlaceholder {
		t.Errorf("路径模板缺省应补生态默认, 实际 %q", g.ProxyPath.PathTpl)
	}

	// ECHConfig SNI 指针解引用
	sni := "sni.example.com"
	cfg2 := panelConfig{ProtocolType: "vless"}
	cfg2.ECHConfig.DNS = "dns.example.com"
	cfg2.ECHConfig.SNI = &sni
	g2, ok := panelGenSettings(cfg2)
	if !ok || g2.ECHSNI != sni || g2.ECHDNS != "dns.example.com" {
		t.Errorf("ECH 配置映射不符: ok=%v g=%+v", ok, g2)
	}
}

// TestMaxi 取较大值。
func TestMaxi(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{1, 2, 2}, {2, 1, 2}, {3, 3, 3}, {-1, 0, 0}, {-5, -2, -2},
	}
	for _, c := range cases {
		if got := maxi(c.a, c.b); got != c.want {
			t.Errorf("maxi(%d,%d) = %d, 期望 %d", c.a, c.b, got, c.want)
		}
	}
}
