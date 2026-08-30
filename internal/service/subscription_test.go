// subscription_test.go - SubscriptionService 单元测试：
// 覆盖 cfgMu 读写锁保护的读端/写端并发（配合 go test -race）、
// subconverterBaseURL 的本地端口副本补齐（不回写共享状态）与模板/数据源列表排序。
package service

import (
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"edt/internal/config"
)

const testStaticUUID = "233a3a24-35a5-4a1d-9ebe-3e0bda3b3f3b"

// newTestSubscriptionService 构造完整依赖的订阅服务（static UUID，不发网络请求）。
func newTestSubscriptionService(t *testing.T) (*SubscriptionService, *ConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	vlessPath := filepath.Join(dir, "vless.txt")
	nrtPath := filepath.Join(dir, "nrt.txt")

	cs := NewConfigStore(vlessPath, nrtPath, nil)
	rs := NewResultStore(filepath.Join(dir, "result.csv"))
	ps := NewPreIPStore(filepath.Join(dir, "pre_ip.txt"))
	us := NewUUIDService("", filepath.Join(dir, "run_time.txt"), "example.com", 1)

	profiles := map[string]config.SubscriptionProfile{
		"vless": {
			Key: "vless", Name: "默认模板", Format: "edt",
			Domain: "example.com", UUIDMode: "static", UUID: testStaticUUID,
		},
	}
	defaultByID := map[string]string{"1": "vless"}
	dataSources := map[string]config.DataSourceConfig{
		"1": {ID: "1", Name: "本地节点", Kind: "vless_file", Path: vlessPath},
	}
	s := NewSubscriptionService(cs, rs, ps, us, nrtPath, 2053, profiles, "vless", defaultByID, false, dataSources)
	return s, cs, dir
}

// TestSubscriptionBuildVless 基线：vless_file 数据源构建订阅（明文与 base64 两种模式）。
func TestSubscriptionBuildVless(t *testing.T) {
	s, cs, _ := newTestSubscriptionService(t)
	if err := cs.Add("1.1.1.1", "东京", "8.8.8.8:2053"); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if err := cs.Add("DIRECT", "直连", ""); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}

	sub, err := s.BuildSubscription("1", "")
	if err != nil {
		t.Fatalf("BuildSubscription 失败: %v", err)
	}
	if !strings.Contains(sub, "vless://") || !strings.Contains(sub, "example.com") {
		t.Errorf("订阅内容缺失关键字段: %q", sub)
	}
	// subID != "3" 时节点名带旗帜前缀，但名称本身应保留（链接 fragment 经 URL 编码）
	if !strings.Contains(sub, url.QueryEscape("东京")) {
		t.Errorf("订阅应包含节点名 东京（编码后）: %q", sub)
	}

	// encodeB64 开启后应输出 base64
	alt := newTestSubscriptionServiceAlt(t, true)
	body, err := alt.BuildSubscription("1", "")
	if err != nil {
		t.Fatalf("BuildSubscription(b64) 失败: %v", err)
	}
	if strings.Contains(body, "vless://") {
		t.Errorf("开启 encodeB64 后不应包含明文 vless://: %q", body)
	}
}

// newTestSubscriptionServiceAlt 构造 encodeB64 可选的订阅服务。
func newTestSubscriptionServiceAlt(t *testing.T, encodeB64 bool) *SubscriptionService {
	t.Helper()
	dir := t.TempDir()
	vlessPath := filepath.Join(dir, "vless.txt")
	cs := NewConfigStore(vlessPath, filepath.Join(dir, "nrt.txt"), nil)
	if err := cs.Add("1.1.1.1", "东京", ""); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	profiles := map[string]config.SubscriptionProfile{
		"vless": {Key: "vless", Name: "默认", Format: "edt", Domain: "example.com",
			UUIDMode: "static", UUID: testStaticUUID},
	}
	return NewSubscriptionService(cs,
		NewResultStore(filepath.Join(dir, "result.csv")),
		NewPreIPStore(filepath.Join(dir, "pre_ip.txt")),
		NewUUIDService("", filepath.Join(dir, "run_time.txt"), "example.com", 1),
		filepath.Join(dir, "nrt.txt"), 443, profiles, "vless",
		map[string]string{"1": "vless"}, encodeB64,
		map[string]config.DataSourceConfig{
			"1": {ID: "1", Name: "本地", Kind: "vless_file", Path: vlessPath},
		})
}

