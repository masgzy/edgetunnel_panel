// behavior_test.go - 节点解析行为与 ProxyIP 兜底的单元测试：
// bare_ip_role（无 @ 行裸 IP 角色）、resolveProxyIP（分区域 -> 探测 -> 全局）、
// fillFallbackProxyIP（就地填充）与 flagOptions 注入后的订阅名称处理。
package service

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"edt/internal/config"
)

// writeTestFile 写入测试数据文件。
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConfigStoreBareIPRole 无 @ 行裸 IP 角色开关的解析行为。
func TestConfigStoreBareIPRole(t *testing.T) {
	newStore := func(t *testing.T, role string) *ConfigStore {
		t.Helper()
		dir := t.TempDir()
		path := dir + "/vless.txt"
		content := "1.2.3.4#裸行\n" +
			"5.6.7.8@9.9.9.9#完整行\n"
		writeTestFile(t, path, content)
		s := NewConfigStore(path, dir+"/NRT.txt", nil)
		s.SetBareIPRole(role)
		return s
	}

	t.Run("默认视为ProxyIP", func(t *testing.T) {
		entries, err := newStore(t, "proxyip").Parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("期望 2 个条目, 得到 %d", len(entries))
		}
		if entries[0].IP != "1.2.3.4" || entries[0].YxIP != "" {
			t.Errorf("裸行应解析为 ProxyIP: %+v", entries[0])
		}
		if entries[1].IP != "5.6.7.8" || entries[1].YxIP != "9.9.9.9" {
			t.Errorf("带 @ 行不受开关影响: %+v", entries[1])
		}
	})

	t.Run("yxip视为优选IP", func(t *testing.T) {
		entries, err := newStore(t, "yxip").Parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("期望 2 个条目, 得到 %d", len(entries))
		}
		if entries[0].YxIP != "1.2.3.4" || entries[0].IP != "" {
			t.Errorf("裸行应解析为优选 IP: %+v", entries[0])
		}
		if entries[0].YxHost != "1.2.3.4" || entries[0].YxPort != 0 {
			t.Errorf("优选 host/port 解析不符: %+v", entries[0])
		}
		// 带 @ 的完整格式不受开关影响
		if entries[1].IP != "5.6.7.8" || entries[1].YxIP != "9.9.9.9" {
			t.Errorf("带 @ 行不受开关影响: %+v", entries[1])
		}
	})

	t.Run("非法角色按proxyip", func(t *testing.T) {
		entries, err := newStore(t, "nonsense").Parse()
		if err != nil {
			t.Fatal(err)
		}
		if entries[0].IP != "1.2.3.4" {
			t.Errorf("非法角色应按 proxyip: %+v", entries[0])
		}
	})
}

// TestResolveProxyIPPriority 兜底解析优先级：
// 名称区域码（三字优先）> cfcolo 探测 > 全局默认。
func TestResolveProxyIPPriority(t *testing.T) {
	s := NewSubscriptionService(
		NewConfigStore(t.TempDir()+"/vless.txt", t.TempDir()+"/NRT.txt", nil),
		NewResultStore(t.TempDir()+"/result.csv"),
		NewPreIPStore(t.TempDir()+"/pre_ip.txt"),
		NewUUIDService("https://example.com/admin/config.json", t.TempDir()+"/run_time.txt", "example.com", 5),
		t.TempDir()+"/NRT.txt", 8443,
		map[string]config.SubscriptionProfile{}, "", nil, false,
		map[string]config.DataSourceConfig{},
	)

	t.Run("未配置返回空", func(t *testing.T) {
		s.SetProxyIPSettings("", false, nil)
		if got := s.resolveProxyIP("HKG-01", "1.1.1.1"); got != "" {
			t.Errorf("未配置应返回空, 得到 %q", got)
		}
	})

	t.Run("名称三字码命中by_region", func(t *testing.T) {
		s.SetProxyIPSettings("10.0.0.1", false, map[string]string{"HKG": "1.2.3.4", "NRT": "5.6.7.8"})
		if got := s.resolveProxyIP("优选 HKG-01", "1.1.1.1"); got != "1.2.3.4" {
			t.Errorf("HKG 节点应命中 by_region[HKG], 得到 %q", got)
		}
		if got := s.resolveProxyIP("NRT 02", ""); got != "5.6.7.8" {
			t.Errorf("NRT 节点应命中 by_region[NRT], 得到 %q", got)
		}
	})

	t.Run("无码回落全局", func(t *testing.T) {
		s.SetProxyIPSettings("10.0.0.1", false, map[string]string{"HKG": "1.2.3.4"})
		if got := s.resolveProxyIP("无名节点", "1.1.1.1"); got != "10.0.0.1" {
			t.Errorf("无码应回落全局, 得到 %q", got)
		}
	})

	t.Run("二字码键低优先", func(t *testing.T) {
		// 名称同时含三字码与二字码：三字码优先
		s.SetProxyIPSettings("", false, map[string]string{"JP": "2.2.2.2", "NRT": "5.6.7.8"})
		if got := s.resolveProxyIP("JP NRT", ""); got != "5.6.7.8" {
			t.Errorf("三字码应优先, 得到 %q", got)
		}
		// 仅有二字码时正常命中
		if got := s.resolveProxyIP("JP node", ""); got != "2.2.2.2" {
			t.Errorf("二字码键应可命中, 得到 %q", got)
		}
	})

	t.Run("探测关闭时不发网络请求", func(t *testing.T) {
		s.SetProxyIPSettings("10.0.0.1", false, map[string]string{"HKG": "1.2.3.4"})
		// detect=false：无码节点直接回落全局，不会因 host 不可达而阻塞
		if got := s.resolveProxyIP("no-code", "203.0.113.1"); got != "10.0.0.1" {
			t.Errorf("探测关闭应直接回落全局, 得到 %q", got)
		}
	})
}

