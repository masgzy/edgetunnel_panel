// flag_code.go - 按名称中的区域码补全国旗 emoji。
//
// 与 flag.go 的「已有旗帜补中文名」相反，本文件处理的是
// 节点名称本身不含旗帜、但含有区域码（机场三字码 / cfcolo / ISO 码）的场景：
//
//	"HKG-01"   -> "🇭🇰 HKG-01"
//	"ICN 02"   -> "🇰🇷 ICN 02"
//	"LAX 01"   -> "🇺🇸 LAX 01"
//	"US 01"    -> "🇺🇸 US 01"      （flag.iso2 开启时）
//	"TPE-03"   -> "🇨🇳 TPE-03"     （TW 区域按约定使用中国旗帜）
//
// 匹配以「字母 token」为单位（连续 A-Za-z 序列），
// 天然避开子串误匹配（busy 里的 us、INDIA 里的 IN 均不会命中）。
package module

import (
	"strings"
)

// FlagOptions 区域码补旗开关（对应 config.yml 的 flag 节）。
type FlagOptions struct {
	IATA bool // 三字码：机场码 / cfcolo / ISO alpha-3
	ISO2 bool // 二字码：ISO 3166-1 alpha-2（存在误匹配风险，默认关闭）
}

// iataToISO2 三字码 -> ISO 3166-1 alpha-2 国家/地区码。
// 覆盖：常见机场码（与 Cloudflare cfcolo 同源）、常用 ISO alpha-3 国家码、
// 少数习惯别名（UK 等，二字码表处理）。TW 区域码按约定归入 CN（中国旗帜）。
var iataToISO2 = map[string]string{
	// ---- 东亚 / 东南亚 ----
	"HKG": "HK", "MFM": "MO", "TPE": "CN", "KHH": "CN", "RMQ": "CN",
	"TNN": "CN", "TSA": "CN", "MZG": "CN", "TWN": "CN",
	"NRT": "JP", "HND": "JP", "KIX": "JP", "ITM": "JP", "CTS": "JP",
	"FUK": "JP", "OKA": "JP", "NGO": "JP", "JPN": "JP",
	"ICN": "KR", "GMP": "KR", "PUS": "KR", "CJU": "KR", "KOR": "KR",
	"PEK": "CN", "PKX": "CN", "PVG": "CN", "SHA": "CN", "CAN": "CN",
	"SZX": "CN", "CTU": "CN", "TFU": "CN", "HGH": "CN", "NKG": "CN",
	"XIY": "CN", "CKG": "CN", "WUH": "CN", "CSX": "CN", "XMN": "CN",
	"TAO": "CN", "DLC": "CN", "SYX": "CN", "HRB": "CN", "URC": "CN",
	"KMG": "CN", "CHN": "CN",
	"SIN": "SGP", "SGP": "SGP", "BKK": "TH", "DMK": "TH", "CNX": "TH",
	"THA": "TH", "KUL": "MY", "MYS": "MY", "CGK": "ID", "DPS": "ID",
	"IDN": "ID", "MNL": "PH", "CEB": "PH", "PHL": "PH",
	"SGN": "VN", "HAN": "VN", "VNM": "VN", "PNH": "KH", "RGN": "MM",
	"VTE": "LA",
	// ---- 南亚 / 中亚 ----
	"DEL": "IN", "BOM": "IN", "MAA": "IN", "BLR": "IN", "HYD": "IN",
	"IND": "IN", "CMB": "LK", "MLE": "MV", "KTM": "NP", "DAC": "BD",
	"ISB": "PK", "KHI": "PK", "ALA": "KZ", "NQZ": "KZ", "TAS": "UZ",
	"FRU": "KG", "DYU": "TJ", "ASB": "TM",
	// ---- 中东 ----
	"DXB": "AE", "AUH": "AE", "ARE": "AE", "DOH": "QA", "RUH": "SA",
	"JED": "SA", "SAU": "SA", "KWI": "KW", "BAH": "BH", "MCT": "OM",
	"AMM": "JO", "BEY": "LB", "TLV": "IL", "IKA": "IR", "IST": "TR",
	"SAW": "TR", "TUR": "TR",
	// ---- 欧洲 ----
	"LHR": "GB", "LGW": "GB", "MAN": "GB", "EDI": "GB", "GBR": "GB",
	"CDG": "FR", "ORY": "FR", "NCE": "FR", "FRA": "DE", "MUC": "DE",
	"BER": "DE", "HAM": "DE", "DEU": "DE", "AMS": "NL", "NLD": "NL",
	"BRU": "BE", "LUX": "LU", "ZRH": "CH", "GVA": "CH", "VIE": "AT",
	"AUT": "AT", "MAD": "ES", "BCN": "ES", "ESP": "ES", "LIS": "PT",
	"FCO": "IT", "MXP": "IT", "LIN": "IT", "ITA": "IT", "ATH": "GR",
	"DUB": "IE", "CPH": "DK", "ARN": "SE", "OSL": "NO", "HEL": "FI",
	"KEF": "IS", "WAW": "PL", "PRG": "CZ", "BUD": "HU", "OTP": "RO",
	"SOF": "BG", "BEG": "RS", "ZAG": "HR", "SJJ": "BA", "SKG": "SK",
	"LJU": "SI", "TLL": "EE", "RIX": "LV", "VNO": "LT", "SVO": "RU",
	"DME": "RU", "LED": "RU", "RUS": "RU",
	// ---- 北美 ----
	"JFK": "US", "EWR": "US", "LAX": "US", "SJC": "US", "SFO": "US",
	"SEA": "US", "ORD": "US", "DFW": "US", "IAH": "US", "MIA": "US",
	"ATL": "US", "BOS": "US", "IAD": "US", "DCA": "US", "DEN": "US",
	"PHX": "US", "LAS": "US", "MSP": "US", "DTW": "US", "SLC": "US",
	"USA": "US",
	"YVR": "CA", "YYZ": "CA", "YUL": "CA", "YYC": "CA",
	"MEX": "MX", "GDL": "MX",
	// ---- 南美 / 大洋洲 ----
	"GRU": "BR", "GIG": "BR", "BSB": "BR", "BRA": "BR", "EZE": "AR",
	"SCL": "CL", "LIM": "PE", "BOG": "CO", "MVD": "UY", "ASU": "PY",
	"SYD": "AU", "MEL": "AU", "BNE": "AU", "PER": "AU", "AUS": "AU",
	"AKL": "NZ", "WLG": "NZ", "NZL": "NZ", "NAN": "FJ",
	// ---- 非洲 ----
	"JNB": "ZA", "CPT": "ZA", "ZAF": "ZA", "LOS": "NG", "ACC": "GH",
	"NBO": "KE", "ADD": "ET", "CAI": "EG", "EGY": "EG", "CMN": "MA",
	"ALG": "DZ", "TUN": "TN", "ABV": "NG", "DAR": "TZ",
}

