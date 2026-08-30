// config_store_test.go - ConfigStore 单元测试：
// 覆盖内置 NRT 变量的三种写法解析、节点行字段净化、
// 纯函数（行切分/重排）以及 Reload 与读写路径的并发安全（配合 go test -race）。
package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"edt/internal/config"
)

// newTestConfigStore 构造带临时目录的 ConfigStore，变量表含 inline/file 两种来源。
func newTestConfigStore(t *testing.T) (*ConfigStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	vlessPath := filepath.Join(dir, "vless.txt")
	nrtPath := filepath.Join(dir, "nrt.txt")

	fileVarPath := filepath.Join(dir, "filevar.txt")
	if err := os.WriteFile(fileVarPath, []byte("file-value\n"), 0o644); err != nil {
		t.Fatalf("写入 filevar 失败: %v", err)
	}
	vars := map[string]config.VariableSource{
		"MYVAR":   {Key: "MYVAR", SourceType: "inline", Value: "inline-value"},
		"FILEVAR": {Key: "FILEVAR", SourceType: "file", Path: fileVarPath},
	}
	return NewConfigStore(vlessPath, nrtPath, vars), vlessPath, nrtPath
}

// TestExtractVariableKeyNRT 内置 NRT 变量无需注册即可识别：
// 裸值 / {{NRT}} / ${NRT}（含内侧空白）三种写法均应返回 "NRT"。
func TestExtractVariableKeyNRT(t *testing.T) {
	cs, _, _ := newTestConfigStore(t)

	nrtForms := []string{"NRT", "{{NRT}}", "${NRT}", "{{ NRT }}", "${ NRT }"}
	for _, form := range nrtForms {
		if got := cs.extractVariableKey(form); got != "NRT" {
			t.Errorf("extractVariableKey(%q) = %q, 期望 NRT", form, got)
		}
	}
	// 未注册变量仍应拒绝
	if got := cs.extractVariableKey("{{FOO}}"); got != "" {
		t.Errorf("extractVariableKey({{FOO}}) = %q, 期望空串", got)
	}
	if got := cs.extractVariableKey("${FOO}"); got != "" {
		t.Errorf("extractVariableKey(${FOO}) = %q, 期望空串", got)
	}
	// 已注册变量原样返回键名
	if got := cs.extractVariableKey("MYVAR"); got != "MYVAR" {
		t.Errorf("extractVariableKey(MYVAR) = %q, 期望 MYVAR", got)
	}
	if got := cs.extractVariableKey("{{MYVAR}}"); got != "MYVAR" {
		t.Errorf("extractVariableKey({{MYVAR}}) = %q, 期望 MYVAR", got)
	}
}

// TestParseLineNRTVariable NRT 变量端到端解析：
// 节点行中三种写法都应取到 nrt.txt 的内容；nrt 文件缺失时回退为字面量 NRT。
func TestParseLineNRTVariable(t *testing.T) {
	cs, vlessPath, nrtPath := newTestConfigStore(t)

	if err := os.WriteFile(nrtPath, []byte("1.2.3.4\n"), 0o644); err != nil {
		t.Fatalf("写入 nrt 文件失败: %v", err)
	}
	content := "NRT@5.6.7.8#东京\n{{NRT}}#节点B\n${NRT}#节点C\n"
	if err := os.WriteFile(vlessPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}

	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("期望解析出 3 个节点，实际 %d 个: %+v", len(entries), entries)
	}
	for i, want := range []string{"1.2.3.4", "1.2.3.4", "1.2.3.4"} {
		if entries[i].IP != want {
			t.Errorf("entries[%d].IP = %q, 期望 %q（NRT 应取 nrt.txt 内容）", i, entries[i].IP, want)
		}
	}
	if entries[0].YxIP != "5.6.7.8" {
		t.Errorf("entries[0].YxIP = %q, 期望 5.6.7.8", entries[0].YxIP)
	}

	// nrt 文件缺失：回退为字面量，节点行仍可解析
	if err := os.Remove(nrtPath); err != nil {
		t.Fatalf("删除 nrt 文件失败: %v", err)
	}
	entries, err = cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("期望解析出 3 个节点，实际 %d 个", len(entries))
	}
	for i, want := range []string{"NRT", "NRT", "NRT"} {
		if entries[i].IP != want {
			t.Errorf("nrt 缺失时 entries[%d].IP = %q, 期望字面量 %q", i, entries[i].IP, want)
		}
	}
}

