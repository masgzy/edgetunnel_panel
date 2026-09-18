package service

import (
	"encoding/csv"
	"os"
	"strconv"
	"strings"
	"sync"

	"edt/internal/module"
)

// ResultRow 测速结果行。
type ResultRow struct {
	IP    string
	Send  int
	Res   int
	PLR   float64
	Ping  float64
	Speed float64
	Code  string
}

// ResultStore 管理 data/result.csv。
type ResultStore struct {
	mu       sync.Mutex // 保护 filePath 与文件读写
	filePath string
}

// NewResultStore 构造。
func NewResultStore(filePath string) *ResultStore {
	return &ResultStore{filePath: filePath}
}

// Reload 热更新 result.csv 路径（配置重载时调用；低频，读写风格与构造约定一致）。
func (r *ResultStore) Reload(filePath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filePath = filePath
}

// readRows 读取并解析结果文件全部行。
func (r *ResultStore) readRows() ([]ResultRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.Open(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	// 跳过表头
	if _, err := reader.Read(); err != nil {
		return nil, nil
	}
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	rows := make([]ResultRow, 0, len(records))
	for _, rec := range records {
		if len(rec) < 7 {
			continue
		}
		send, _ := strconv.Atoi(rec[1])
		res, _ := strconv.Atoi(rec[2])
		plr, _ := strconv.ParseFloat(rec[3], 64)
		ping, _ := strconv.ParseFloat(rec[4], 64)
		speed, _ := strconv.ParseFloat(rec[5], 64)
		rows = append(rows, ResultRow{
			IP:    rec[0],
			Send:  send,
			Res:   res,
			PLR:   plr,
			Ping:  ping,
			Speed: speed,
			Code:  rec[6],
		})
	}
	return rows, nil
}

// GetFirst 返回第一行的可 JSON 序列化结构。
func (r *ResultStore) GetFirst() (map[string]interface{}, error) {
	rows, err := r.readRows()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoData
	}
	row := rows[0]
	return map[string]interface{}{
		"ip":    row.IP,
		"send":  row.Send,
		"res":   row.Res,
		"plr":   row.PLR,
		"ping":  row.Ping,
		"speed": row.Speed,
		"code":  row.Code,
	}, nil
}

// GetFirstIP 返回第一行的 IP。
func (r *ResultStore) GetFirstIP() string {
	rows, err := r.readRows()
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].IP
}

// ProxyEntry 供 id=3 订阅构建使用。
type ProxyEntry struct {
	Name string
	Code string // 原始区域码（cfcolo / 机场码），供分区域 ProxyIP 匹配
	IP   string
	YxIP string
}

// GetAllAsProxyEntries 把 result.csv 转换为 id=3 订阅所需的节点列表。
// 名称 = pre + 区域码；按 flag 开关补旗（三字码 / 二字码），
// TW 区域码按约定使用中国旗帜（见 module.AddFlagByName）。
func (r *ResultStore) GetAllAsProxyEntries(pre, ip string, opt module.FlagOptions) ([]ProxyEntry, error) {
	rows, err := r.readRows()
	if err != nil {
		return nil, err
	}
	entries := make([]ProxyEntry, 0, len(rows))
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.Code))
		// 先对纯区域码补旗（保证 code 本身作为独立 token 可被识别），
		// 再拼 pre 前缀并补中文名，与旧行为（HKG/NRT 带旗）保持兼容。
		name := code
		if opt.IATA || opt.ISO2 {
			name = module.AddFlagByName(code, opt)
		}
		name = module.AddFlagEmoji(pre + name)
		entries = append(entries, ProxyEntry{
			Name: name,
			Code: code,
			IP:   ip,
			YxIP: row.IP,
		})
	}
	return entries, nil
}
