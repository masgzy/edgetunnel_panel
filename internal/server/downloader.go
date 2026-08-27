// downloader.go - 多线程分片下载器。
// 用于从远程下载 subconverter 二进制（支持多线程分片）。
package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"edt/internal/ui"
)

// minArchiveSize 发布包合理体积下限；小于该值视为探测错误而非真实文件。
const minArchiveSize = 1 << 20 // 1MB

// downloadClient 带 User-Agent 的 HTTP 客户端；部分 CDN 会限制无 UA 流量。
func downloadClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// setDownloadUA 设置常见 CDN 校验的 User-Agent。
func setDownloadUA(req *http.Request) {
	req.Header.Set("User-Agent", "edt-downloader/2")
}

// DownloadSubconverter 多线程分片下载 subconverter。
// url: 下载地址; destDir: 目标目录; threads: 线程数（默认 32）。
// 返回压缩包落盘路径（统一命名，避免与解压条目同名冲突）；
// 服务器不支持 Range 或探得的尺寸可疑时自动退化为单线程。
func DownloadSubconverter(downloadURL, destDir string, threads int) (string, error) {
	if threads <= 0 {
		threads = 32
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	client := downloadClient(30 * time.Second)
	totalSize, err := probeContentLength(client, downloadURL)
	if err != nil {
		return "", err
	}
	// 尺寸缺失或小于发布包合理下限时不可信（可能是重定向页/HTML 错误页），
	// 盲目分片会把真文件切成垃圾碎片，必须整体退化单线程。
	if totalSize <= 0 || totalSize < minArchiveSize {
		ui.Printf(ui.WarnStyle, "%s 探测大小 %s 不可信，退化单线程下载\n",
			ui.SymWarn, formatBytes(totalSize))
		return singleThreadDownload(downloadURL, destDir, downloadClient(5*time.Minute))
	}

	// 分片数不超过实际字节数下限，避免微小文件产生海量空分片
	if maxThreads := int(totalSize/(64*1024)) + 1; threads > maxThreads {
		threads = maxThreads
	}
	ui.Printf(ui.InfoStyle, "%s 文件大小: %s, 线程数: %d\n",
		ui.SymArrow, formatBytes(totalSize), threads)

	tempDir := filepath.Join(destDir, ".tmp")
	os.MkdirAll(tempDir, 0o755)
	defer os.RemoveAll(tempDir)

	if err := parallelDownload(downloadURL, tempDir, threads, totalSize, client); err != nil {
		return "", err
	}

	// 下载产物统一落为 .part 命名，绝不占用 subconverter 这个解压目标名
	// （历史 bug：zip 存为 subconverter 后解压内含同名条目，create 自我截断归零）
	archivePath := filepath.Join(destDir, "sc_download.part")
	if err := mergeParts(tempDir, threads, archivePath); err != nil {
		return "", err
	}
	os.Chmod(archivePath, 0o644)

	ui.Printf(ui.SuccessStyle, "%s 下载完成 (%s)\n",
		ui.SymSuccess, formatBytes(totalSize))
	return archivePath, nil
}

// probeContentLength 通过 GET Range bytes=0-0 探测文件总大小。
// 不用 HEAD：部分代理/网关（如 nightly.link）对 HEAD 返回错误页，其 Content-Length
// 会被误当成文件大小导致分片切错；也不盲信状态码，需同时校验 Content-Range。
func probeContentLength(client *http.Client, downloadURL string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return 0, err
	}
	setDownloadUA(req)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("探测请求失败: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64))

	if resp.StatusCode == http.StatusOK {
		// 未启用 Range：无法分片，返回 -1 交由调用方走单线程
		return -1, nil
	}
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("探测返回 %d，源站可能不可用", resp.StatusCode)
	}
	cr := resp.Header.Get("Content-Range") // 形如 "bytes 0-0/7199881"
	idx := strings.LastIndex(cr, "/")
	if idx < 0 || idx == len(cr)-1 {
		return -1, nil
	}
	var total int64
	if _, err := fmt.Sscanf(cr[idx+1:], "%d", &total); err != nil || total <= 0 {
		return -1, nil
	}
	return total, nil
}

