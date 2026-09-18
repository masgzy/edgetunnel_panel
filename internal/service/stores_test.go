// stores_test.go - PreIPStore / ResultStore 单元测试：
// 覆盖热更新路径 Reload 与文件读写的并发安全（配合 go test -race）及基础读写语义。
package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"edt/internal/module"
)

// TestPreIPStoreSetGetReload pre_ip.txt 两行格式的读写与热更新。
func TestPreIPStoreSetGetReload(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "pre_ip_a.txt")
	p := NewPreIPStore(pathA)

	// 缺失文件返回空
	if pre, ip := p.Get(); pre != "" || ip != "" {
		t.Errorf("缺失文件 Get = (%q,%q), 期望 (\"\",\"\")", pre, ip)
	}
	// 写入并回读
	if err := p.Set("JP", "1.2.3.4"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if pre, ip := p.Get(); pre != "JP" || ip != "1.2.3.4" {
		t.Errorf("Get = (%q,%q), 期望 (JP,1.2.3.4)", pre, ip)
	}
	// Reload 后新路径为空
	pathB := filepath.Join(dir, "pre_ip_b.txt")
	p.Reload(pathB)
	if pre, ip := p.Get(); pre != "" || ip != "" {
		t.Errorf("Reload 后 Get = (%q,%q), 期望 (\"\",\"\")", pre, ip)
	}
	if err := p.Set("HK", "5.6.7.8"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if pre, ip := p.Get(); pre != "HK" || ip != "5.6.7.8" {
		t.Errorf("新路径 Get = (%q,%q), 期望 (HK,5.6.7.8)", pre, ip)
	}
	// 旧路径内容未被破坏
	data, err := os.ReadFile(pathA)
	if err != nil || string(data) != "JP\n1.2.3.4" {
		t.Errorf("旧路径内容 = (%q,%v), 期望 (\"JP\\n1.2.3.4\",nil)", data, err)
	}
	// 单行文件：ip 为空
	if err := os.WriteFile(pathB, []byte("ONLYPRE"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if pre, ip := p.Get(); pre != "ONLYPRE" || ip != "" {
		t.Errorf("单行文件 Get = (%q,%q), 期望 (ONLYPRE,\"\")", pre, ip)
	}
}

// TestResultStoreReadAndEntries result.csv 解析：首行 IP、地区码映射与节点条目转换。
func TestResultStoreReadAndEntries(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "result.csv")
	r := NewResultStore(csvPath)

	// 缺失文件：无数据语义
	if ip := r.GetFirstIP(); ip != "" {
		t.Errorf("缺失文件 GetFirstIP = %q, 期望空", ip)
	}
	if entries, err := r.GetAllAsProxyEntries("", "", module.FlagOptions{}); err != nil || len(entries) != 0 {
		t.Errorf("缺失文件 GetAllAsProxyEntries = (%d,%v), 期望 (0,nil)", len(entries), err)
	}
	if _, err := r.GetFirst(); !errors.Is(err, ErrNoData) {
		t.Errorf("缺失文件 GetFirst 错误 = %v, 期望 ErrNoData", err)
	}

	// 写入带表头的 CSV
	content := "IP,Send,Res,PLR,Ping,Speed,Code\n" +
		"1.1.1.1,4,4,0,100.5,10.25,HKG\n" +
		"2.2.2.2,4,3,25,200,5,NRT\n" +
		"3.3.3.3,4,4,0,50,20,SJC\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 result.csv 失败: %v", err)
	}

	if ip := r.GetFirstIP(); ip != "1.1.1.1" {
		t.Errorf("GetFirstIP = %q, 期望 1.1.1.1", ip)
	}
	first, err := r.GetFirst()
	if err != nil {
		t.Fatalf("GetFirst 失败: %v", err)
	}
	if first["ip"] != "1.1.1.1" || first["send"] != 4 || first["plr"] != float64(0) ||
		first["ping"] != float64(100.5) || first["speed"] != float64(10.25) || first["code"] != "HKG" {
		t.Errorf("GetFirst 内容不符: %v", first)
	}

	entries, err := r.GetAllAsProxyEntries("JP", "9.9.9.9", module.FlagOptions{IATA: true})
	if err != nil {
		t.Fatalf("GetAllAsProxyEntries 失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("期望 3 个条目, 实际 %d", len(entries))
	}
	// IATA 开启：三个码均应带旗，且 Code 字段保留原始码供分区域匹配
	for i, want := range []string{"HKG", "NRT", "SJC"} {
		if !strings.Contains(entries[i].Name, want) {
			t.Errorf("条目 %d 名称应含 %s: %q", i, want, entries[i].Name)
		}
		if entries[i].Code != want {
			t.Errorf("条目 %d Code = %q, 期望 %q", i, entries[i].Code, want)
		}
	}
	for i, want := range []string{"9.9.9.9", "9.9.9.9", "9.9.9.9"} {
		if entries[i].IP != want {
			t.Errorf("条目 %d IP = %q, 期望 %q", i, entries[i].IP, want)
		}
	}
	wantYx := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	for i, want := range wantYx {
		if entries[i].YxIP != want {
			t.Errorf("条目 %d YxIP = %q, 期望 %q", i, entries[i].YxIP, want)
		}
	}

	// 少于 7 列的残行应被跳过
	if err := os.WriteFile(csvPath, []byte("IP,Send,Res,PLR,Ping,Speed,Code\nbad,row\n"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	entries, err = r.GetAllAsProxyEntries("", "", module.FlagOptions{})
	if err != nil || len(entries) != 0 {
		t.Errorf("残行应被跳过: (%d,%v), 期望 (0,nil)", len(entries), err)
	}
}

// TestStoresConcurrentReloadAndRead 并发回归：Reload 热替换路径
// 与 Get/readRows 并发执行不得产生数据竞争。
// 该用例需在 go test -race 下运行方有意义。
func TestStoresConcurrentReloadAndRead(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "result_a.csv")
	pathB := filepath.Join(dir, "result_b.csv")
	content := "IP,Send,Res,PLR,Ping,Speed,Code\n1.1.1.1,4,4,0,100,10,HKG\n"
	for _, p := range []string{pathA, pathB} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", p, err)
		}
	}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(4)

	r := NewResultStore(pathA)
	go func() { // ResultStore Reload 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				r.Reload(pathB)
			} else {
				r.Reload(pathA)
			}
		}
	}()
	go func() { // ResultStore 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := r.GetFirst(); err != nil && !errors.Is(err, ErrNoData) {
				t.Errorf("GetFirst: %v", err)
				return
			}
		}
	}()

	preA := filepath.Join(dir, "pre_a.txt")
	preB := filepath.Join(dir, "pre_b.txt")
	if err := os.WriteFile(preA, []byte("JP\n1.2.3.4"), 0o644); err != nil {
		t.Fatalf("写入 pre_a 失败: %v", err)
	}
	p := NewPreIPStore(preA)
	go func() { // PreIPStore Reload 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				p.Reload(preB)
			} else {
				p.Reload(preA)
			}
		}
	}()
	go func() { // PreIPStore 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = p.Get()
		}
	}()
	wg.Wait()
}