// TestParseLineVariableForms 普通（非内置）变量的三种写法解析。
func TestParseLineVariableForms(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)

	content := "MYVAR#内联\n{{MYVAR}}#花括号\n${MYVAR}#美元\nFILEVAR#文件\n"
	if err := os.WriteFile(vlessPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}
	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("期望解析出 4 个节点，实际 %d 个", len(entries))
	}
	for i, want := range []string{"inline-value", "inline-value", "inline-value", "file-value"} {
		if entries[i].IP != want {
			t.Errorf("entries[%d].IP = %q, 期望 %q", i, entries[i].IP, want)
		}
	}
}

// TestParseLineDirectAndEmpty DIRECT/空 IP 应规范化为空（表示直连）。
func TestParseLineDirectAndEmpty(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)

	content := "DIRECT#直连\n@1.1.1.1#空反代\n普通#普通\n"
	if err := os.WriteFile(vlessPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}
	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("期望解析出 3 个节点，实际 %d 个: %+v", len(entries), entries)
	}
	if entries[0].IP != "" {
		t.Errorf("DIRECT 应解析为空 IP，实际 %q", entries[0].IP)
	}
	if entries[1].IP != "" {
		t.Errorf("空反代行应解析为空 IP，实际 %q", entries[1].IP)
	}
	if entries[2].IP != "普通" {
		t.Errorf("普通行 IP = %q, 期望 普通", entries[2].IP)
	}
}

// TestFormatLineSanitizesNewlines formatLine 必须净化字段中的换行/回车，
// 防止一行节点被拆成多行破坏 vless.txt 行结构。
func TestFormatLineSanitizesNewlines(t *testing.T) {
	cs, _, _ := newTestConfigStore(t)

	cases := []struct{ ip, name, yxIP string }{
		{"1.2.3.4\n2.3.4.5", "名字\r\n第二行", "8.8.8.8\n9.9.9.9"},
		{"ip\r注入", "n", "y\rx"},
		{"\n前导", "\t缩进名\n", "\r\n"},
	}
	for _, c := range cases {
		line := cs.formatLine(c.ip, c.name, c.yxIP)
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("formatLine(%q,%q,%q) = %q, 含换行/回车", c.ip, c.name, c.yxIP, line)
		}
	}

	// DIRECT 与空 IP 规范化
	if got := cs.formatLine("DIRECT", "n", ""); got != "#n" {
		t.Errorf("formatLine(DIRECT,n,\"\") = %q, 期望 #n", got)
	}
	if got := cs.formatLine("", "n", ""); got != "#n" {
		t.Errorf("formatLine(\"\",n,\"\") = %q, 期望 #n", got)
	}
	// 带优选：ip@yx#name
	if got := cs.formatLine("1.2.3.4", "n", "8.8.8.8:2053"); got != "1.2.3.4@8.8.8.8:2053#n" {
		t.Errorf("formatLine 带优选 = %q, 期望 1.2.3.4@8.8.8.8:2053#n", got)
	}
	// 换行被替换为空格
	if got := cs.formatLine("1.2.3.4\n4.5.6.7", "n", ""); got != "1.2.3.4 4.5.6.7#n" {
		t.Errorf("formatLine 换行净化 = %q, 期望 \"1.2.3.4 4.5.6.7#n\"", got)
	}
}