// TestSubscriptionResolveProfileErrors 未知模板/未知数据源应报错而非 panic。
func TestSubscriptionResolveProfileErrors(t *testing.T) {
	s, _, _ := newTestSubscriptionService(t)
	if _, err := s.BuildSubscription("1", "不存在"); err == nil {
		t.Error("未知模板应报错")
	}
	if _, err := s.BuildSubscription("99", ""); err == nil {
		t.Error("未知数据源应报错")
	}
}

// TestSubscriptionConcurrentReloadAndBuild 并发回归：Reload/Set* 写端
// 与 Build*/Status/GetDomain 读端并发执行不得产生数据竞争。
// 该用例需在 go test -race 下运行方有意义。
func TestSubscriptionConcurrentReloadAndBuild(t *testing.T) {
	s, cs, dir := newTestSubscriptionService(t)
	if err := cs.Add("1.1.1.1", "东京", "8.8.8.8"); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if err := cs.Add("2.2.2.2", "大阪", ""); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}

	profilesB := map[string]config.SubscriptionProfile{
		"vless": {Key: "vless", Name: "模板B", Format: "edt", Domain: "b.example.com",
			UUIDMode: "static", UUID: testStaticUUID},
		"clash": {Key: "clash", Name: "Clash", Format: "edt", Domain: "b.example.com",
			UUIDMode: "dynamic"},
	}
	dataB := map[string]config.DataSourceConfig{
		"1": {ID: "1", Name: "本地B", Kind: "vless_file", Path: filepath.Join(dir, "vless.txt")},
	}

	const workers = 9
	const iters = 60
	var wg sync.WaitGroup
	wg.Add(workers)

	go func() { // Reload 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.Reload(filepath.Join(dir, "nrt.txt"), 8443, profilesB, "clash",
				map[string]string{"1": "clash"}, i%2 == 0, dataB)
		}
	}()
	go func() { // SetGenConfig 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.SetGenConfig(false, i%2 == 0, config.DefaultGenSettings())
		}
	}()
	go func() { // SetSubConverter / SetUserinfoExpire 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.SetSubConverter(SubConverterConfig{Mode: "local", LocalPort: 25500})
			s.SetUserinfoExpire("2027-01-01")
		}
	}()
	go func() { // Build 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := s.BuildSubscription("1", "vless"); err != nil {
				t.Errorf("BuildSubscription: %v", err)
				return
			}
		}
	}()
	go func() { // mihomo 构建读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := s.BuildMihomoSubscription("1", "vless", ""); err != nil {
				t.Errorf("BuildMihomoSubscription: %v", err)
				return
			}
		}
	}()
	go func() { // 状态读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.RuntimeStatus()
			_ = s.SubConverterStatus()
		}
	}()
	go func() { // 生成配置读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.EffectiveGenSettings()
			_ = s.PanelGenAvailable()
		}
	}()
	go func() { // 域名读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := s.GetDomain("1", "vless"); err != nil {
				t.Errorf("GetDomain: %v", err)
				return
			}
		}
	}()
	go func() { // userinfo 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = s.GetUserinfo()
		}
	}()
	wg.Wait()
}