// TestFillFallbackProxyIP 就地填充：显式 IP 不覆盖，空 IP 按名称码填充。
func TestFillFallbackProxyIP(t *testing.T) {
	s := NewSubscriptionService(
		NewConfigStore(t.TempDir()+"/vless.txt", t.TempDir()+"/NRT.txt", nil),
		NewResultStore(t.TempDir()+"/result.csv"),
		NewPreIPStore(t.TempDir()+"/pre_ip.txt"),
		NewUUIDService("", t.TempDir()+"/run_time.txt", "example.com", 5),
		t.TempDir()+"/NRT.txt", 8443,
		map[string]config.SubscriptionProfile{}, "", nil, false,
		map[string]config.DataSourceConfig{},
	)
	s.SetProxyIPSettings("10.0.0.1", false, map[string]string{"HKG": "1.2.3.4", "NRT": "5.6.7.8"})

	items := []SubItem{
		{Name: "HKG-01", IP: "", YxHost: "1.1.1.1"},
		{Name: "显式节点", IP: "8.8.8.8"},
		{Name: "NRT 节点", IP: "", YxIP: "2.2.2.2:2053"},
		{Name: "无码节点", IP: ""},
	}
	s.fillFallbackProxyIP(items)

	if items[0].IP != "1.2.3.4" {
		t.Errorf("HKG 节点应填充 by_region 值, 得到 %q", items[0].IP)
	}
	if items[1].IP != "8.8.8.8" {
		t.Errorf("显式 IP 不应被覆盖, 得到 %q", items[1].IP)
	}
	if items[2].IP != "5.6.7.8" {
		t.Errorf("NRT 节点应填充 by_region 值, 得到 %q", items[2].IP)
	}
	if items[3].IP != "10.0.0.1" {
		t.Errorf("无码节点应回落全局, 得到 %q", items[3].IP)
	}
}

// TestFlagOptionsWiring 国旗开关注入后订阅名称按码补旗（TW 用中国旗）。
func TestFlagOptionsWiring(t *testing.T) {
	s := NewSubscriptionService(
		NewConfigStore(t.TempDir()+"/vless.txt", t.TempDir()+"/NRT.txt", nil),
		NewResultStore(t.TempDir()+"/result.csv"),
		NewPreIPStore(t.TempDir()+"/pre_ip.txt"),
		NewUUIDService("", t.TempDir()+"/run_time.txt", "example.com", 5),
		t.TempDir()+"/NRT.txt", 8443,
		map[string]config.SubscriptionProfile{}, "", nil, false,
		map[string]config.DataSourceConfig{},
	)
	s.SetFlagOptions(true, false)

	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if got := s.applyFlagToName("HKG-01"); got != "🇭🇰 HKG-01" {
		t.Errorf("applyFlagToName(HKG-01) = %q", got)
	}
	if got := s.applyFlagToName("TPE-03"); !strings.HasPrefix(got, "🇨🇳") {
		t.Errorf("TPE 节点应使用中国旗帜, 得到 %q", got)
	}
	// 已含旗帜的名称不重复补旗
	if got := s.applyFlagToName("🇯🇵 NRT"); got != "🇯🇵 日本 NRT" {
		t.Errorf("已有旗帜应只补中文名: %q", got)
	}
}

