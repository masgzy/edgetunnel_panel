// Package config 负责加载与校验 config.yml。
// 对应原 Python 项目的 edt_app/config.py。
//
// 解析流程（见 Load）：
//  1. ensureConfigFile  —— 定位 config.yml，缺失时从 config.example.yml 初始化；
//  2. readConfigYAML    —— 读取并反序列化为顶层映射；
//  3. requireTopSections—— 校验五个必需顶层节（app/remote/auth/files/subscriptions）；
//  4. parseProfiles / parseDataSources / parseVariables —— 领域节解析；
//  5. parseAppSection / parseRemoteSection / parseAuthSection —— 标量配置节；
//  6. applySubConverterSettings —— 可选的 subconverter 桥接节（缺省安全降级）；
//  7. applyNodesSection / applyFlagSection / applyProxyIPSection —— 可选行为节
//     （节点解析角色 / 国旗补全 / 全局与分区域 ProxyIP，缺省安全降级）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SubscriptionProfile 订阅模板定义。
type SubscriptionProfile struct {
	Key      string
	Name     string
	Aliases  []string
	Format   string // edt | snippets
	Domain   string
	UUIDMode string // dynamic | static
	UUID     string // static 模式下的固定 UUID
}

// DataSourceConfig 数据源配置。
type DataSourceConfig struct {
	ID   string
	Name string
	Kind string // vless_file | json_file | preferred_result
	Path string // 相对 root 解析后的绝对路径（preferred_result 为空）
}

// VariableSource 变量来源。
type VariableSource struct {
	Key        string
	SourceType string // file | inline
	Value      string // inline 时的内容
	Path       string // file 时的绝对路径
}

// RuntimeConfig 运行时配置。
type RuntimeConfig struct {
	RootDir          string
	Host             string
	Port             int
	Debug            bool
	ControlDomain    string
	AdminURL         string
	RequestTimeout   int
	SubscriptionPort int
	UUIDCacheTTL     int // uuid 缓存有效期（秒；remote.uuid_cache_ttl，<=0 用缺省 86400）

	LoginURL       string
	LoginPassword  string // 远程控制端（EDT Worker 面板）登录口令
	WebPassword    string // 本面板 Web 控制台登录口令（缺省回退 LoginPassword）
	UserinfoExpire string

	// 节点解析行为（nodes 节，可选）
	BareIPRole string // vless.txt 无 @ 行的裸 IP 角色：proxyip（默认）| yxip

	// 国旗补全（flag 节，可选）
	FlagIATA bool // 名称中三字码（HKG/ICN/LAX…）补国旗；缺省 true
	FlagISO2 bool // 名称中二字码（HK/US…）补国旗；缺省 false

	// ProxyIP 解析（proxyip 节，可选）：节点未显式携带 proxyip 时的兜底来源
	ProxyIPGlobal   string            // 全局默认 ProxyIP
	ProxyIPDetect   bool              // 名称无区域码时是否经 cdn-cgi/trace 探测 cfcolo
	ProxyIPByRegion map[string]string // 按三字码（cfcolo/机场码）指定 ProxyIP

	VlessFile     string
	ResultFile    string
	RunTimeFile   string
	AuthCacheFile string
	NRTFile       string
	PreIPFile     string

	Profiles                 map[string]SubscriptionProfile
	DefaultProfile           string
	DefaultProfileByID       map[string]string
	EncodeSubscriptionBase64 bool
	DataSources              map[string]DataSourceConfig
	Variables                map[string]VariableSource

	// subconverter 桥接配置
	SubConverterMode   string // off | local | remote
	SubConverterRemote string
	SubConverterBin    string // 本地 subconverter 二进制路径
	SubConverterPort   int

	// 生成配置（gen 节）：订阅链接的协议/传输/证书/0RTT/分片等参数来源。
	// GenAutoFromPanel=true 且面板可用时使用面板值，否则用 GenManual。
	GenAutoFromPanel      bool        // 自动获取配置（协议，设置）；缺省 true
	GenAggregateWorkerSub bool        // 聚合面板(Worker)原生订阅到订阅输出；缺省 false
	GenManual             GenSettings // 手动模式默认值（已归一化）
}