// iso2Alias 二字码别名表（非 ISO 官方码的习惯写法 -> 官方码）。
var iso2Alias = map[string]string{
	"UK": "GB", // 习惯别名：英国官方码为 GB
}

// codeToISO2 把任意区域码归一化为 ISO alpha-2：
//  1. 二字码别名（UK -> GB）；
//  2. 三字码表（机场码 / alpha-3）；
//  3. 本身就是合法二字码（在 iso2ToZh 中存在）。
//
// 找不到返回空串。TW 相关码在 iata 表内已归入 CN；
// 二字码 TW 的旗帜映射在 flagForISO2 内特判。
func codeToISO2(code string) string {
	if code == "" {
		return ""
	}
	if len(code) == 2 {
		if iso2Alias[code] != "" {
			return iso2Alias[code]
		}
		if _, ok := iso2ToZh[code]; ok {
			return code
		}
		return ""
	}
	if len(code) == 3 {
		if v, ok := iataToISO2[code]; ok && v != "" {
			return v
		}
	}
	return ""
}

// flagForISO2 由 ISO alpha-2 生成旗帜 emoji（两个 regional indicator）。
// TW 按约定返回中国旗帜（避免出现 TW 旗帜 emoji）。
func flagForISO2(iso2 string) string {
	if iso2 == "TW" {
		return cnFlag
	}
	rs := []rune(iso2)
	if len(rs) != 2 {
		return ""
	}
	for _, r := range rs {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return string([]rune{0x1F1E6 + rune(rs[0]-'A'), 0x1F1E6 + rune(rs[1]-'A')})
}

// FlagForCode 返回区域码对应的旗帜 emoji（可带空格分隔的展示用）。
// 未知码返回空串。TW / TPE / TWN 等 TW 区域码统一返回中国旗帜。
func FlagForCode(code string) string {
	return flagForISO2(codeToISO2(strings.ToUpper(strings.TrimSpace(code))))
}

// LetterTokens 抽取文本中的全部字母 token（连续 A-Za-z 序列，返回大写）。
// 供名称区域码匹配与 proxyip 分区域查找共用；中文、数字、符号均为分隔。
func LetterTokens(text string) []string {
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToUpper(string(cur)))
			cur = cur[:0]
		}
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// regionMatch 名称里第一个可识别的区域码及其在原文中的起始字节位置。
// code 为空表示未命中。
type regionMatch struct {
	code  string // 原样大写的码（可能是三字码也可能是二字码）
	start int    // 在原文中的字节偏移
}