// parallelDownload 按 Range 分片并发下载到 tempDir/part_NNN。
// 任一分片失败即整体失败；服务器若忽略 Range 返回 200 全量体，同样判为失败
// （否则每个分片都会写入完整文件，合并结果必然损坏）。
func parallelDownload(downloadURL, tempDir string, threads int, totalSize int64, client *http.Client) error {
	partSize := totalSize / int64(threads)

	var wg sync.WaitGroup
	errCh := make(chan error, threads)
	successCount := 0
	var successMu sync.Mutex

	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start := int64(idx) * partSize
			end := start + partSize - 1
			if idx == threads-1 {
				end = totalSize - 1 // 最后一片包含剩余
			}
			var err error
			// 瞬时限流/网络抖动小退避重试一次
			for attempt := 0; attempt < 2; attempt++ {
				if err = downloadChunk(downloadURL, tempDir, idx, start, end, client); err == nil {
					break
				}
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			}
			if err != nil {
				errCh <- err
				return
			}
			successMu.Lock()
			successCount++
			successMu.Unlock()
		}(i)
	}

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return fmt.Errorf("分片下载失败: %w", err)
	}
	if successCount < threads {
		return fmt.Errorf("仅 %d/%d 分片成功", successCount, threads)
	}
	return nil
}

// downloadChunk 下载单个 Range 分片并写入 tempDir/part_NNN。
func downloadChunk(downloadURL, tempDir string, idx int, start, end int64, client *http.Client) error {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("分片 %d 返回 %d（服务器可能不支持断点分片）", idx, resp.StatusCode)
	}

	partFile := filepath.Join(tempDir, fmt.Sprintf("part_%03d", idx))
	out, err := os.Create(partFile)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// mergeParts 按序合并全部分片到 binPath。
func mergeParts(tempDir string, threads int, binPath string) error {
	out, err := os.Create(binPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i := 0; i < threads; i++ {
		partFile := filepath.Join(tempDir, fmt.Sprintf("part_%03d", i))
		data, err := os.ReadFile(partFile)
		if err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// singleThreadDownload 退化为单线程整档下载，产物固定命名为 sc_download.part。
// 绝不能写成 destDir/subconverter：解压时 zip 内同名条目会 create 覆盖自身归零。
func singleThreadDownload(downloadURL, destDir string, client *http.Client) (string, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	setDownloadUA(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("下载返回非成功状态码: %d", resp.StatusCode)
	}
	if cl := resp.ContentLength; cl > 0 && cl < minArchiveSize {
		bodyHead, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		snippet := strings.TrimSpace(string(bodyHead))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "..."
		}
		return "", fmt.Errorf("响应仅 %dB 且更像错误页: %q", cl, snippet)
	}

	archivePath := filepath.Join(destDir, "sc_download.part")
	out, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	written, err := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written < minArchiveSize {
		return "", fmt.Errorf("下载内容仅 %s，不足发布包下限，已放弃", formatBytes(written))
	}
	os.Chmod(archivePath, 0o644)
	return archivePath, nil
}

// formatBytes 格式化字节大小。
func formatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	}
	if b < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
	return fmt.Sprintf("%.1fGB", float64(b)/(1024*1024*1024))
}

// archNames GOOS/GOARCH → subconverter 发布架构名映射。
var archNames = map[[2]string]string{
	{"linux", "amd64"}:   "linux64",
	{"linux", "386"}:     "linux32",
	{"linux", "arm64"}:   "aarch64",
	{"linux", "arm"}:     "armv7",
	{"android", "arm64"}: "aarch64", // Termux 等环境：GOOS=android 但子进程按 aarch64 处理
	{"darwin", "amd64"}:  "darwin64",
	{"darwin", "arm64"}:  "darwinarm",
	{"windows", "amd64"}: "win64",
	{"windows", "386"}:   "win32",
}

// detectArch 自动检测当前系统架构，返回对应的 subconverter 架构名（未知平台回退 linux64）。
func detectArch() string {
	if name, ok := archNames[[2]string{runtime.GOOS, runtime.GOARCH}]; ok {
		return name
	}
	return "linux64"
}

