// edt_panel - VLess 订阅管理服务（Go 实现）。
//
// 由原 Python Flask 项目重写，纯 Go 单二进制，无外部运行时依赖。
// 支持原 config.yml 格式，保留全部原功能，并集成 subconverter 桥接。
//
// main 的执行阶段（见 main 函数编排）：
//  1. loadRuntimeConfig   —— 解析 CLI + 加载 config.yml 并应用覆盖；
//  2. newApp              —— 装配各 service；
//  3. resolveSubConverter —— 合成 subconverter 桥接配置（CLI 优先，config 兜底）；
//  4. runHTTP             —— 启动监听并等待退出信号（优雅关闭）。
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"edt/internal/config"
	"edt/internal/module"
	"edt/internal/server"
	"edt/internal/service"
	"edt/internal/ui"
)

// CLI 命令行参数（kong 解析，支持长短参数）。
type CLI struct {
	Config string `short:"c" help:"配置文件路径（默认 ./config.yml）" type:"path"`
	Host   string `short:"H" help:"覆盖监听地址"`
	Port   int    `short:"p" help:"覆盖监听端口"`
	Debug  bool   `short:"d" long:"debug" help:"调试模式"`

	// subconverter 桥接
	SCMode   string `long:"sc-mode" help:"subconverter 模式: off|local|remote" default:"off"`
	SCRemote string `long:"sc-remote" help:"远程 subconverter 地址（如 https://api.v1.mk）"`
	SCLocal  string `long:"sc-local" help:"本地 subconverter 二进制路径" type:"path"`
	SCPort   int    `long:"sc-port" help:"本地 subconverter 监听端口" default:"25500"`

	NoColor bool `long:"no-color" help:"禁用终端彩色输出"`
}

// app 装配完成的运行时服务集合。
type app struct {
	cfg      *config.RuntimeConfig
	cfgStore *service.ConfigStore
	preIP    *service.PreIPStore
	result   *service.ResultStore
	uuid     *service.UUIDService
	subSvc   *service.SubscriptionService
}

const (
	// appVersion 面板版本号：CLI --version、启动横幅与 /api/status（设置页关于）共用。
	appVersion = "1.0.0-alpha1"
)

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("edt"),
		kong.Description("edt_panel - VLess 订阅管理服务"),
		kong.UsageOnError(),
		kong.Vars{"version": appVersion},
	)
	_ = ctx

	if !cli.NoColor {
		ui.Init()
	}

	cfg, err := loadRuntimeConfig(cli)
	if err != nil {
		// 初始化 config.yml 后退出是预期行为
		ui.Println(ui.WarnStyle, ui.SymWarn+" "+err.Error())
		os.Exit(1)
	}
	printBanner(cfg)

	a := newApp(cfg)

	// subconverter 桥接：CLI 参数优先，config.yml 兜底
	scCfg, scMode := resolveSubConverterSettings(cli, cfg)
	var sup *scSupervisor
	if scMode == "local" {
		sup = startSCSupervisor(scCfg.LocalBin, scCfg.LocalPort, cfg.RootDir)
	}
	a.subSvc.SetSubConverter(scCfg)

	// 启动日志落盘（先于 WatchLogFile，保证日志文件存在、轮询可立即开始）
	server.LogLine("edt_panel 服务启动 root=%s 控制端口=%d 订阅端口=%d subconverter=%s",
		cfg.RootDir, cfg.Port, cfg.SubscriptionPort, scMode)

	// 实时日志推送：/ws 与 HTTP 同端口同源（可鉴权），日志文件守护常驻
	go server.WatchLogFile()
	ui.Printf(ui.DimStyle, "  - 实时日志: ws://%s/ws\n", cfg.Host+":"+strconv.Itoa(cfg.Port))

	// HTTP Server 先于回调构造：重启回调需要引用 srv 做优雅关闭
	var srv *http.Server
	handler := server.New(server.Deps{
		Cfg:             cfg,
		Version:         appVersion,
		ConfigStore:     a.cfgStore,
		PreIPStore:      a.preIP,
		ResultStore:     a.result,
		UUIDService:     a.uuid,
		SubscriptionSvc: a.subSvc,
		Stats:           service.NewStatsService(filepath.Join(cfg.RootDir, "data", "stats.json")),
		WebPassword:     cfg.LoginPassword,
		SCRestart:       supRestartFunc(sup),
		ReloadServices: func(newCfg *config.RuntimeConfig) ([]string, error) {
			return reloadRuntime(cli, a, sup, newCfg)
		},
		RestartProc: func() error { return restartProc(srv, sup) },
	})
	srv = newHTTPServer(cfg, handler)

	runHTTP(srv, cfg, scMode, sup)
}