// TestDetectBudgetConcurrent 并发构建下探测预算的原子计数不产生数据竞争
// （需 go test -race 运行方有意义）。
func TestDetectBudgetConcurrent(t *testing.T) {
	s := NewSubscriptionService(
		NewConfigStore(t.TempDir()+"/vless.txt", t.TempDir()+"/NRT.txt", nil),
		NewResultStore(t.TempDir()+"/result.csv"),
		NewPreIPStore(t.TempDir()+"/pre_ip.txt"),
		NewUUIDService("", t.TempDir()+"/run_time.txt", "example.com", 5),
		t.TempDir()+"/NRT.txt", 8443,
		map[string]config.SubscriptionProfile{}, "", nil, false,
		map[string]config.DataSourceConfig{},
	)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.detectBudget.Store(detectBudgetPerBuild)
			_ = s.detectColo("") // 空 host 直接跳过，不触网
		}()
	}
	wg.Wait()
}

// TestBuildNodeLinkALPN 生成配置 ALPN 字段 → 分享链接 alpn 参数（需求：链接需携带 alpn）。
func TestBuildNodeLinkALPN(t *testing.T) {
	base := config.DefaultGenSettings()
	// 未配置 ALPN：链接不携带 alpn 参数
	link := BuildNodeLink(base, "edt", LinkParams{
		UUID: "233a3a24-35a5-4a1d-9ebe-3e0bda3b3f3b", Authority: "1.2.3.4:443",
		Domain: "example.com", Name: "n1",
	})
	if strings.Contains(link, "alpn=") {
		t.Errorf("未配置 ALPN 时不应输出 alpn 参数: %s", link)
	}
	// 配置 ALPN：vless / trojan 链接均携带编码后的 alpn
	g := base
	g.ALPN = "h2,http/1.1"
	for _, proto := range []string{"vless", "trojan"} {
		g2 := g
		g2.Protocol = proto
		link := BuildNodeLink(g2, "edt", LinkParams{
			UUID: "233a3a24-35a5-4a1d-9ebe-3e0bda3b3f3b", Authority: "1.2.3.4:443",
			Domain: "example.com", Name: "n1",
		})
		if !strings.Contains(link, "alpn=h2%2Chttp%2F1.1") {
			t.Errorf("%s 链接应携带编码 alpn 参数: %s", proto, link)
		}
	}
}

// TestUUIDDiskCacheRoundtrip UUID 磁盘缓存：落盘 → 新实例恢复（重启零等待语义）。
func TestUUIDDiskCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	runTimeFile := dir + "/run_time.txt"

	u1 := NewUUIDService("https://p.example.com/admin/config.json", runTimeFile, "p.example.com", 5)
	u1.mu.Lock()
	u1.cachedUUID = "disk-cached-uuid"
	u1.fetchedAt = time.Now()
	u1.genOK = true
	u1.genSettings = config.DefaultGenSettings()
	u1.genSettings.Transport = "xhttp"
	u1.usagePages, u1.usageWorkers, u1.usageMax, u1.usageOK = 10, 20, 100000, true
	u1.genHost = "panel.example.com"
	u1.genHosts = []string{"panel.example.com"}
	cache := u1.buildDiskCacheLocked()
	u1.mu.Unlock()
	u1.saveDiskCache(cache)

	// 新实例（模拟重启）应从磁盘恢复 UUID/快照
	u2 := NewUUIDService("https://p.example.com/admin/config.json", runTimeFile, "p.example.com", 5)
	u2.mu.Lock()
	gotUUID := u2.cachedUUID
	gotGen := u2.genOK
	gotGenTransport := u2.genSettings.Transport
	u2.mu.Unlock()
	if gotUUID != "disk-cached-uuid" {
		t.Errorf("磁盘缓存恢复 UUID = %q, 期望 disk-cached-uuid", gotUUID)
	}
	if !gotGen || gotGenTransport != "xhttp" {
		t.Errorf("磁盘缓存恢复生成配置不完整: ok=%v transport=%q", gotGen, gotGenTransport)
	}
	if cf, ok := u2.UsageSnapshot(); !ok || cf.Pages != 10 || cf.Workers != 20 {
		t.Errorf("磁盘缓存恢复用量快照失败: %+v ok=%v", cf, ok)
	}
	// Get 应直接返回缓存值（不发网络）
	if got, err := u2.Get(); err != nil || got != "disk-cached-uuid" {
		t.Errorf("Get = (%q,%v), 期望命中磁盘缓存", got, err)
	}
	// Clear 应删除磁盘缓存文件
	if err := u2.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(u2.cachePath); !os.IsNotExist(err) {
		t.Errorf("Clear 后磁盘缓存应被删除, err=%v", err)
	}
}