// scInstallHandler POST /api/sc-install - 多线程下载安装 subconverter
// 请求体 JSON: {"threads": 32, "arch": "linux64", "scbase": "https://..."}
func (d *Deps) scInstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST method only")
		return
	}
	req, err := parseScInstallRequest(r, d.Cfg.RootDir)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	destDir := filepath.Join(d.Cfg.RootDir, "bin", "subconverter")
	os.MkdirAll(destDir, 0o755)

	threads, err := installSubconverterArchive(scDownloadURL(req.ScBase, req.Arch), destDir, req.Threads)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "安装失败: "+err.Error())
		return
	}
	// 本地桥接运行中时，安装完成立即重启子进程以启用新版本二进制
	if d.SCRestart != nil {
		go d.SCRestart()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "installed",
		"arch":     req.Arch,
		"bin_path": filepath.Join(destDir, "subconverter"),
		"threads":  threads,
	})
}

// scInstallRequest POST 请求体。
type scInstallRequest struct {
	Threads int    `json:"threads"`
	Arch    string `json:"arch"`
	ScBase  string `json:"scbase"`
}

// parseScInstallRequest 解码请求并补齐默认值（线程数/架构/scbase 源），
// 并拒绝指向内网/环回/链路本地的下载源（SSRF 防御：安装器会代为发起 GET）。
func parseScInstallRequest(r *http.Request, rootDir string) (scInstallRequest, error) {
	var req scInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, err
	}
	if req.Threads <= 0 {
		req.Threads = 32
	}
	if req.Arch == "" {
		req.Arch = detectArch()
	}
	if req.ScBase == "" {
		req.ScBase = readScBase(rootDir)
	}
	if isForbiddenScBase(req.ScBase, req.Arch) {
		return req, fmt.Errorf("下载源指向内网/环回地址，已拒绝（防 SSRF）")
	}
	return req, nil
}

// isForbiddenScBase 判断下载源是否解析到禁止的网络目标。
// 规则：非 http(s) 协议、无主机名、localhost、环回/私网/链路本地/未指定 IP 一律拒绝。
func isForbiddenScBase(scBase, arch string) bool {
	u, err := url.Parse(scDownloadURL(scBase, arch))
	if err != nil {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return true
	}
	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // 域名（含内网域名）不在此拦，交由 DNS 不可控的现实约束；主要防裸 IP 滥用
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// defaultScBase 官方 nightly 源（nightly.link 代理，仅提供 zip）。
// 注意：该构建不支持 vless 分享链接解析，纯 vless 订阅会得到 "No nodes were found!"。
const defaultScBase = "https://nightly.link/tindy2013/subconverter/actions/runs/29030454713"

// recommendedScBase 推荐 vless 支持 fork 的发布模板源，实测可完整转换
// vless(ws+tls/none) 为 clash/singbox/surge 等；{arch} 由安装器替换。
const recommendedScBase = "https://github.com/asdlokj1qpi233/subconverter/releases/latest/download/subconverter_{arch}.tar.gz"

// readScBase 读取 scbase.txt 中的下载源：
//   - 支持 {arch} 占位符模板（推荐，直接指向任意 release 资产名）；
//   - 兼容旧行为：无占位符时视为 nightly.link 风格 base URL，由 scDownloadURL 补全。
//
// 文件缺失或为空时返回支持 vless 的默认源。
func readScBase(rootDir string) string {
	scbasePath := filepath.Join(rootDir, "scbase.txt")
	if data, err := os.ReadFile(scbasePath); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return v
		}
	}
	return recommendedScBase
}

// scDownloadURL 把 scbase 配置解析为实际下载地址：
//   - 含 {arch} 占位符 → 视为完整 URL 模板直接替换；
//   - 否则兼容旧的 nightly.link 风格（base + /subconverter_ARCH.zip）。
func scDownloadURL(scBase, arch string) string {
	if strings.Contains(scBase, "{arch}") {
		return strings.ReplaceAll(scBase, "{arch}", arch)
	}
	return strings.TrimRight(scBase, "/") + "/subconverter_" + arch + ".zip"
}

