// gen.go - 「生成配置」节（gen）：协议/传输/证书校验/0RTT/分片/随机路径/ECH/指纹。
//
// 数据来源优先级：
//  1. auto_from_panel = true 且远程面板 admin/config.json 拉取成功 → 使用面板值；
//  2. 否则使用本节的 manual 手动默认值；
//  3. manual 缺项时逐项回落到与历史硬编码模板一致的缺省值。
package config

import (
	"fmt"
	"math/rand"
	"strings"
)

// 支持的取值白名单（对齐 edgetunnel 生态面板选项）。
var (
	GenProtocols    = []string{"vless", "trojan", "ss"}
	GenTransports   = []string{"ws", "xhttp", "grpc"}
	GenGRPCModes    = []string{"gun", "multi"}
	GenFragments    = []string{"", "shadowrocket", "happ"}
	GenFingerprints = []string{
		"chrome", "firefox", "safari", "ios", "android",
		"edge", "360", "qq", "random", "randomized",
	}
)

// GenSettings 单个生成配置快照。
type GenSettings struct {
	Protocol       string `json:"protocol"`        // vless | trojan | ss
	Transport      string `json:"transport"`       // ws | xhttp | grpc
	GRPCMode       string `json:"grpc_mode"`       // gun | multi
	GRPCUserAgent  string `json:"grpc_user_agent"` // gRPC user-agent 元数据（可空）
	SkipCertVerify bool   `json:"skip_cert_verify"`
	Enable0RTT     bool   `json:"enable_0rtt"`
	Fragment       string `json:"fragment"`    // "" | shadowrocket | happ
	RandomPath     bool   `json:"random_path"` // 随机伪装路径前缀
	ECH            bool   `json:"ech"`
	ECHDNS         string `json:"ech_dns"`
	ECHSNI         string `json:"ech_sni"`
	Fingerprint    string `json:"fingerprint"`
	SSCipher       string `json:"ss_cipher"` // SS 加密方式（面板 SS.加密方式；缺省 aes-128-gcm）
	SSTLS          bool   `json:"ss_tls"`    // SS 是否启用 TLS（面板 SS.TLS；false 时端口映射为 noTLS 组）
}

// ssTLSPorts/ssPlainPorts SS TLS 与 noTLS 端口一一映射（对齐生态运行时行为）。
var ssTLSPorts = []int{443, 2053, 2083, 2087, 2096, 8443}
var ssPlainPorts = []int{80, 2052, 2082, 2086, 2095, 8080}

// SSMapPort SS 非 TLS 时把 TLS 端口组映射为对应 noTLS 端口；其余端口原样返回。
func SSMapPort(port string) string {
	n := 0
	if _, err := fmt.Sscanf(port, "%d", &n); err != nil {
		return port
	}
	for i, tp := range ssTLSPorts {
		if n == tp {
			return fmt.Sprintf("%d", ssPlainPorts[i])
		}
	}
	return port
}

// DefaultGenSettings 返回缺省生成配置。
// 与既有硬编码订阅模板完全一致（vless + ws + chrome + 始终 0RTT），
// 保证未升级配置文件的老部署生成结果零变化。
func DefaultGenSettings() GenSettings {
	return GenSettings{
		Protocol:       "vless",
		Transport:      "ws",
		GRPCMode:       "gun",
		GRPCUserAgent:  "",
		SkipCertVerify: false,
		Enable0RTT:     true,
		Fragment:       "",
		RandomPath:     false,
		ECH:            false,
		ECHDNS:         "",
		ECHSNI:         "",
		Fingerprint:    "chrome",
		SSCipher:       SSCipher,
		SSTLS:          true,
	}
}