// TestSplitLinesKeepEnds 行切分需与 Python splitlines(keepends=True) 一致。
func TestSplitLinesKeepEnds(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a\nb\r\nc\rd", []string{"a\n", "b\r\n", "c\r", "d"}},
		{"a\n", []string{"a\n"}},
		{"a", []string{"a"}},
		{"", nil},
		{"\n", []string{"\n"}},
		{"a\r\n", []string{"a\r\n"}},
		{"\r\n\r", []string{"\r\n", "\r"}},
	}
	for _, c := range cases {
		got := splitLinesKeepEnds(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitLinesKeepEnds(%q) = %#v, 期望 %#v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitLinesKeepEnds(%q)[%d] = %q, 期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestReorderLines 行重排：前移、后移、自身位置均应稳定。
func TestReorderLines(t *testing.T) {
	lines := []string{"l1\n", "l2\n", "l3\n"}

	cases := []struct {
		moveSrc, targetSrc int
		want               []string
	}{
		{1, 3, []string{"l2\n", "l1\n", "l3\n"}}, // 后移：插到 l3 之前
		{3, 1, []string{"l3\n", "l1\n", "l2\n"}}, // 前移：插到 l1 之前
		{2, 2, []string{"l1\n", "l2\n", "l3\n"}}, // 不动
		{1, 2, []string{"l1\n", "l2\n", "l3\n"}}, // 移到下一行之前：等价不动
	}
	for _, c := range cases {
		got := reorderLines(append([]string(nil), lines...), c.moveSrc, c.targetSrc)
		if len(got) != len(c.want) {
			t.Errorf("reorderLines(move=%d,target=%d) = %#v, 期望 %#v", c.moveSrc, c.targetSrc, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("reorderLines(move=%d,target=%d)[%d] = %q, 期望 %q",
					c.moveSrc, c.targetSrc, i, got[i], c.want[i])
			}
		}
	}
}

// TestConfigStoreCRUDRoundTrip 增改删换位全链路回归（保证并发测试的数据语义正确）。
func TestConfigStoreCRUDRoundTrip(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)

	// Add 两行
	if err := cs.Add("1.1.1.1", "节点A", "8.8.8.8"); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if err := cs.Add("2.2.2.2", "节点B", ""); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "节点A" || entries[1].Name != "节点B" {
		t.Fatalf("Add 后内容不符: %+v", entries)
	}

	// FindDuplicate：IP 与 name 任一匹配
	if line, err := cs.FindDuplicate("", "节点B"); err != nil || line != 2 {
		t.Errorf("FindDuplicate(name=节点B) = (%d,%v), 期望 (2,nil)", line, err)
	}
	if line, err := cs.FindDuplicate("2.2.2.2", ""); err != nil || line != 2 {
		t.Errorf("FindDuplicate(ip=2.2.2.2) = (%d,%v), 期望 (2,nil)", line, err)
	}
	if line, err := cs.FindDuplicate("9.9.9.9", "不存在"); err != nil || line != 0 {
		t.Errorf("FindDuplicate(无重复) = (%d,%v), 期望 (0,nil)", line, err)
	}

	// UpdateVisible：只传 name，其余回填原值
	newName := "节点A改"
	if err := cs.UpdateVisible(1, nil, &newName, nil); err != nil {
		t.Fatalf("UpdateVisible 失败: %v", err)
	}
	entries, _ = cs.Parse()
	if entries[0].Name != "节点A改" || entries[0].IP != "1.1.1.1" || entries[0].YxIP != "8.8.8.8" {
		t.Errorf("UpdateVisible 未回填原值: %+v", entries[0])
	}

	// SwapVisible
	if err := cs.SwapVisible(1, 2); err != nil {
		t.Fatalf("SwapVisible 失败: %v", err)
	}
	entries, _ = cs.Parse()
	if entries[0].Name != "节点B" || entries[1].Name != "节点A改" {
		t.Errorf("SwapVisible 后顺序不符: %+v", entries)
	}

	// MoveTo：把第 2 行移到第 1 行之前
	if err := cs.MoveTo(2, 1); err != nil {
		t.Fatalf("MoveTo 失败: %v", err)
	}
	entries, _ = cs.Parse()
	if entries[0].Name != "节点A改" || entries[1].Name != "节点B" {
		t.Errorf("MoveTo 后顺序不符: %+v", entries)
	}

	// BatchDelete：第 1 行成功、越界行计失败
	success, failed := cs.BatchDelete([]int{1, 99})
	if success != 1 || failed != 1 {
		t.Errorf("BatchDelete = (%d,%d), 期望 (1,1)", success, failed)
	}
	entries, _ = cs.Parse()
	if len(entries) != 1 || entries[0].Name != "节点B" {
		t.Fatalf("BatchDelete 后内容不符: %+v", entries)
	}

	// DeleteVisible
	if err := cs.DeleteVisible(1); err != nil {
		t.Fatalf("DeleteVisible 失败: %v", err)
	}
	entries, _ = cs.Parse()
	if len(entries) != 0 {
		t.Fatalf("DeleteVisible 后应为空: %+v", entries)
	}

	// 越界错误
	if err := cs.DeleteVisible(5); err != ErrOutOfRange {
		t.Errorf("DeleteVisible(5) = %v, 期望 ErrOutOfRange", err)
	}

	// 末尾换行归一化：删除后文件不应残留多余空行
	data, err := os.ReadFile(vlessPath)
	if err != nil {
		t.Fatalf("读取 vless.txt 失败: %v", err)
	}
	if string(data) != "" {
		t.Errorf("全删后文件应为空, 实际 %q", string(data))
	}
}

// TestConfigStoreReloadVsReads 并发回归：Reload 热替换路径/变量表
// 与 Parse/FilePath/Variables/FindDuplicate/Add 并发执行不得产生数据竞争。
// 该用例需在 go test -race 下运行方有意义。
func TestConfigStoreReloadVsReads(t *testing.T) {
	cs, vlessPathA, nrtPathA := newTestConfigStore(t)
	if err := os.WriteFile(vlessPathA, []byte("1.1.1.1#A\n2.2.2.2#B\n"), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}

	dirB := t.TempDir()
	vlessPathB := filepath.Join(dirB, "vless_b.txt")
	nrtPathB := filepath.Join(dirB, "nrt_b.txt")
	if err := os.WriteFile(vlessPathB, []byte("3.3.3.3#C\n"), 0o644); err != nil {
		t.Fatalf("写入 vless_b.txt 失败: %v", err)
	}

	const workers = 8
	const iters = 150
	var wg sync.WaitGroup
	wg.Add(workers)

	go func() { // Reload 写端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				cs.Reload(vlessPathB, nrtPathB, map[string]config.VariableSource{
					"R": {Key: "R", SourceType: "inline", Value: "r"},
				})
			} else {
				cs.Reload(vlessPathA, nrtPathA, nil)
			}
		}
	}()
	go func() { // Parse 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := cs.Parse(); err != nil {
				t.Errorf("Parse: %v", err)
				return
			}
		}
	}()
	go func() { // 元信息读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cs.FilePath()
			_ = cs.Variables()
		}
	}()
	go func() { // FindDuplicate 读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_, _ = cs.FindDuplicate("1.1.1.1", "")
		}
	}()
	go func() { // Add 写端（对当前生效文件追加）
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if err := cs.Add("9.9.9.9", "临时", ""); err != nil {
				t.Errorf("Add: %v", err)
				return
			}
		}
	}()
	go func() { // 删除读端
		defer wg.Done()
		for i := 0; i < iters; i++ {
			entries, err := cs.Parse()
			if err != nil {
				t.Errorf("Parse: %v", err)
				return
			}
			if len(entries) > 0 {
				if err := cs.DeleteVisible(1); err != nil && err != ErrOutOfRange {
					t.Errorf("DeleteVisible: %v", err)
					return
				}
			}
		}
	}()
	// 剩余 2 个协程：混合读写
	for w := 0; w < 2; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _ = cs.FindDuplicate("", "临时")
				_ = cs.FilePath()
				entries, err := cs.Parse()
				if err != nil {
					t.Errorf("Parse: %v", err)
					return
				}
				_ = entries
			}
		}()
	}
	wg.Wait()
}