// installSubconverterArchive 事务化安装：下载 → 解压 → 归一化 → 校验全部成功后，
// 才原子替换旧目录；任一环节失败都不动现有安装（避免"先删后装"把好安装炸掉）。
// 返回实际使用的线程数（单线程回退时为 1）。
func installSubconverterArchive(downloadURL, destDir string, threads int) (int, error) {
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return 0, err
	}
	staging, err := os.MkdirTemp(parent, ".scinstall-")
	if err != nil {
		return 0, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	defer os.RemoveAll(staging)

	unpackDir := filepath.Join(staging, "unpacked")
	if err := os.MkdirAll(unpackDir, 0o755); err != nil {
		return 0, err
	}

	usedThreads := threads
	archivePath, err := DownloadSubconverter(downloadURL, staging, threads)
	if err != nil {
		// 分片下载失败，尝试单线程
		ui.Println(ui.WarnStyle, ui.SymWarn+" 分片下载失败，尝试单线程: "+err.Error())
		usedThreads = 1
		archivePath, err = singleThreadDownload(downloadURL, staging, downloadClient(5*time.Minute))
		if err != nil {
			return 0, fmt.Errorf("下载失败: %w", err)
		}
	}

	archiveFile := filepath.Join(staging, "sc_download.archive")
	if err := os.Rename(archivePath, archiveFile); err != nil {
		return 0, fmt.Errorf("移动下载档案失败: %w", err)
	}
	if err := extractArchive(unpackDir, archiveFile); err != nil {
		return 0, fmt.Errorf("解压失败: %w", err)
	}
	os.Remove(archiveFile)

	// 发布结构归一化：部分源（如 asdlokj1qpi233 fork tar.gz）把一切包在
	// 顶层 subconverter/ 子目录中，须整体上移一级，运行期以安装目录为工作目录。
	if err := flattenReleaseLayout(unpackDir); err != nil {
		return 0, err
	}

	// 安装完整性校验：必须存在体积合理的主机原生可执行镜像
	binPath := filepath.Join(unpackDir, "subconverter")
	if err := validateBinary(binPath); err != nil {
		return 0, err
	}
	// zip 条目的外部属性不含 Unix 权限位时解压默认不可执行，显式补齐
	if err := os.Chmod(binPath, 0o755); err != nil {
		return 0, fmt.Errorf("设置可执行权限失败: %w", err)
	}

	// 全部就绪，原子替换旧安装；旧目录先让位到 .old 以兼容跨设备/非空目录
	oldDir := destDir + ".old"
	os.RemoveAll(oldDir)
	if _, err := os.Lstat(destDir); err == nil {
		if err := os.Rename(destDir, oldDir); err != nil {
			return 0, fmt.Errorf("备份旧安装失败: %w", err)
		}
	}
	if err := os.Rename(unpackDir, destDir); err != nil {
		// 回滚：新装失败则恢复旧目录
		os.Rename(oldDir, destDir)
		return 0, fmt.Errorf("启用新安装失败: %w", err)
	}
	os.RemoveAll(oldDir)
	return usedThreads, nil
}

// flattenReleaseLayout 归一化解压产物布局。
// 当 destDir 直下没有 subconverter 可执行文件、而是被包进某个唯一子目录时，
// 将该子目录的所有条目上移一级（同名覆盖），保证 pref/base/rules 与主程序同层。
func flattenReleaseLayout(destDir string) error {
	direct := filepath.Join(destDir, "subconverter")
	if st, err := os.Stat(direct); err == nil && st.Mode().IsRegular() {
		return nil // 已是扁平布局
	}

	entries, err := os.ReadDir(destDir)
	if err != nil {
		return err
	}
	var inner string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".tmp" {
			if _, err := os.Stat(filepath.Join(destDir, e.Name(), "subconverter")); err == nil {
				inner = filepath.Join(destDir, e.Name())
				break
			}
		}
	}
	if inner == "" {
		return fmt.Errorf("发布包内未找到 subconverter 主程序目录")
	}

	// 先改名腾位：inner 的名字与最终二进制落点同名，直接原地搬运会自毁
	tmp := inner + ".flatten"
	if err := os.Rename(inner, tmp); err != nil {
		return fmt.Errorf("暂存发布目录失败: %w", err)
	}
	sub, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range sub {
		src := filepath.Join(tmp, e.Name())
		dst := filepath.Join(destDir, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			if err := os.RemoveAll(dst); err != nil {
				return fmt.Errorf("清理旧条目 %s 失败: %w", e.Name(), err)
			}
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("上移条目 %s 失败: %w", e.Name(), err)
		}
	}
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	// 上移后主程序可能仍是不可执行位（取决于来源档案），统一补齐
	os.Chmod(filepath.Join(destDir, "subconverter"), 0o755)
	return nil
}

