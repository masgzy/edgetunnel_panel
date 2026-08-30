// userinfo.go - 生成 Subscription-Userinfo 头。
// 对应原 Python module/userinfo.py。
package module

import (
	"fmt"
	"time"
)

// cstZone 中国标准时间（UTC+8，无夏令时）。
// 使用 FixedZone 而非 time.LoadLocation：容器/精简镜像/部分 Windows 环境可能
// 缺少 tzdata，LoadLocation 失败会静默回退 UTC，导致日期数按 UTC 日期计算。
var cstZone = time.FixedZone("CST", 8*3600)

// UserinfoConfig userinfo 模块依赖。
type UserinfoConfig struct {
	Expire string // "2030-01-01 00:00:00+08:00"
}

// GetUserinfo 根据当前时间生成符合订阅规范的 userinfo 字符串。
//
// upload/download 用日期数；total=时间+日期；expire=配置过期时间戳。
// 原 Python 实现：num = 时分秒 * 1G（若 >1024），num1 = 日期数 * 1G（若 >1024）。
func GetUserinfo(expireStr string) string {
	return getUserinfo(expireStr, 0, 0, 0, false)
}

// GetUserinfoWithUsage 基于面板上报的真实用量生成 userinfo。
//
// 对齐 EDT-Pages 面板 config.json 的 CF.Usage 字段语义：
// pages→upload、workers→download、max→总配额（与原实现一致的 GB 量纲，
// 客户端订阅页据此展示已用/总量进度条）。不存在真实用量时请使用 GetUserinfo。
func GetUserinfoWithUsage(expireStr string, usage Usage) string {
	if !usage.Valid {
		return GetUserinfo(expireStr)
	}
	return getUserinfo(expireStr, usage.Pages, usage.Workers, usage.Max, true)
}

// Usage CF 用量数值集合。
type Usage struct {
	Pages   int64
	Workers int64
	Max     int64
	Valid   bool
}

// getUserinfo userinfo 数值合成主体；useReal 表示使用面板真实用量。
// 伪造路径保持与原 Python 版逐位一致，便于回归对照。
func getUserinfo(expireStr string, pages, workers, max int64, useReal bool) string {
	dt, err := time.Parse("2006-01-02 15:04:05-07:00", expireStr)
	if err != nil {
		// 尝试其他时区格式
		for _, layout := range []string{
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05",
			"2006-01-02 15:04:05 MST",
		} {
			if dt, err = time.Parse(layout, expireStr); err == nil {
				break
			}
		}
	}
	var timestamp int64
	if err == nil {
		timestamp = dt.Unix()
	}

	now := time.Now().In(cstZone)

	GB := int64(1024 * 1024 * 1024)
	if useReal {
		pagesSum := pages * GB
		workersSum := workers * GB
		total := max * GB
		if total <= 0 {
			total = pagesSum + workersSum
		}
		return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
			pagesSum, workersSum, total, timestamp)
	}

	timeInt := atoi(now.Format("150405"))
	dateInt := atoi(now.Format("060102"))

	var num, num1 int64
	if timeInt > 1024 {
		num = int64(timeInt) * GB
	}
	if dateInt > 1024 {
		num1 = int64(dateInt) * GB
	}

	pagesSum := num1 / 2
	workersSum := num1 / 2
	total := num + num1

	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		pagesSum, workersSum, total, timestamp)
}

// atoi 宽松字符串转整数（非数字返回 0）。
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	return n
}