// findRegion 按开关扫描名称：三字码优先（更具体），其次二字码。
// opt.IATA / opt.ISO2 均关闭时直接未命中。
func findRegion(text string, opt FlagOptions) regionMatch {
	tokens := letterTokenSpans(text)
	// 第一轮：三字码（机场码 / cfcolo / alpha-3）
	if opt.IATA {
		for _, t := range tokens {
			if len(t.token) != 3 {
				continue
			}
			if codeToISO2(t.token) != "" {
				return regionMatch{code: t.token, start: t.start}
			}
		}
	}
	// 第二轮：二字码（ISO alpha-2 + 别名）
	if opt.ISO2 {
		for _, t := range tokens {
			if len(t.token) != 2 {
				continue
			}
			if codeToISO2(t.token) != "" {
				return regionMatch{code: t.token, start: t.start}
			}
		}
	}
	return regionMatch{}
}

// tokenSpan 字母 token 及其在原文中的起始字节位置。
type tokenSpan struct {
	token string
	start int
}

// letterTokenSpans 扫描文本中的 ASCII 字母 token（连续 A-Za-z 序列，返回大写）
// 及其起始字节位置。中文/数字/符号/多字节字符均为分隔符。
// 位置为字节偏移，可直接用于字符串插入。
func letterTokenSpans(text string) []tokenSpan {
	var tokens []tokenSpan
	start := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isLetter {
			if start == -1 {
				start = i
			}
		} else if start != -1 {
			tokens = append(tokens, tokenSpan{token: strings.ToUpper(text[start:i]), start: start})
			start = -1
		}
	}
	if start != -1 {
		tokens = append(tokens, tokenSpan{token: strings.ToUpper(text[start:]), start: start})
	}
	return tokens
}

// FindRegionCode 返回文本中首个可识别的区域码（大写；未命中为空串）。
// 供 proxyip 分区域匹配使用：IATA 与 ISO2 任一开启即生效。
func FindRegionCode(text string, opt FlagOptions) string {
	return findRegion(text, opt).code
}

// containsFlagEmoji 报告文本中是否已含任意旗帜 emoji（regional indicator 对）。
func containsFlagEmoji(text string) bool {
	for _, r := range text {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			return true
		}
	}
	return false
}

// AddFlagByName 按名称中的区域码补全国旗（名称已含旗帜时不重复添加）。
// 旗帜插入在命中的码之前并跟随一个空格，例如
// "美国 LAX" -> "美国 🇺🇸 LAX"、"HKG-01" -> "🇭🇰 HKG-01"；
// 命中 TW 区域码时使用中国旗帜（见 flagForISO2）。
func AddFlagByName(text string, opt FlagOptions) string {
	if text == "" || (!opt.IATA && !opt.ISO2) {
		return text
	}
	if containsFlagEmoji(text) {
		return text
	}
	m := findRegion(text, opt)
	if m.code == "" {
		return text
	}
	flag := flagForISO2(codeToISO2(m.code))
	if flag == "" {
		return text
	}
	return text[:m.start] + flag + " " + text[m.start:]
}