// Normalized 对全部枚举字段做白名单收敛：非法值回落到对应缺省，
// 自由文本字段 trim 后返回副本。
func (g GenSettings) Normalized() GenSettings {
	d := DefaultGenSettings()
	out := g

	out.Protocol = strings.ToLower(strings.TrimSpace(g.Protocol))
	if !contains(GenProtocols, out.Protocol) {
		out.Protocol = d.Protocol
	}
	out.Transport = strings.ToLower(strings.TrimSpace(g.Transport))
	if !contains(GenTransports, out.Transport) {
		out.Transport = d.Transport
	}
	out.GRPCMode = strings.ToLower(strings.TrimSpace(g.GRPCMode))
	if !contains(GenGRPCModes, out.GRPCMode) {
		out.GRPCMode = d.GRPCMode
	}
	out.Fragment = strings.ToLower(strings.TrimSpace(g.Fragment))
	if !contains(GenFragments, out.Fragment) {
		out.Fragment = d.Fragment
	}
	finger := strings.ToLower(strings.TrimSpace(g.Fingerprint))
	if finger == "" {
		finger = d.Fingerprint
	}
	if !contains(GenFingerprints, finger) {
		finger = d.Fingerprint
	}
	out.Fingerprint = finger

	out.GRPCUserAgent = strings.TrimSpace(g.GRPCUserAgent)
	out.SSCipher = strings.TrimSpace(g.SSCipher)
	if out.SSCipher == "" {
		out.SSCipher = d.SSCipher
	}
	out.ECHDNS = strings.TrimSpace(g.ECHDNS)
	out.ECHSNI = strings.TrimSpace(g.ECHSNI)
	if out.ECH && out.ECHDNS == "" {
		// 开启 ECH 却未给 DNS 时回退关闭，避免生成无效链接。
		out.ECH = false
	}
	return out
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// randomPathDirs 常用目录词表（随机伪装路径池）。
var randomPathDirs = []string{
	"about", "account", "acg", "act", "activity", "ad", "ads", "ajax", "album", "albums",
	"anime", "api", "app", "apps", "archive", "archives", "article", "articles", "ask", "auth",
	"avatar", "bbs", "bd", "blog", "blogs", "book", "books", "bt", "buy", "cart",
	"category", "categories", "cb", "channel", "channels", "chat", "china", "city", "class", "classify",
	"clip", "clips", "club", "cn", "code", "collect", "collection", "comic", "comics", "community",
	"company", "config", "contact", "content", "course", "courses", "cp", "data", "detail", "details",
	"dh", "directory", "discount", "discuss", "dl", "dload", "doc", "docs", "document", "documents",
	"doujin", "download", "downloads", "drama", "edu", "en", "ep", "episode", "episodes", "event",
	"events", "f", "faq", "favorite", "favourites", "favs", "feedback", "file", "files", "film",
	"films", "forum", "forums", "friend", "friends", "game", "games", "gif", "go", "group",
	"groups", "help", "home", "hot", "htm", "html", "image", "images", "img", "index",
	"info", "intro", "item", "items", "ja", "jp", "jump", "knowledge", "lang", "lesson",
	"lessons", "lib", "library", "link", "links", "list", "live", "lives", "m", "mag",
	"magnet", "mall", "manhua", "map", "member", "members", "message", "messages", "mobile", "movie",
	"movies", "music", "my", "new", "news", "note", "novel", "novels", "online", "order",
	"out", "outbound", "p", "page", "pages", "pay", "payment", "pdf", "photo", "photos",
	"pic", "pics", "picture", "pictures", "play", "player", "playlist", "post", "posts", "product",
	"products", "program", "programs", "project", "qa", "question", "rank", "ranking", "read", "readme",
	"redirect", "reg", "register", "res", "resource", "retrieve", "sale", "search", "season", "seasons",
	"section", "seller", "series", "service", "services", "setting", "settings", "share", "shop", "show",
	"shows", "site", "soft", "sort", "source", "special", "star", "stars", "static", "stock",
	"store", "stream", "streaming", "streams", "student", "study", "tag", "tags", "task", "teacher",
	"team", "tech", "temp", "test", "thread", "tool", "tools", "topic", "topics", "torrent",
	"trade", "travel", "tv", "txt", "type", "u", "upload", "uploads", "url", "urls",
	"user", "users", "v", "version", "videos", "view", "vip", "vod", "watch", "web",
	"wenku", "wiki", "work", "www", "zh", "zh-cn", "zh-tw", "zip",
}

// SS 加密方式固定值（生态缺省一致）。
const SSCipher = "aes-128-gcm"

// BaseProxyPath 反代路径模板（无 query、未编码）：edt 与 snippets 两种格式。
func BaseProxyPath(format, proxyIP string) string {
	if proxyIP == "" || proxyIP == "DIRECT" {
		return "/"
	}
	if format == "edt" {
		return "/proxyip=" + proxyIP
	}
	return "/snippets/ip=" + proxyIP
}

// TransportPath 计算当前配置下某节点的传输路径值（含 enc/ed 追加与随机前缀），
// 未做 URL 编码。format 为订阅模板格式（edt|snippets）。
func (g GenSettings) TransportPath(format, proxyIP string) string {
	p := BaseProxyPath(format, proxyIP)

	var params []string
	if g.Protocol == "ss" {
		params = append(params, "enc="+g.SSCipher)
	}
	if g.Enable0RTT {
		params = append(params, "ed=2560")
	}
	if len(params) > 0 {
		p += "?" + strings.Join(params, "&")
	}

	if g.RandomPath {
		n := rand.Intn(3) + 1 //nolint:gosec // 仅伪装混淆用
		dirs := make([]string, 0, n)
		for i := 0; i < n; i++ {
			dirs = append(dirs, randomPathDirs[rand.Intn(len(randomPathDirs))]) //nolint:gosec
		}
		if p == "/" {
			p = "/" + strings.Join(dirs, "/")
		} else {
			p = "/" + strings.Join(dirs, "/") + strings.Replace(p, "/?", "?", 1)
		}
	}
	return p
}

// applyGenSection 应用可选的 gen 节；节点缺失或字段非法时保持缺省，
// 不视为致命错误（与 subconverter 节的宽松策略一致）。
func applyGenSection(cfg *RuntimeConfig, data map[string]interface{}) {
	cfg.GenAutoFromPanel = true // 默认勾选：自动获取配置（协议，设置）
	cfg.GenAggregateWorkerSub = false
	cfg.GenManual = DefaultGenSettings()

	raw, ok := data["gen"]
	if !ok || raw == nil {
		return
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := m["auto_from_panel"]; ok {
		if b, err := asBool(v, "gen.auto_from_panel"); err == nil {
			cfg.GenAutoFromPanel = b
		}

		if v, ok := m["aggregate_worker_sub"]; ok {
			if b, err := asBool(v, "gen.aggregate_worker_sub"); err == nil {
				cfg.GenAggregateWorkerSub = b
			}
		}
	}
	mm, ok := m["manual"].(map[string]interface{})
	if !ok {
		return
	}
	g := cfg.GenManual
	if v, ok := mm["protocol"]; ok {
		g.Protocol = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["transport"]; ok {
		g.Transport = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["grpc_mode"]; ok {
		g.GRPCMode = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["grpc_user_agent"]; ok {
		g.GRPCUserAgent = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["skip_cert_verify"]; ok {
		if b, err := asBool(v, "gen.manual.skip_cert_verify"); err == nil {
			g.SkipCertVerify = b
		}
	}
	if v, ok := mm["enable_0rtt"]; ok {
		if b, err := asBool(v, "gen.manual.enable_0rtt"); err == nil {
			g.Enable0RTT = b
		}
	}
	if v, ok := mm["fragment"]; ok {
		g.Fragment = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["random_path"]; ok {
		if b, err := asBool(v, "gen.manual.random_path"); err == nil {
			g.RandomPath = b
		}
	}
	if v, ok := mm["ech"]; ok {
		if b, err := asBool(v, "gen.manual.ech"); err == nil {
			g.ECH = b
		}
	}
	if v, ok := mm["ech_dns"]; ok {
		g.ECHDNS = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["ech_sni"]; ok {
		g.ECHSNI = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["fingerprint"]; ok {
		g.Fingerprint = fmt.Sprintf("%v", v)
	}

	if v, ok := mm["ss_cipher"]; ok {
		g.SSCipher = fmt.Sprintf("%v", v)
	}
	if v, ok := mm["ss_tls"]; ok {
		if b, err := asBool(v, "gen.manual.ss_tls"); err == nil {
			g.SSTLS = b
		}
	}
	cfg.GenManual = g.Normalized()
}
