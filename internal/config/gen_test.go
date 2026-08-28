package config

import "testing"

// TestRenderBasePath 面板反代路径基座渲染（对齐生态完整节点路径规则）。
func TestRenderBasePath(t *testing.T) {
	tpl := "proxyip=" + ProxyIPPlaceholder
	cases := []struct {
		name      string
		pp        GenProxyPath
		nodeIP    string
		wantPath  string
		wantQuery string
	}{
		{"默认面板_auto_条目IP", GenProxyPath{"auto", "/", tpl}, "1.2.3.4", "/proxyip=1.2.3.4", ""},
		{"默认面板_auto_条目空", GenProxyPath{"auto", "/", tpl}, "", "/", ""},
		{"默认面板_auto_条目DIRECT", GenProxyPath{"auto", "/", tpl}, "DIRECT", "/", ""},
		{"自定义PATH前缀", GenProxyPath{"auto", "/sub", tpl}, "1.2.3.4", "/sub/proxyip=1.2.3.4", ""},
		{"自定义模板", GenProxyPath{"auto", "/", "pyip=" + ProxyIPPlaceholder}, "1.2.3.4", "/pyip=1.2.3.4", ""},
		{"模板含查询", GenProxyPath{"auto", "/", "proxyip=" + ProxyIPPlaceholder + "?x=1"}, "1.2.3.4", "/proxyip=1.2.3.4", "x=1"},
		{"PATH自带查询", GenProxyPath{"auto", "/p?ed=2048", tpl}, "", "/p", "ed=2048"},
		{"PATH自带查询_条目IP", GenProxyPath{"auto", "/p?ed=2048", tpl}, "1.2.3.4", "/p/proxyip=1.2.3.4", "ed=2048"},
		{"面板具体IP兜底", GenProxyPath{"5.6.7.8", "/", tpl}, "", "/proxyip=5.6.7.8", ""},
		{"面板具体IP_条目DIRECT", GenProxyPath{"5.6.7.8", "/", tpl}, "DIRECT", "/proxyip=5.6.7.8", ""},
		{"条目IP优先于面板", GenProxyPath{"5.6.7.8", "/", tpl}, "1.2.3.4", "/proxyip=1.2.3.4", ""},
		{"前缀无斜杠", GenProxyPath{"auto", "sub/", tpl}, "1.2.3.4", "/sub/proxyip=1.2.3.4", ""},
		{"前缀根值", GenProxyPath{"auto", "/", tpl}, "1.2.3.4", "/proxyip=1.2.3.4", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pp := c.pp
			gotPath, gotQuery := pp.RenderBasePath(c.nodeIP)
			if gotPath != c.wantPath || gotQuery != c.wantQuery {
				t.Fatalf("RenderBasePath(%q) = (%q, %q), want (%q, %q)",
					c.nodeIP, gotPath, gotQuery, c.wantPath, c.wantQuery)
			}
		})
	}
}

// TestTransportPathFallback 面板快照缺省时回落历史硬编码行为（v1.1.0 一致）。
func TestTransportPathFallback(t *testing.T) {
	g := DefaultGenSettings() // vless + ws + 0RTT
	if got := g.TransportPath("edt", "1.2.3.4"); got != "/proxyip=1.2.3.4?ed=2560" {
		t.Fatalf("edt fallback = %q", got)
	}
	if got := g.TransportPath("snippets", "1.2.3.4"); got != "/snippets/ip=1.2.3.4?ed=2560" {
		t.Fatalf("snippets fallback = %q", got)
	}
	if got := g.TransportPath("edt", ""); got != "/?ed=2560" {
		t.Fatalf("empty ip fallback = %q", got)
	}
}

// TestTransportPathPanel 面板反代路径快照参与路径组装。
func TestTransportPathPanel(t *testing.T) {
	tpl := "proxyip=" + ProxyIPPlaceholder

	// 默认面板（PATH="/" + auto）与回落行为一致：零变化。
	base := DefaultGenSettings()
	base.ProxyPath = &GenProxyPath{"auto", "/", tpl}
	if got := base.TransportPath("edt", "1.2.3.4"); got != "/proxyip=1.2.3.4?ed=2560" {
		t.Fatalf("panel default = %q", got)
	}

	// 自定义 PATH 前缀生效。
	prefixed := DefaultGenSettings()
	prefixed.ProxyPath = &GenProxyPath{"auto", "/sub", tpl}
	if got := prefixed.TransportPath("edt", "1.2.3.4"); got != "/sub/proxyip=1.2.3.4?ed=2560" {
		t.Fatalf("panel prefix = %q", got)
	}

	// 自定义模板生效。
	custom := DefaultGenSettings()
	custom.ProxyPath = &GenProxyPath{"auto", "/", "pyip=" + ProxyIPPlaceholder}
	if got := custom.TransportPath("edt", "1.2.3.4"); got != "/pyip=1.2.3.4?ed=2560" {
		t.Fatalf("panel template = %q", got)
	}

	// SS：enc 参数与面板查询片段并存。
	ss := DefaultGenSettings()
	ss.Protocol = "ss"
	ss.ProxyPath = &GenProxyPath{"5.6.7.8", "/", tpl}
	if got := ss.TransportPath("edt", ""); got != "/proxyip=5.6.7.8?enc=aes-128-gcm&ed=2560" {
		t.Fatalf("panel ss = %q", got)
	}

	// 0RTT 关闭时不追加 ed。
	noRTT := DefaultGenSettings()
	noRTT.Enable0RTT = false
	noRTT.ProxyPath = &GenProxyPath{"auto", "/sub", tpl}
	if got := noRTT.TransportPath("edt", "1.2.3.4"); got != "/sub/proxyip=1.2.3.4" {
		t.Fatalf("panel no rtt = %q", got)
	}
}