// extractArchive 按档案魔数自动选择解压器：PK\x03\x04 → zip，\x1f\x8b → tar.gz。
func extractArchive(destDir, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		f.Close()
		return fmt.Errorf("读取档案头失败: %w", err)
	}
	f.Close()

	switch {
	case magic[0] == 'P' && magic[1] == 'K':
		return extractZip(destDir, archivePath)
	case magic[0] == 0x1f && magic[1] == 0x8b:
		return extractTarGz(destDir, archivePath)
	default:
		return fmt.Errorf("未知档案格式（既非 zip 也非 tar.gz）")
	}
}

// extractTarGz 安全解压 tar.gz 到目标目录（防路径穿越，显式保留执行位）。
func extractTarGz(destDir, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip 打开失败: %w", err)
	}
	defer gz.Close()

	destClean := filepath.Clean(destDir)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		cleaned := filepath.Clean(filepath.Join(destClean, hdr.Name))
		if cleaned != destClean && !strings.HasPrefix(cleaned, destClean+string(filepath.Separator)) {
			continue // 跳过穿越条目
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleaned, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", hdr.Name, err)
			}
			continue
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
				return fmt.Errorf("创建父目录 %s 失败: %w", filepath.Dir(hdr.Name), err)
			}
			out, err := os.OpenFile(cleaned, os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
				os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("创建条目 %s 失败: %w", hdr.Name, err)
			}
			written, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("写入条目 %s 失败: %w", hdr.Name, copyErr)
			}
			if closeErr != nil {
				return closeErr
			}
			if written != hdr.Size {
				return fmt.Errorf("条目 %s 大小不符（得 %d 应 %d）", hdr.Name, written, hdr.Size)
			}
		}
	}
}

// validateBinary 校验解压出的主程序体积与魔数（Linux/macOS ELF 或 Windows PE）。
func validateBinary(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("安装后未找到 subconverter 主程序")
	}
	if st.Size() < minArchiveSize/2 {
		return fmt.Errorf("subconverter 主程序仅 %dB，安装包可能损坏", st.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("读取主程序头失败: %w", err)
	}
	isELF := magic[0] == 0x7f && string(magic[1:]) == "ELF"
	isPE := magic[0] == 'M' && magic[1] == 'Z'
	if !isELF && !isPE {
		return fmt.Errorf("subconverter 主程序缺少可执行魔数（既非 ELF 也非 PE）")
	}
	return nil
}

// extractZip 安全解压 zip 到目标目录（Zip Slip 防路径穿越）。
func extractZip(destDir, zipPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	destClean := filepath.Clean(destDir)
	for _, f := range r.File {
		cleaned := filepath.Clean(filepath.Join(destClean, f.Name))
		// 清理后仍必须位于目标目录内，否则跳过恶意条目
		if cleaned != destClean && !strings.HasPrefix(cleaned, destClean+string(filepath.Separator)) {
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(cleaned, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(cleaned), 0o755)
		out, err := os.Create(cleaned)
		if err != nil {
			return fmt.Errorf("创建条目 %s 失败: %w", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return fmt.Errorf("打开条目 %s 失败: %w", f.Name, err)
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return fmt.Errorf("写入条目 %s 失败: %w", f.Name, copyErr)
		}
		// 写出体积必须与档案声明一致，防止损坏流产生半截文件
		if st, err := os.Stat(cleaned); err == nil && st.Size() != int64(f.UncompressedSize64) {
			return fmt.Errorf("条目 %s 大小不符（得 %d 应 %d），档案可能已损坏",
				f.Name, st.Size(), f.UncompressedSize64)
		}
	}
	return nil
}