// topSections config.yml 的五个必需顶层节。
type topSections struct {
	app           map[string]interface{}
	remote        map[string]interface{}
	auth          map[string]interface{}
	files         map[string]interface{}
	subscriptions map[string]interface{}
}

// Load 加载并解析 config.yml，返回填好的 RuntimeConfig。
// 任一环节校验失败都会返回带路径前缀的错误，不产出半成品配置。
func Load(rootDir string) (*RuntimeConfig, error) {
	if rootDir == "" {
		// 二进制位于 edt/ 下、配置在 edt/ 根，默认以工作目录为根。
		rootDir = "."
	}
	rootDir, _ = filepath.Abs(rootDir)

	if err := ensureConfigFile(rootDir); err != nil {
		return nil, err
	}
	return LoadFrom(rootDir, "config.yml")
}

// LoadFrom 从 rootDir 下指定文件名加载配置（语义与 Load 一致，但：
// 不触发缺失初始化，供面板「配置文件」保存前的完整校验复用——
// 校验通过即代表正式写盘后 Load 必然成功）。
func LoadFrom(rootDir, filename string) (*RuntimeConfig, error) {
	if rootDir == "" {
		rootDir = "."
	}
	rootDir, _ = filepath.Abs(rootDir)

	data, err := readConfigYAML(rootDir, filename)
	if err != nil {
		return nil, err
	}

	sections, err := requireTopSections(data)
	if err != nil {
		return nil, err
	}

	profiles, defaultProfile, defaultByID, encodeB64, err := parseProfiles(sections.subscriptions)
	if err != nil {
		return nil, err
	}
	dataSources, err := parseDataSources(rootDir, data)
	if err != nil {
		return nil, err
	}
	variables, err := parseVariables(rootDir, data)
	if err != nil {
		return nil, err
	}

	host, port, debug, err := parseAppSection(sections.app)
	if err != nil {
		return nil, err
	}
	controlDomain, adminURL, timeout, subPort, uuidTTL, err := parseRemoteSection(sections.remote)
	if err != nil {
		return nil, err
	}
	loginURL, loginPassword, userinfoExpire, err := parseAuthSection(sections.auth, controlDomain)
	if err != nil {
		return nil, err
	}

	cfg := &RuntimeConfig{
		RootDir:                  rootDir,
		Host:                     host,
		Port:                     port,
		Debug:                    debug,
		ControlDomain:            controlDomain,
		AdminURL:                 adminURL,
		RequestTimeout:           timeout,
		SubscriptionPort:         subPort,
		UUIDCacheTTL:             uuidTTL,
		LoginURL:                 loginURL,
		LoginPassword:            loginPassword,
		WebPassword:              resolveWebPassword(sections.auth, loginPassword),
		UserinfoExpire:           userinfoExpire,
		VlessFile:                resolvePath(rootDir, fileValue(sections.files, "vless_file", "data/vless.txt")),
		ResultFile:               resolvePath(rootDir, fileValue(sections.files, "result_file", "data/result.csv")),
		RunTimeFile:              resolvePath(rootDir, fileValue(sections.files, "run_time_file", "data/run_time.txt")),
		AuthCacheFile:            resolvePath(rootDir, fileValue(sections.files, "auth_cache_file", "data/auth.txt")),
		NRTFile:                  filepath.Join(rootDir, "NRT.txt"),
		PreIPFile:                filepath.Join(rootDir, "data", "pre_ip.txt"),
		Profiles:                 profiles,
		DefaultProfile:           defaultProfile,
		DefaultProfileByID:       defaultByID,
		EncodeSubscriptionBase64: encodeB64,
		DataSources:              dataSources,
		Variables:                variables,
	}

	applySubConverterSettings(cfg, rootDir, data)
	applyGenSection(cfg, data)
	applyNodesSection(cfg, data)
	applyFlagSection(cfg, data)
	applyProxyIPSection(cfg, data)
	return cfg, nil
}

