// flag_code_test.go - 区域码识别与按码补旗的单元测试。
// 重点约定：TW 区域码（TW/TPE/TWN 等）一律映射为中国旗帜 🇨🇳。
package module

import (
	"strings"
	"testing"
)

func TestLetterTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"HKG-01", []string{"HKG"}},
		{"美国 LAX 02", []string{"LAX"}},
		{"hk us", []string{"HK", "US"}},
		{"busy INDIA", []string{"BUSY", "INDIA"}}, // 子串不拆分
		{"HKG01", []string{"HKG"}},                // 数字也是分隔
		{"JP🇭🇰 x", []string{"JP", "X"}},
		{"", nil},
		{"中文-only", []string{"ONLY"}},
	}
	for _, c := range cases {
		got := LetterTokens(c.in)
		if len(got) != len(c.want) {
			t.Errorf("LetterTokens(%q) = %v, 期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("LetterTokens(%q)[%d] = %q, 期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestFlagForCode(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"HKG", "🇭🇰"},
		{"hkg", "🇭🇰"}, // 大小写归一
		{"ICN", "🇰🇷"},
		{"LAX", "🇺🇸"},
		{"SJC", "🇺🇸"},
		{"US", "🇺🇸"},
		{"UK", "🇬🇧"}, // 别名归一 GB
		{"GB", "🇬🇧"},
		// TW 区域码按约定一律使用中国旗帜
		{"TW", "🇨🇳"},
		{"TPE", "🇨🇳"},
		{"KHH", "🇨🇳"},
		{"TWN", "🇨🇳"},
		{"CN", "🇨🇳"},
		{"CHN", "🇨🇳"},
		{"XYZ", ""},  // 未知码
		{"ABCD", ""}, // 长度不符
		{"", ""},
	}
	for _, c := range cases {
		if got := FlagForCode(c.code); got != c.want {
			t.Errorf("FlagForCode(%q) = %q, 期望 %q", c.code, got, c.want)
		}
	}
}

func TestAddFlagByName(t *testing.T) {
	optIATA := FlagOptions{IATA: true}
	optBoth := FlagOptions{IATA: true, ISO2: true}
	optISO2 := FlagOptions{ISO2: true}

	cases := []struct {
		name string
		in   string
		opt  FlagOptions
		want string
	}{
		{"三字码前插旗", "HKG-01", optIATA, "🇭🇰 HKG-01"},
		{"机场码多种", "ICN 02", optIATA, "🇰🇷 ICN 02"},
		{"美国双机场", "美国 LAX", optIATA, "美国 🇺🇸 LAX"},
		{"TW码用中国旗", "TPE-03", optIATA, "🇨🇳 TPE-03"},
		{"二字码默认关", "US 01", optIATA, "US 01"},
		{"二字码开启", "US 01", optBoth, "🇺🇸 US 01"},
		{"二字码UK", "UK-1", optISO2, "🇬🇧 UK-1"},
		{"二字码TW用中国旗", "TW node", optISO2, "🇨🇳 TW node"},
		{"已有旗帜不重复", "🇭🇰 HKG", optBoth, "🇭🇰 HKG"},
		{"已有其他旗帜也不加", "🇯🇵 NRT HKG", optBoth, "🇯🇵 NRT HKG"},
		{"未知码不加", "XYZ-9", optBoth, "XYZ-9"},
		{"全开关关闭", "HKG", FlagOptions{}, "HKG"},
		{"空串", "", optBoth, ""},
		{"子串不误匹配", "busy", optISO2, "busy"},
		{"首个命中优先", "NRT 侧重 LAX", optIATA, "🇯🇵 NRT 侧重 LAX"},
	}
	for _, c := range cases {
		if got := AddFlagByName(c.in, c.opt); got != c.want {
			t.Errorf("%s: AddFlagByName(%q) = %q, 期望 %q", c.name, c.in, got, c.want)
		}
	}
}

func TestFindRegionCode(t *testing.T) {
	optBoth := FlagOptions{IATA: true, ISO2: true}
	if got := FindRegionCode("优选 HKG-01", optBoth); got != "HKG" {
		t.Errorf("FindRegionCode = %q, 期望 HKG", got)
	}
	if got := FindRegionCode("us node", FlagOptions{ISO2: true}); got != "US" {
		t.Errorf("FindRegionCode(小写) = %q, 期望 US", got)
	}
	if got := FindRegionCode("无码节点", optBoth); got != "" {
		t.Errorf("无码应返回空, 得到 %q", got)
	}
}

func TestAddFlagEmojiTwReplacement(t *testing.T) {
	// 输入文本已含 TW 旗帜：应整体替换为中国旗帜，且标注为「中国台湾」
	in := "节点 " + twFlag + " 01"
	out := AddFlagEmoji(in)
	if strings.Contains(out, twFlag) {
		t.Errorf("输出不应再含 TW 旗帜: %q", out)
	}
	if !strings.Contains(out, cnFlag) {
		t.Errorf("输出应含中国旗帜: %q", out)
	}
	if !strings.Contains(out, "中国台湾") {
		t.Errorf("输出应标注中国台湾: %q", out)
	}
}

func TestStripPort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.2.3.4", "1.2.3.4"},
		{"1.2.3.4:8443", "1.2.3.4"},
		{"example.com", "example.com"},
		{"example.com:443", "example.com"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"}, // 裸 IPv6 保持原样
		{"", ""},
	}
	for _, c := range cases {
		if got := stripPort(c.in); got != c.want {
			t.Errorf("stripPort(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
