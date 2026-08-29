// ws.go - Go 原生 WebSocket 实时日志推送（gorilla/websocket）。
//
// 设计要点：
//   - 不再监听独立的 3003 端口，/ws 直接挂在主 HTTP mux 上（同源、可鉴权）；
//   - hub 广播不直接 WriteMessage：每个客户端有带缓冲的发送队列和独立写协程，
//     串行化单连接写操作，规避 gorilla/websocket 禁止并发写的竞态；
//   - ping/pong 保活 + 读超时，快速回收死连接；
//   - 日志文件超过阈值自动截断，防止 /tmp/edt.log 无限增长。
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WS 与日志文件参数。
const (
	// 日志随数据目录走（相对工作目录），不再依赖 /tmp——Windows 无此目录且易被系统清理。
	logFilePath     = "data/panel.log"
	logMaxSize      = 8 << 20 // 超过 8MB 截断
	wsWriteWait     = 10 * time.Second
	wsPongWait      = 60 * time.Second
	wsPingPeriod    = 30 * time.Second
	wsSendQueueSize = 64
)

// wsUpgrader WS 升级器；CheckOrigin 仅允许同源（防跨站 WS 劫持）。
var wsUpgrader = websocket.Upgrader{
	// 同源策略：Origin 缺失（非浏览器客户端）或与 Host 同源才放行
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// wsClient 单个已连接的 WebSocket 客户端。
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
}

// wsHub 管理全部客户端，广播经各客户端队列串行投递。
type wsHub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

// hub 全局广播中心（进程唯一）。
var hub = &wsHub{clients: make(map[*wsClient]struct{})}

// add 注册客户端。
func (h *wsHub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// remove 注销客户端并回收其发送队列。
func (h *wsHub) remove(c *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.done)
	}
	h.mu.Unlock()
}

// broadcast 向所有客户端入队；队列满视为慢消费者，直接踢除。
func (h *wsHub) broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			go h.remove(c)
		}
	}
}

// newClient 组装单个 WS 客户端：带缓冲发送队列 + 独立写协程。
func newClient(conn *websocket.Conn) *wsClient {
	return &wsClient{
		conn: conn,
		send: make(chan []byte, wsSendQueueSize),
		done: make(chan struct{}),
	}
}

// writePump 每连接唯一写协程：串行处理 send 队列与 ping。
func writePump(c *wsClient) {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				hub.remove(c)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				hub.remove(c)
				return
			}
		}
	}
}

// wsHandler GET /ws - 升级连接并推送实时日志（需在路由注册时过鉴权中间件）。
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := newClient(conn)
	hub.add(c)
	go writePump(c)

	// 初始推送最近日志
	initMsg := readLogTail(200)
	select {
	case c.send <- append([]byte(`{"type":"init","data":`), logJSONString(initMsg)...):
	default:
	}

	// 读泵：心跳超时 / 对端关闭时清理
	go func() {
		defer hub.remove(c)
		conn.SetReadLimit(4096)
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// logJSONString 把字符串编码为 JSON 字符串字面量（含引号）。
func logJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// readLogTail 读取日志文件最后 N 行。
func readLogTail(n int) string {
	data, err := os.ReadFile(logFilePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n <= 0 || len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// LogLine 追加一条带时间戳的日志到日志文件。
// 由 WatchLogFile 的轮询在 1s 内读取并广播给所有 WS 客户端（设置页实时日志）。
// 首次调用会自动创建日志文件（WatchLogFile 依赖文件存在才开始监听）。
func LogLine(format string, args ...interface{}) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...) + "\n"
	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// data 目录可能不存在（首次在干净目录启动），创建后重试一次
		if mkErr := os.MkdirAll(filepath.Dir(logFilePath), 0o755); mkErr == nil {
			f, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		}
	}
	if err == nil {
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
}

// WatchLogFile 监听日志文件变化，变化时广播追加内容；文件过大自动截断。
// 由 main 启动一次，常驻运行。
func WatchLogFile() {
	var lastSize int64

	// 等待文件出现
	for i := 0; i < 30; i++ {
		if info, err := os.Stat(logFilePath); err == nil {
			lastSize = info.Size()
			break
		}
		time.Sleep(1 * time.Second)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		info, err := os.Stat(logFilePath)
		if err != nil {
			continue
		}
		switch {
		case info.Size() == lastSize:
			continue
		case info.Size() > logMaxSize && lastSize <= logMaxSize:
			// 先推送新增，随后截断并重置游标
			pushNewLines(&lastSize, info.Size())
			truncateLogKeepTail(1000)
			lastSize = fileSize(logFilePath)
			continue
		case info.Size() < lastSize:
			lastSize = 0 // 文件被截断/重建
		}
		pushNewLines(&lastSize, info.Size())
	}
}

// pushNewLines 读取 [from,size) 区间内容逐行广播，并推进 *lastSize。
func pushNewLines(lastSize *int64, size int64) {
	f, err := os.Open(logFilePath)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(*lastSize, 0); err != nil {
		return
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		msg, _ := json.Marshal(map[string]string{"type": "append", "data": line + "\n"})
		hub.broadcast(msg)
	}
	*lastSize = size
}

// truncateLogKeepTail 截断日志仅保留最后 keep 行。
func truncateLogKeepTail(keep int) {
	tail := readLogTail(keep)
	tmp := logFilePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(tail+"\n"), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, logFilePath)
}

// fileSize 返回文件字节数（不存在时 0）。
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