// reloadRuntime 把重载后的配置应用到运行中的各服务
// （面板「配置文件」保存且校验通过后调用；与 newApp 的装配顺序保持一致）。
// CLI 显式覆盖项沿用启动时语义（-H/-p/-d 仍优先于 config.yml）。
// 返回热生效项描述列表，供前端展示。
func reloadRuntime(cli CLI, a *app, sup *scSupervisor, newCfg *config.RuntimeConfig) ([]string, error) {
	if cli.Host != "" {
		newCfg.Host = cli.Host
	}
	if cli.Port != 0 {
		newCfg.Port = cli.Port
	}
	if cli.Debug {
		newCfg.Debug = true
	}

	a.cfgStore.Reload(newCfg.VlessFile, newCfg.NRTFile, newCfg.Variables)
	a.preIP.Reload(newCfg.PreIPFile)
	a.result.Reload(newCfg.ResultFile)
	a.uuid.Reload(newCfg.AdminURL, newCfg.RunTimeFile, newCfg.ControlDomain, newCfg.RequestTimeout)
	a.uuid.SetAuthConfig(module.AuthConfig{
		LoginURL:       newCfg.LoginURL,
		LoginPassword:  newCfg.LoginPassword,
		ControlDomain:  newCfg.ControlDomain,
		RequestTimeout: time.Duration(newCfg.RequestTimeout) * time.Second,
		AuthCacheFile:  newCfg.AuthCacheFile,
	})
	a.subSvc.Reload(
		newCfg.NRTFile, newCfg.SubscriptionPort,
		newCfg.Profiles, newCfg.DefaultProfile, newCfg.DefaultProfileByID,
		newCfg.EncodeSubscriptionBase64, newCfg.DataSources,
	)
	a.subSvc.SetUserinfoExpire(newCfg.UserinfoExpire)
	a.subSvc.SetGenConfig(newCfg.GenAutoFromPanel, newCfg.GenAggregateWorkerSub, newCfg.GenManual)

	// subconverter 桥接随配置更新；local 模式触发子进程监督者
	// （bin 路径变化 / 首次安装后自动拉起新进程）
	scCfg, scMode := resolveSubConverterSettings(cli, newCfg)
	a.subSvc.SetSubConverter(scCfg)
	if scMode == "local" && sup != nil {
		sup.TriggerRestart()
	}

	return []string{
		"文件路径（files）",
		"变量（variables）",
		"订阅模板（subscriptions）",
		"数据源（data_sources）",
		"远程面板对接（remote）",
		"鉴权与订阅头（auth）",
		"生成配置（gen）",
		"subconverter 桥接",
	}, nil
}

// restartProc 优雅关闭监听后 exec 自身重启进程（收到请求即刻异步执行，
// 先让 HTTP 响应送达客户端）。Windows 无进程自替换能力，同步返回错误提示。
func restartProc(srv *http.Server, sup *scSupervisor) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Windows 平台不支持进程内重启，请手动退出后重新启动")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件失败: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	go func() {
		time.Sleep(500 * time.Millisecond) // 让重启响应先送达
		ui.Println(ui.WarnStyle, ui.SymWarn+" 收到重启请求，正在重启服务…")
		sup.Stop()
		if srv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}
		server.LogLine("服务重启：exec %s", exe)
		if err := execSelf(exe); err != nil {
			ui.Println(ui.ErrorStyle, ui.SymFail+" 重启失败: "+err.Error())
			os.Exit(1) // 兜底退出：systemd 等守护进程会重新拉起
		}
	}()
	return nil
}

// newHTTPServer 构造 HTTP Server。
// 生产加固（社区实践）：ReadHeaderTimeout —— slowloris 防线（不设则 Go 默认无超时）；
// IdleTimeout —— 空闲连接回收；不设 ReadTimeout/WriteTimeout —— 会掐断 /ws 长连接。
func newHTTPServer(cfg *config.RuntimeConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// supRestartFunc 将监督者的重启触发能力以函数值注入 server 层（nil 安全）。
func supRestartFunc(sup *scSupervisor) func() {
	if sup == nil {
		return nil
	}
	return sup.TriggerRestart
}

// scSupervisor 本地 subconverter 子进程监督者：
// 启动、健康检查、崩溃自动重启（指数退避）与优雅停止。
type scSupervisor struct {
	bin     string
	port    int
	rootDir string

	mu        sync.Mutex
	cmd       *exec.Cmd
	stopped   bool
	restarts  int
	restartCh chan struct{} // 外部请求尽快重启（如安装新版本后）
}

// startSCSupervisor 创建监督者并进入守护循环；二进制缺失时只告警不退出，
// 之后安装完成触发 TriggerRestart 即可自动拉起。
func startSCSupervisor(bin string, port int, rootDir string) *scSupervisor {
	s := &scSupervisor{bin: bin, port: port, rootDir: rootDir,
		restartCh: make(chan struct{}, 1)}
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		ui.Println(ui.WarnStyle, ui.SymWarn+" 本地 subconverter 二进制不存在: "+bin)
		ui.Println(ui.DimStyle, "  可在设置页通过「下载安装」获取，或手动放到 "+filepath.Dir(bin))
		return s
	}
	go s.loop()
	return s
}

