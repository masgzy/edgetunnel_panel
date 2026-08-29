package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig 在临时目录写入最小可用的 config.yml（可选字段按参数注入）。
func writeTempConfig(t *testing.T, extraRemote, extraAuth string) string {
	t.Helper()
	dir := t.TempDir()
	content := `app:
  host: 127.0.0.1
  port: 5001
  debug: false
remote:
  control_domain: example.com
` + extraRemote + `  request_timeout: 15
  subscription_port: 8443
auth:
` + extraAuth + `  login_password: pw
files:
  vless_file: data/vless.txt
  result_file: data/result.csv
  run_time_file: data/run_time.txt
  auth_cache_file: data/auth.txt
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
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDerivedURLs admin_url / login_url 缺省时由 control_domain（base）派生；
// 显式配置仍然覆盖派生值。
func TestDerivedURLs(t *testing.T) {
	// 场景1：全部省略 → 默认路径拼接
	dir := writeTempConfig(t, "", "")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.AdminURL != "https://example.com/admin/config.json" {
		t.Errorf("admin_url 派生错误: %s", cfg.AdminURL)
	}
	if cfg.LoginURL != "https://example.com/login" {
		t.Errorf("login_url 派生错误: %s", cfg.LoginURL)
	}
	if cfg.UserinfoExpire != "2030-01-01 00:00:00+08:00" {
		t.Errorf("userinfo_expire 缺省错误: %s", cfg.UserinfoExpire)
	}

	// 场景2：显式覆盖仍然生效
	dir = writeTempConfig(t, "  admin_url: https://ctl.example.net/custom/admin.json\n",
		"  login_url: https://ctl.example.net/signin\n")
	cfg, err = Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.AdminURL != "https://ctl.example.net/custom/admin.json" {
		t.Errorf("admin_url 显式覆盖失败: %s", cfg.AdminURL)
	}
	if cfg.LoginURL != "https://ctl.example.net/signin" {
		t.Errorf("login_url 显式覆盖失败: %s", cfg.LoginURL)
	}

	// 场景3：control_domain 带 scheme/路径也能归一化拼接
	dir = writeTempConfig(t, "", "")
	data, _ := os.ReadFile(filepath.Join(dir, "config.yml"))
	_ = os.WriteFile(filepath.Join(dir, "config.yml"),
		[]byte(strings.ReplaceAll(string(data), "control_domain: example.com", `control_domain: "https://example.com/"`)), 0o644)
	cfg, err = Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.AdminURL != "https://example.com/admin/config.json" {
		t.Errorf("scheme 归一化失败: %s", cfg.AdminURL)
	}
}

// TestLoadFrom 带文件名的校验入口与 Load 行为一致。
func TestLoadFrom(t *testing.T) {
	dir := writeTempConfig(t, "", "")
	if _, err := LoadFrom(dir, "config.yml"); err != nil {
		t.Fatalf("LoadFrom 校验失败: %v", err)
	}
	if _, err := LoadFrom(dir, "not-exist.yml"); err == nil {
		t.Fatal("LoadFrom 对缺失文件应返回错误")
	}
}
