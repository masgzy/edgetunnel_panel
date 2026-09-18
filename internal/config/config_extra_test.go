// config_extra_test.go - 新增可选配置节的行为测试：
// auth.web_password（双口令拆分）、nodes.bare_ip_role、flag（国旗开关）、proxyip（分区域兜底）。
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawConfig 直接写入给定内容的最小 config.yml（五个必需顶层节齐备）。
func writeRawConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const baseConfig = `app:
  host: 127.0.0.1
  port: 5001
  debug: false
remote:
  control_domain: example.com
  request_timeout: 15
  subscription_port: 8443
auth:
  login_password: pw
files:
  vless_file: data/vless.txt
subscriptions:
  default_profile: main
  profiles:
    main:
      name: main
      format: edt
      domain: sub.example.com
      uuid_mode: static
      uuid: 00000000-0000-0000-0000-000000000000
data_sources:
  by_id:
    "1":
      name: main
      kind: vless_file
      path: data/vless.txt
`

// injectAuth 在 baseConfig 的 auth 节内插入额外行（auth 级字段的正确注入点）。
func injectAuth(extra string) string {
	return strings.ReplaceAll(baseConfig, "  login_password: pw\n", "  login_password: pw\n"+extra)
}

// TestWebPasswordFallback Web 控制台口令的三级来源：
// 显式 web_password > 环境变量 > 回退 login_password；显式空串表示关闭鉴权。
func TestWebPasswordFallback(t *testing.T) {
	t.Run("缺省回退login_password", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.WebPassword != "pw" {
			t.Errorf("WebPassword = %q, 期望回退为 login_password %q", cfg.WebPassword, "pw")
		}
	})

	t.Run("显式设置独立口令", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, injectAuth("  web_password: web-secret\n")))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.WebPassword != "web-secret" {
			t.Errorf("WebPassword = %q, 期望 web-secret", cfg.WebPassword)
		}
		if cfg.LoginPassword != "pw" {
			t.Errorf("LoginPassword 不应被影响: %q", cfg.LoginPassword)
		}
	})

	t.Run("环境变量优先", func(t *testing.T) {
		t.Setenv("EDT_WEB_PASSWORD", "env-secret")
		cfg, err := Load(writeRawConfig(t, injectAuth("  web_password: web-secret\n")))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.WebPassword != "env-secret" {
			t.Errorf("WebPassword = %q, 期望 env-secret", cfg.WebPassword)
		}
	})

	t.Run("显式空串关闭鉴权", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, injectAuth("  web_password: \"\"\n")))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.WebPassword != "" {
			t.Errorf("WebPassword = %q, 期望空串（关闭控制台鉴权）", cfg.WebPassword)
		}
	})
}

// TestNodesSection bare_ip_role 的合法值与非法值降级。
func TestNodesSection(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"缺省proxyip", baseConfig, "proxyip"},
		{"显式yxip", baseConfig + "nodes:\n  bare_ip_role: yxip\n", "yxip"},
		{"大写归一化", baseConfig + "nodes:\n  bare_ip_role: YXIP\n", "yxip"},
		{"非法值降级", baseConfig + "nodes:\n  bare_ip_role: whatever\n", "proxyip"},
		{"空节缺省", baseConfig + "nodes:\n", "proxyip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(writeRawConfig(t, c.body))
			if err != nil {
				t.Fatalf("加载失败: %v", err)
			}
			if cfg.BareIPRole != c.want {
				t.Errorf("BareIPRole = %q, 期望 %q", cfg.BareIPRole, c.want)
			}
		})
	}
}

// TestFlagSection 国旗补全开关：缺省 iata 开 / iso2 关；显式值覆盖。
func TestFlagSection(t *testing.T) {
	t.Run("缺省iata开iso2关", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if !cfg.FlagIATA || cfg.FlagISO2 {
			t.Errorf("FlagIATA=%v FlagISO2=%v, 期望 true/false", cfg.FlagIATA, cfg.FlagISO2)
		}
	})
	t.Run("全开", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig+"flag:\n  iata: true\n  iso2: true\n"))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if !cfg.FlagIATA || !cfg.FlagISO2 {
			t.Errorf("FlagIATA=%v FlagISO2=%v, 期望 true/true", cfg.FlagIATA, cfg.FlagISO2)
		}
	})
	t.Run("全关", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig+"flag:\n  iata: false\n  iso2: false\n"))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.FlagIATA || cfg.FlagISO2 {
			t.Errorf("FlagIATA=%v FlagISO2=%v, 期望 false/false", cfg.FlagIATA, cfg.FlagISO2)
		}
	})
}

// TestProxyIPSection 分区域兜底配置解析：global/detect/by_region 与键归一化。
func TestProxyIPSection(t *testing.T) {
	t.Run("完整解析", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig+`proxyip:
  global: 9.9.9.9
  detect: true
  by_region:
    hkg: 1.2.3.4
    NRT: "5.6.7.8"
`))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.ProxyIPGlobal != "9.9.9.9" {
			t.Errorf("ProxyIPGlobal = %q", cfg.ProxyIPGlobal)
		}
		if !cfg.ProxyIPDetect {
			t.Error("ProxyIPDetect = false, 期望 true")
		}
		if v := cfg.ProxyIPByRegion["HKG"]; v != "1.2.3.4" {
			t.Errorf("by_region[hkg] 键应归一化为 HKG, 得到 %q", v)
		}
		if v := cfg.ProxyIPByRegion["NRT"]; v != "5.6.7.8" {
			t.Errorf("by_region[NRT] = %q", v)
		}
	})
	t.Run("节缺失零值", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if cfg.ProxyIPGlobal != "" || cfg.ProxyIPDetect || len(cfg.ProxyIPByRegion) != 0 {
			t.Errorf("未配置 proxyip 节应保持零值: %+v", cfg)
		}
	})
	t.Run("空值条目剔除", func(t *testing.T) {
		cfg, err := Load(writeRawConfig(t, baseConfig+`proxyip:
  by_region:
    HKG: ""
    NRT: 5.6.7.8
`))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if _, ok := cfg.ProxyIPByRegion["HKG"]; ok {
			t.Error("空值条目应被剔除")
		}
		if cfg.ProxyIPByRegion["NRT"] != "5.6.7.8" {
			t.Errorf("by_region[NRT] = %q", cfg.ProxyIPByRegion["NRT"])
		}
	})
}