// loop 守护循环：拉起子进程并等待退出；异常退出按指数退避自动重启，
// 稳定运行 60s 后退避时长复位；收到 TriggerRestart 时立即重试。
func (s *scSupervisor) loop() {
	backoff := time.Second
	for {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		cmd := exec.Command(s.bin)
		cmd.Dir = filepath.Dir(s.bin)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Start()
		if err == nil {
			s.cmd = cmd
		}
		s.mu.Unlock()

		if err != nil {
			ui.Println(ui.WarnStyle, ui.SymWarn+" 本地 subconverter 启动失败: "+err.Error())
		} else {
			ui.Printf(ui.SuccessStyle, "%s 本地 subconverter 已启动 (PID %d, 端口 %d)\n",
				ui.SymSuccess, cmd.Process.Pid, s.port)
			runErr := cmd.Wait()
			s.mu.Lock()
			s.cmd = nil
			s.restarts++
			n := s.restarts
			s.mu.Unlock()
			if s.isStopped() {
				return
			}
			ui.Printf(ui.WarnStyle, "%s 本地 subconverter 异常退出 (%v)，为第 %d 次，稍后自动重启\n",
				ui.SymWarn, runErr, n)
		}

		select {
		case <-time.After(backoff):
		case <-s.restartCh:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		// 长时间稳定运行后退避复位，避免偶发崩溃被长时间惩罚
		if _, alive := s.currentPID(); alive {
			backoff = time.Second
		}
	}
}

// currentPID 返回当前子进程 PID 与存活状态。
func (s *scSupervisor) currentPID() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return 0, false
	}
	return s.cmd.Process.Pid, true
}