// ensureConfigFile 保证 config.yml 存在；缺失时尝试从 config.example.yml 初始化，
// 初始化成功同样返回错误以中断启动，提示用户先编辑配置。
func ensureConfigFile(rootDir string) error {
	configPath := filepath.Join(rootDir, "config.yml")
	if fileExists(configPath) {
		return nil
	}
	examplePath := filepath.Join(rootDir, "config.example.yml")
	if !fileExists(examplePath) {
		return fmt.Errorf("缺少配置文件: %s，同时也找不到模板文件: %s", configPath, examplePath)
	}
	if err := copyFile(examplePath, configPath); err != nil {
		return fmt.Errorf("初始化 config.yml 失败: %w", err)
	}
	return fmt.Errorf("检测到 config.yml 不存在，已根据 config.example.yml 初始化。请先编辑 config.yml 后再重新启动")
}

// readConfigYAML 读取并解析指定配置文件，顶层必须是对象映射。
func readConfigYAML(rootDir, filename string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(filepath.Join(rootDir, filename))
	if err != nil {
		return nil, fmt.Errorf("读取 config.yml 失败: %w", err)
	}
	var data map[string]interface{}
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("解析 config.yml 失败: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("config.yml 顶层必须是对象映射")
	}
	return data, nil
}

// requireTopSections 校验并取出五个必需顶层节。
func requireTopSections(data map[string]interface{}) (*topSections, error) {
	var err error
	s := &topSections{}
	if s.app, err = requireMapping(data, "app"); err != nil {
		return nil, err
	}
	if s.remote, err = requireMapping(data, "remote"); err != nil {
		return nil, err
	}
	if s.auth, err = requireMapping(data, "auth"); err != nil {
		return nil, err
	}
	if s.files, err = requireMapping(data, "files"); err != nil {
		return nil, err
	}
	if s.subscriptions, err = requireMapping(data, "subscriptions"); err != nil {
		return nil, err
	}
	return s, nil
}

// parseAppSection 解析 app 节：host / port / debug。
func parseAppSection(m map[string]interface{}) (host string, port int, debug bool, err error) {
	hostI, err := requireValue(m, "host", "app")
	if err != nil {
		return "", 0, false, err
	}
	portI, err := requireValue(m, "port", "app")
	if err != nil {
		return "", 0, false, err
	}
	port, err = asInt(portI, "app.port")
	if err != nil {
		return "", 0, false, err
	}
	debugI, err := requireValue(m, "debug", "app")
	if err != nil {
		return "", 0, false, err
	}
	debug, err = asBool(debugI, "app.debug")
	if err != nil {
		return "", 0, false, err
	}
	return fmt.Sprintf("%v", hostI), port, debug, nil
}