// TestSubconverterBaseURL 桥接基地址解析：off 报错、local 缺省端口补齐且
// 不回写共享结构（修复点：锁内写 LocalPort 已改为局部副本）、remote 去尾斜杠。
func TestSubconverterBaseURL(t *testing.T) {
	s, _, _ := newTestSubscriptionService(t)

	// off / 未配置
	s.SetSubConverter(SubConverterConfig{Mode: "off"})
	if _, err := s.subconverterBaseURL(); err == nil {
		t.Error("mode=off 应报错")
	}

	// local + 缺省端口：补 25500，且不得回写共享状态
	s.SetSubConverter(SubConverterConfig{Mode: "local"})
	s.cfgMu.RLock()
	got, err := s.subconverterBaseURL()
	localPort := s.subConverter.LocalPort
	s.cfgMu.RUnlock()
	if err != nil {
		t.Fatalf("local 解析失败: %v", err)
	}
	if got != "http://127.0.0.1:25500" {
		t.Errorf("local 缺省端口 base = %q, 期望 http://127.0.0.1:25500", got)
	}
	if localPort != 0 {
		t.Errorf("缺省端口不应回写共享状态, LocalPort = %d", localPort)
	}

	// local + 显式端口
	s.SetSubConverter(SubConverterConfig{Mode: "local", LocalPort: 12345})
	s.cfgMu.RLock()
	got, _ = s.subconverterBaseURL()
	s.cfgMu.RUnlock()
	if got != "http://127.0.0.1:12345" {
		t.Errorf("local 显式端口 base = %q, 期望 http://127.0.0.1:12345", got)
	}

	// remote：去尾斜杠
	s.SetSubConverter(SubConverterConfig{Mode: "remote", Remote: "https://api.example.com/"})
	s.cfgMu.RLock()
	got, err = s.subconverterBaseURL()
	s.cfgMu.RUnlock()
	if err != nil || got != "https://api.example.com" {
		t.Errorf("remote base = (%q,%v), 期望 (https://api.example.com,nil)", got, err)
	}

	// remote 未填地址
	s.SetSubConverter(SubConverterConfig{Mode: "remote"})
	if _, err := s.subconverterBaseURL(); err == nil {
		t.Error("remote 未填地址应报错")
	}
}

// TestSubConverterStatus 状态返回需包含 mode/remote/local_port 三键。
func TestSubConverterStatus(t *testing.T) {
	s, _, _ := newTestSubscriptionService(t)
	s.SetSubConverter(SubConverterConfig{Mode: "local", LocalPort: 25500})
	st := s.SubConverterStatus()
	if st["mode"] != "local" || st["local_port"] != 25500 {
		t.Errorf("SubConverterStatus = %v", st)
	}
}

// TestProfileListAndDataSourceListSorted 列表输出必须按键/ID 排序，保证 API 顺序稳定；
// 数据源 ID 为纯数字时按数值序（2 < 9 < 10）。
func TestProfileListAndDataSourceListSorted(t *testing.T) {
	profiles := map[string]config.SubscriptionProfile{
		"zebra": {Key: "zebra", Name: "Z"},
		"alpha": {Key: "alpha", Name: "A"},
		"mid":   {Key: "mid", Name: "M"},
	}
	list := profileList(profiles)
	if len(list) != 3 {
		t.Fatalf("profileList 长度 = %d, 期望 3", len(list))
	}
	wantOrder := []string{"alpha", "mid", "zebra"}
	for i, want := range wantOrder {
		if list[i]["key"] != want {
			t.Errorf("profileList[%d] = %v, 期望 key=%s", i, list[i], want)
		}
	}

	sources := map[string]config.DataSourceConfig{
		"10": {ID: "10", Name: "十", Kind: "vless_file"},
		"9":  {ID: "9", Name: "九", Kind: "vless_file"},
		"2":  {ID: "2", Name: "二", Kind: "vless_file"},
		"ab": {ID: "ab", Name: "混排", Kind: "vless_file"},
	}
	dlist := dataSourceList(sources)
	wantIDs := []string{"2", "9", "10", "ab"}
	if len(dlist) != len(wantIDs) {
		t.Fatalf("dataSourceList 长度 = %d, 期望 %d", len(dlist), len(wantIDs))
	}
	for i, want := range wantIDs {
		if dlist[i]["id"] != want {
			t.Errorf("dataSourceList[%d] = %v, 期望 id=%s（数值序应使 2<9<10）", i, dlist[i], want)
		}
	}
}

// TestMarkDuplicates 重名节点加序号后缀，保证订阅内名称唯一。
func TestMarkDuplicates(t *testing.T) {
	got := MarkDuplicates([]string{"a", "a", "b", "a", "b"})
	want := []string{"a", "a (1)", "b", "a (2)", "b (1)"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MarkDuplicates[%d] = %q, 期望 %q（完整结果 %#v）", i, got[i], want[i], got)
		}
	}
}