// isStopped 返回监督者是否已进入停止流程。
func (s *scSupervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// TriggerRunnable 报告监督者是否有可触发的重启通道（未启动过进程时也允许触发拉起）。
func (s *scSupervisor) TriggerRestart() {
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
}

// Stop 优雅停止监督者与子进程（先 SIGINT，3s 超时后 Kill）。
// 空接收者安全：subconverter 关闭（off）时不构造监督者，
// 启动失败路径仍会调用本方法。
func (s *scSupervisor) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// loadRuntimeConfig 解析配置路径、加载 config.yml 并应用 CLI 覆盖项。
func loadRuntimeConfig(cli CLI) (*config.RuntimeConfig, error) {
	if cli.Config == "" {
		cli.Config = "config.yml"
	}
	rootDir := filepath.Dir(cli.Config)
	cfg, err := config.Load(rootDir)
	if err != nil {
		return nil, err
	}
	if cli.Host != "" {
		cfg.Host = cli.Host
	}
	if cli.Port != 0 {
		cfg.Port = cli.Port
	}
	if cli.Debug {
		cfg.Debug = true
	}
	return cfg, nil
}

// newApp 按依赖顺序构造 ConfigStore / PreIPStore / ResultStore / UUIDService / SubscriptionService。
func newApp(cfg *config.RuntimeConfig) *app {
	cfgStore := service.NewConfigStore(cfg.VlessFile, cfg.NRTFile, cfg.Variables)
	preIPStore := service.NewPreIPStore(cfg.PreIPFile)
	resultStore := service.NewResultStore(cfg.ResultFile)
	uuidService := service.NewUUIDService(
		cfg.AdminURL, cfg.RunTimeFile, cfg.ControlDomain, cfg.RequestTimeout,
	)
	uuidService.SetAuthConfig(module.AuthConfig{
		LoginURL:       cfg.LoginURL,
		LoginPassword:  cfg.LoginPassword,
		ControlDomain:  cfg.ControlDomain,
		RequestTimeout: time.Duration(cfg.RequestTimeout) * time.Second,
		AuthCacheFile:  cfg.AuthCacheFile,
	})
	subService := service.NewSubscriptionService(
		cfgStore, resultStore, preIPStore, uuidService,
		cfg.NRTFile, cfg.SubscriptionPort,
		cfg.Profiles, cfg.DefaultProfile, cfg.DefaultProfileByID,
		cfg.EncodeSubscriptionBase64, cfg.DataSources,
	)
	subService.SetUserinfoExpire(cfg.UserinfoExpire)
	subService.SetGenConfig(cfg.GenAutoFromPanel, cfg.GenAggregateWorkerSub, cfg.GenManual)
	return &app{
		cfg:      cfg,
		cfgStore: cfgStore,
		preIP:    preIPStore,
		result:   resultStore,
		uuid:     uuidService,
		subSvc:   subService,
	}
}

// resolveSubConverterSettings 合成 subconverter 桥接配置；
// 返回配置与最终生效的 mode（供启动横幅与本地子进程判断）。
func resolveSubConverterSettings(cli CLI, cfg *config.RuntimeConfig) (service.SubConverterConfig, string) {
	scMode := cli.SCMode
	if scMode == "" || scMode == "off" {
		scMode = cfg.SubConverterMode
	}
	scRemote := cli.SCRemote
	if scRemote == "" {
		scRemote = cfg.SubConverterRemote
	}
	scBin := cli.SCLocal
	if scBin == "" {
		scBin = cfg.SubConverterBin
	}
	if scBin == "" {
		scBin = filepath.Join(cfg.RootDir, "bin", "subconverter", "subconverter")
	}
	scPort := cli.SCPort
	if scPort == 0 || scPort == 25500 {
		if cfg.SubConverterPort > 0 {
			scPort = cfg.SubConverterPort
		} else {
			scPort = 25500
		}
	}
	return service.SubConverterConfig{
		Mode:      scMode,
		Remote:    scRemote,
		LocalBin:  scBin,
		LocalPort: scPort,
	}, scMode
}

// runHTTP 打印访问地址，阻塞等待服务错误或退出信号。
// 退出时优雅关闭 HTTP 并回收 subconverter 子进程（监督者统一管理），避免孤儿进程。
func runHTTP(srv *http.Server, cfg *config.RuntimeConfig, scMode string, sup *scSupervisor) {
	addr := srv.Addr
	ui.Printf(ui.InfoStyle, "%s 监听 %s\n", ui.SymArrow, addr)
	ui.Printf(ui.DimStyle, "  - 仪表盘  : http://%s/\n", addr)
	ui.Printf(ui.DimStyle, "  - 节点配置: http://%s/nodes\n", addr)
	ui.Printf(ui.DimStyle, "  - 优选 IP : http://%s/selector\n", addr)
	ui.Printf(ui.DimStyle, "  - 设置    : http://%s/settings\n", addr)
	ui.Printf(ui.DimStyle, "  - subconverter: %s\n", scMode)

	// 生产加固（社区实践）：
	//   ReadHeaderTimeout —— slowloris 防线（不设则 Go 默认无超时）；
	//   IdleTimeout       —— 空闲连接回收；
	//   不设 ReadTimeout/WriteTimeout —— 会掐断 /ws 长连接与日志推送。
	// （Server 实例由 newHTTPServer 构造后传入，重启回调需引用同一实例）

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		ui.Println(ui.ErrorStyle, ui.SymFail+" 服务启动失败: "+err.Error())
		sup.Stop()
		os.Exit(1)
	case <-sigCtx.Done():
		ui.Println(ui.WarnStyle, ui.SymWarn+" 收到退出信号，正在关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		sup.Stop()
	}
}

// printBanner 打印启动横幅（根目录/订阅端口/模板数等概览）。
func printBanner(cfg *config.RuntimeConfig) {
	title := ui.TitleStyle.Render("edt_panel")
	subtitle := ui.DimStyle.Render("VLess 订阅管理服务 · Go " + goVersion())
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  "+title+"  "+subtitle)
	fmt.Fprintln(os.Stderr, "  "+ui.DimStyle.Render(strings.Repeat("-", 40)))
	ui.Printf(ui.DimStyle, "  根目录: %s\n", cfg.RootDir)
	ui.Printf(ui.DimStyle, "  订阅端口: %d  默认模板: %s  Base64: %v\n",
		cfg.SubscriptionPort, cfg.DefaultProfile, cfg.EncodeSubscriptionBase64)
	if len(cfg.Profiles) > 0 {
		ui.Printf(ui.DimStyle, "  模板数: %d  数据源数: %d\n",
			len(cfg.Profiles), len(cfg.DataSources))
	}
	fmt.Fprintln(os.Stderr)
}

// goVersion 返回编译用的 Go 版本（替代原先写死的占位文案）。
func goVersion() string {
	return strings.TrimPrefix(runtime.Version(), "go")
}