// TestSwapVisibleLastLineNoTrailingNewline 回归：末行无换行符时交换不得粘连两行。
// Add/删除路径写入的文件末行可能不带换行，交换后原末行落到中间需要补分隔符。
func TestSwapVisibleLastLineNoTrailingNewline(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)
	if err := os.WriteFile(vlessPath, []byte("1.1.1.1#A\n2.2.2.2#B"), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}
	if err := cs.SwapVisible(1, 2); err != nil {
		t.Fatalf("SwapVisible 失败: %v", err)
	}
	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "B" || entries[1].Name != "A" {
		t.Errorf("交换后应为 [B, A] 两个独立节点, 实际: %+v", entries)
	}
}

// TestMoveToFromLastLine 回归：末行（无换行）移到首行不得与后续行粘连。
func TestMoveToFromLastLine(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)
	if err := os.WriteFile(vlessPath, []byte("1.1.1.1#A\n2.2.2.2#B\n3.3.3.3#C"), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}
	if err := cs.MoveTo(3, 1); err != nil {
		t.Fatalf("MoveTo 失败: %v", err)
	}
	entries, err := cs.Parse()
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("移动后应仍为 3 个节点, 实际 %d: %+v", len(entries), entries)
	}
	want := []string{"C", "A", "B"}
	for i, w := range want {
		if entries[i].Name != w {
			t.Errorf("entries[%d].Name = %q, 期望 %q", i, entries[i].Name, w)
		}
	}
}

// TestConfigStoreUpdateVisibleOutOfRange 越界更新应返回 ErrOutOfRange。
func TestConfigStoreUpdateVisibleOutOfRange(t *testing.T) {
	cs, vlessPath, _ := newTestConfigStore(t)
	if err := os.WriteFile(vlessPath, []byte("1.1.1.1#A\n"), 0o644); err != nil {
		t.Fatalf("写入 vless.txt 失败: %v", err)
	}
	empty := ""
	if err := cs.UpdateVisible(0, &empty, &empty, &empty); err != ErrOutOfRange {
		t.Errorf("UpdateVisible(0) = %v, 期望 ErrOutOfRange", err)
	}
	if err := cs.UpdateVisible(9, &empty, &empty, &empty); err != ErrOutOfRange {
		t.Errorf("UpdateVisible(9) = %v, 期望 ErrOutOfRange", err)
	}
}