// parseRemoteSection 解析 remote 节：control_domain / admin_url / request_timeout / subscription_port。
// admin_url 可省略：默认由 control_domain（base）拼接为
// https://<control_domain>/admin/config.json；仅当控制端路径非默认时才需显式配置。
// uuid_cache_ttl 可省略：uuid 缓存有效期（秒），缺省 86400（24h）。
func parseRemoteSection(m map[string]interface{}) (controlDomain, adminURL string, timeout, subPort, uuidTTL int, err error) {
	controlDomainI, err := requireValue(m, "control_domain", "remote")
	if err != nil {
		return "", "", 0, 0, 0, err
	}
	controlDomain = strings.TrimSpace(fmt.Sprintf("%v", controlDomainI))
	if v, ok := m["admin_url"]; ok && v != nil {
		adminURL = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if adminURL == "" {
		adminURL = "https://" + normalizeBaseHost(controlDomain) + "/admin/config.json"
	}
	timeoutI, err := requireValue(m, "request_timeout", "remote")
	if err != nil {
		return "", "", 0, 0, 0, err
	}
	timeout, err = asInt(timeoutI, "remote.request_timeout")
	if err != nil {
		return "", "", 0, 0, 0, err
	}
	subPortI, err := requireValue(m, "subscription_port", "remote")
	if err != nil {
		return "", "", 0, 0, 0, err
	}
	subPort, err = asInt(subPortI, "remote.subscription_port")
	if err != nil {
		return "", "", 0, 0, 0, err
	}
	if v, ok := m["uuid_cache_ttl"]; ok && v != nil {
		if ttl, terr := asInt(v, "remote.uuid_cache_ttl"); terr == nil && ttl > 0 {
			uuidTTL = ttl
		}
	}
	return controlDomain, adminURL, timeout, subPort, uuidTTL, nil
}

// resolveWebPassword 计算本面板 Web 控制台口令：
// 优先级：环境变量 EDT_WEB_PASSWORD > auth.web_password > auth.login_password（兼容旧版单口令）。
// 显式配置空字符串（web_password: ""）表示关闭 Web 控制台鉴权。
func resolveWebPassword(m map[string]interface{}, loginPassword string) string {
	if v := os.Getenv("EDT_WEB_PASSWORD"); v != "" {
		return v
	}
	if v, ok := m["web_password"]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return loginPassword
}

// parseAuthSection 解析 auth 节；口令优先取环境变量 EDT_LOGIN_PASSWORD。
// login_url / userinfo_expire 均可省略：
//   - login_url 默认由 control_domain 拼接为 https://<control_domain>/login；
//   - userinfo_expire 默认 2030-01-01（客户端订阅页的到期展示）。
func parseAuthSection(m map[string]interface{}, controlDomain string) (loginURL, loginPassword, userinfoExpire string, err error) {
	if v, ok := m["login_url"]; ok && v != nil {
		loginURL = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if loginURL == "" {
		loginURL = "https://" + normalizeBaseHost(controlDomain) + "/login"
	}
	loginPasswordCfg, err := requireValue(m, "login_password", "auth")
	if err != nil {
		return "", "", "", err
	}
	loginPassword = os.Getenv("EDT_LOGIN_PASSWORD")
	if loginPassword == "" {
		loginPassword = fmt.Sprintf("%v", loginPasswordCfg)
	}
	if v, ok := m["userinfo_expire"]; ok && v != nil {
		userinfoExpire = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if userinfoExpire == "" {
		userinfoExpire = "2030-01-01 00:00:00+08:00"
	}
	return loginURL, loginPassword, userinfoExpire, nil
}

// normalizeBaseHost 允许 control_domain 携带 scheme 或路径（如 https://example.com/），
// 拼接默认地址前先归一化出裸主机名；避免拼出 https://https://x 这类坏 URL。
func normalizeBaseHost(domain string) string {
	domain = strings.TrimSpace(domain)
	if i := strings.Index(domain, "://"); i >= 0 {
		domain = domain[i+3:]
	}
	if i := strings.IndexAny(domain, "/?#"); i >= 0 {
		domain = domain[:i]
	}
	domain = strings.TrimSuffix(domain, ":")
	if i := strings.LastIndex(domain, "@"); i >= 0 { // 去掉可能的 user@ 前缀
		domain = domain[i+1:]
	}
	return domain
}

// applySubConverterSettings 应用可选的 subconverter 桥接节；
// 节缺失或字段非法时保持零值/默认（off），不视为致命错误。
func applySubConverterSettings(cfg *RuntimeConfig, rootDir string, data map[string]interface{}) {
	scRaw, ok := data["subconverter"]
	if !ok || scRaw == nil {
		return
	}
	scm, ok := scRaw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := scm["mode"]; ok {
		cfg.SubConverterMode = fmt.Sprintf("%v", v)
	}
	if v, ok := scm["remote"]; ok {
		cfg.SubConverterRemote = fmt.Sprintf("%v", v)
	}
	if v, ok := scm["bin"]; ok {
		cfg.SubConverterBin = resolvePath(rootDir, fmt.Sprintf("%v", v))
	}
	if v, ok := scm["port"]; ok {
		if p, err := asInt(v, "subconverter.port"); err == nil {
			cfg.SubConverterPort = p
		}
	}
}

// applyNodesSection 应用可选的 nodes 节（节点解析行为）。
// 节缺失时保持默认值（bare_ip_role=proxyip），字段非法时忽略该项维持缺省。
func applyNodesSection(cfg *RuntimeConfig, data map[string]interface{}) {
	cfg.BareIPRole = "proxyip"
	raw, ok := data["nodes"]
	if !ok || raw == nil {
		return
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := m["bare_ip_role"]; ok && v != nil {
		role := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
		if role == "proxyip" || role == "yxip" {
			cfg.BareIPRole = role
		}
	}
}

// applyFlagSection 应用可选的 flag 节（国旗补全开关）。
// 缺省：三字码开（机场码几乎无歧义）、二字码关（存在误匹配风险，需显式开启）。
func applyFlagSection(cfg *RuntimeConfig, data map[string]interface{}) {
	cfg.FlagIATA = true
	raw, ok := data["flag"]
	if !ok || raw == nil {
		return
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := m["iata"]; ok && v != nil {
		if b, err := asBool(v, "flag.iata"); err == nil {
			cfg.FlagIATA = b
		}
	}
	if v, ok := m["iso2"]; ok && v != nil {
		if b, err := asBool(v, "flag.iso2"); err == nil {
			cfg.FlagISO2 = b
		}
	}
}

// applyProxyIPSection 应用可选的 proxyip 节（全局/分区域 ProxyIP 兜底）。
// by_region 的键为三字码（cfcolo / 机场码），值可为 IP、域名或变量引用（数据文件侧解析）。
func applyProxyIPSection(cfg *RuntimeConfig, data map[string]interface{}) {
	raw, ok := data["proxyip"]
	if !ok || raw == nil {
		return
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := m["global"]; ok && v != nil {
		cfg.ProxyIPGlobal = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if v, ok := m["detect"]; ok && v != nil {
		if b, err := asBool(v, "proxyip.detect"); err == nil {
			cfg.ProxyIPDetect = b
		}
	}
	if rawMap, ok := m["by_region"]; ok && rawMap != nil {
		if regions, ok := rawMap.(map[string]interface{}); ok {
			byRegion := make(map[string]string, len(regions))
			for code, val := range regions {
				code = strings.ToUpper(strings.TrimSpace(code))
				if code == "" || val == nil {
					continue
				}
				v := strings.TrimSpace(fmt.Sprintf("%v", val))
				if v == "" {
					continue
				}
				byRegion[code] = v
			}
			if len(byRegion) > 0 {
				cfg.ProxyIPByRegion = byRegion
			}
		}
	}
}

// ----- profiles -----

// parseProfiles 解析 subscriptions 节：模板映射、默认模板、按订阅 ID 的默认模板与 Base64 开关。
func parseProfiles(subSection map[string]interface{}) (map[string]SubscriptionProfile, string, map[string]string, bool, error) {
	rawProfiles, err := requireMapping(subSection, "profiles")
	if err != nil {
		return nil, "", nil, false, err
	}
	defaultProfileI, err := requireValue(subSection, "default_profile", "subscriptions")
	if err != nil {
		return nil, "", nil, false, err
	}
	defaultProfile := fmt.Sprintf("%v", defaultProfileI)

	encodeB64, err := encodeBase64Setting(subSection)
	if err != nil {
		return nil, "", nil, false, err
	}
	defaultByID, err := parseDefaultsByID(subSection)
	if err != nil {
		return nil, "", nil, false, err
	}

	profiles := map[string]SubscriptionProfile{}
	for key, raw := range rawProfiles {
		p, err := parseProfileEntry(key, raw)
		if err != nil {
			return nil, "", nil, false, err
		}
		profiles[key] = p
	}

	if err := validateProfileRefs(profiles, defaultProfile, defaultByID); err != nil {
		return nil, "", nil, false, err
	}
	return profiles, defaultProfile, defaultByID, encodeB64, nil
}

// encodeBase64Setting 读取 subscriptions.encode_base64，缺省为 false。
func encodeBase64Setting(subSection map[string]interface{}) (bool, error) {
	raw, ok := subSection["encode_base64"]
	if !ok {
		return false, nil
	}
	return asBool(raw, "subscriptions.encode_base64")
}

// parseDefaultsByID 解析 subscriptions.defaults_by_id（订阅 ID → 模板 key）。
func parseDefaultsByID(subSection map[string]interface{}) (map[string]string, error) {
	defaultByID := map[string]string{}
	raw, ok := subSection["defaults_by_id"]
	if !ok || raw == nil {
		return defaultByID, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("subscriptions.defaults_by_id 必须是对象")
	}
	for k, v := range m {
		defaultByID[k] = fmt.Sprintf("%v", v)
	}
	return defaultByID, nil
}

// parseProfileEntry 解析单个订阅模板条目并校验 format / uuid_mode / uuid。
func parseProfileEntry(key string, raw interface{}) (SubscriptionProfile, error) {
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return SubscriptionProfile{}, fmt.Errorf("subscriptions.profiles.%s 必须是对象", key)
	}
	aliases, err := asStringSlice(rawMap["aliases"])
	if err != nil {
		return SubscriptionProfile{}, fmt.Errorf("subscriptions.profiles.%s.aliases %v", key, err)
	}

	formatI, err := requireValue(rawMap, "format", "subscriptions.profiles."+key)
	if err != nil {
		return SubscriptionProfile{}, err
	}
	formatName := fmt.Sprintf("%v", formatI)
	if formatName != "edt" && formatName != "snippets" {
		return SubscriptionProfile{}, fmt.Errorf("subscriptions.profiles.%s.format 只支持 edt/snippets", key)
	}

	uuidModeI, err := requireValue(rawMap, "uuid_mode", "subscriptions.profiles."+key)
	if err != nil {
		return SubscriptionProfile{}, err
	}
	uuidMode := fmt.Sprintf("%v", uuidModeI)
	if uuidMode != "dynamic" && uuidMode != "static" {
		return SubscriptionProfile{}, fmt.Errorf("subscriptions.profiles.%s.uuid_mode 只支持 dynamic/static", key)
	}

	nameI, err := requireValue(rawMap, "name", "subscriptions.profiles."+key)
	if err != nil {
		return SubscriptionProfile{}, err
	}
	domainI, err := requireValue(rawMap, "domain", "subscriptions.profiles."+key)
	if err != nil {
		return SubscriptionProfile{}, err
	}

	var uuid string
	if uuidMode == "static" {
		uuidI, err := requireValue(rawMap, "uuid", "subscriptions.profiles."+key)
		if err != nil {
			return SubscriptionProfile{}, err
		}
		uuid = fmt.Sprintf("%v", uuidI)
	}

	return SubscriptionProfile{
		Key:      key,
		Name:     fmt.Sprintf("%v", nameI),
		Aliases:  aliases,
		Format:   formatName,
		Domain:   fmt.Sprintf("%v", domainI),
		UUIDMode: uuidMode,
		UUID:     uuid,
	}, nil
}

// validateProfileRefs 校验默认模板与 defaults_by_id 引用的模板均存在。
func validateProfileRefs(profiles map[string]SubscriptionProfile, defaultProfile string, defaultByID map[string]string) error {
	if _, ok := profiles[defaultProfile]; !ok {
		return fmt.Errorf("subscriptions.default_profile 指向了不存在的 profile: %s", defaultProfile)
	}
	for subID, profileKey := range defaultByID {
		if _, ok := profiles[profileKey]; !ok {
			return fmt.Errorf("subscriptions.defaults_by_id.%s 指向了不存在的 profile: %s", subID, profileKey)
		}
	}
	return nil
}

// ----- data sources -----

// parseDataSources 解析 data_sources 节（按订阅 ID 组织的数据源映射）。
func parseDataSources(rootDir string, raw map[string]interface{}) (map[string]DataSourceConfig, error) {
	dsSection, err := requireMapping(raw, "data_sources")
	if err != nil {
		return nil, err
	}
	rawByID, err := requireMapping(dsSection, "by_id")
	if err != nil {
		return nil, err
	}
	result := map[string]DataSourceConfig{}
	for subID, source := range rawByID {
		ds, err := parseDataSourceEntry(rootDir, subID, source)
		if err != nil {
			return nil, err
		}
		result[subID] = ds
	}
	return result, nil
}

// parseDataSourceEntry 解析单个数据源条目；preferred_result 类型无 path。
func parseDataSourceEntry(rootDir, subID string, source interface{}) (DataSourceConfig, error) {
	src, ok := source.(map[string]interface{})
	if !ok {
		return DataSourceConfig{}, fmt.Errorf("data_sources.by_id.%s 必须是对象", subID)
	}
	kindI, err := requireValue(src, "kind", "data_sources.by_id."+subID)
	if err != nil {
		return DataSourceConfig{}, err
	}
	kind := fmt.Sprintf("%v", kindI)
	if kind != "vless_file" && kind != "json_file" && kind != "preferred_result" {
		return DataSourceConfig{}, fmt.Errorf("data_sources.by_id.%s.kind 只支持 vless_file/json_file/preferred_result", subID)
	}
	nameI, err := requireValue(src, "name", "data_sources.by_id."+subID)
	if err != nil {
		return DataSourceConfig{}, err
	}
	var path string
	if kind == "vless_file" || kind == "json_file" {
		pathI, err := requireValue(src, "path", "data_sources.by_id."+subID)
		if err != nil {
			return DataSourceConfig{}, err
		}
		path = resolvePath(rootDir, fmt.Sprintf("%v", pathI))
	}
	return DataSourceConfig{
		ID:   subID,
		Name: fmt.Sprintf("%v", nameI),
		Kind: kind,
		Path: path,
	}, nil
}

// ----- variables -----

// parseVariables 解析可选的 variables 节（模板变量来源映射），缺节时返回空映射。
func parseVariables(rootDir string, raw map[string]interface{}) (map[string]VariableSource, error) {
	result := map[string]VariableSource{}
	variablesSection, ok := raw["variables"]
	if !ok || variablesSection == nil {
		return result, nil
	}
	m, ok := variablesSection.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("config.yml 的 variables 必须是对象")
	}
	for key, src := range m {
		vs, err := parseVariableEntry(rootDir, key, src)
		if err != nil {
			return nil, err
		}
		result[key] = vs
	}
	return result, nil
}

// parseVariableEntry 解析单个变量来源；file 类型取 path，inline 类型取 content。
func parseVariableEntry(rootDir, key string, src interface{}) (VariableSource, error) {
	srcMap, ok := src.(map[string]interface{})
	if !ok {
		return VariableSource{}, fmt.Errorf("variables.%s 必须是对象", key)
	}
	sourceTypeI, err := requireValue(srcMap, "type", "variables."+key)
	if err != nil {
		return VariableSource{}, err
	}
	sourceType := fmt.Sprintf("%v", sourceTypeI)
	if sourceType != "file" && sourceType != "inline" {
		return VariableSource{}, fmt.Errorf("variables.%s.type 只支持 file/inline", key)
	}
	vs := VariableSource{Key: key, SourceType: sourceType}
	if sourceType == "file" {
		pathI, err := requireValue(srcMap, "path", "variables."+key)
		if err != nil {
			return VariableSource{}, err
		}
		vs.Path = resolvePath(rootDir, fmt.Sprintf("%v", pathI))
	} else {
		valueI, err := requireValue(srcMap, "content", "variables."+key)
		if err != nil {
			return VariableSource{}, err
		}
		vs.Value = fmt.Sprintf("%v", valueI)
	}
	return vs, nil
}

// ----- 内部辅助 -----

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func requireMapping(data map[string]interface{}, key string) (map[string]interface{}, error) {
	v, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("config.yml 缺少对象配置: %s", key)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("config.yml 的 %s 必须是对象", key)
	}
	return m, nil
}

func requireValue(section map[string]interface{}, key, sectionName string) (interface{}, error) {
	v, ok := section[key]
	if !ok {
		return nil, fmt.Errorf("config.yml 缺少配置项: %s.%s", sectionName, key)
	}
	if v == nil {
		return nil, fmt.Errorf("config.yml 配置项不能为空: %s.%s", sectionName, key)
	}
	return v, nil
}

func resolvePath(rootDir, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(rootDir, value)
}

// fileValue 读取 files 节的字符串配置；缺失/空值时返回默认值。
// （files 节允许缺项：旧版忽略缺项错误会得到空字符串，
// resolvePath 会把路径解析成根目录本身，读写文件时行为不可预测）
func fileValue(section map[string]interface{}, key, def string) string {
	v, ok := section[key]
	if !ok || v == nil {
		return def
	}
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func asBool(v interface{}, name string) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case int:
		return t != 0, nil
	case string:
		n := strings.ToLower(strings.TrimSpace(t))
		switch n {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off":
			return false, nil
		}
	}
	return false, fmt.Errorf("无效的布尔配置值 %s=%v", name, v)
}

func asInt(v interface{}, name string) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("无效的整数配置值 %s=%v", name, v)
		}
		return i, nil
	}
	return 0, fmt.Errorf("无效的整数配置值 %s=%v", name, v)
}

func asStringSlice(v interface{}) ([]string, error) {
	if v == nil {
		return []string{}, nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("必须是字符串列表")
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("列表元素必须是字符串")
		}
		out = append(out, s)
	}
	return out, nil
}
