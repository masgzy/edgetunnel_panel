// flag.go - 国旗 emoji 处理。
//
// 原 Python flag.py 用一个 250+ 项的 emoji->中文名字典做正向替换。
// 实际上 flag emoji 由两个 regional indicator symbol 组成（🇺=U+1F1E6 ... 🇿=U+1F1FF），
// 可直接反推出 ISO 3166-1 alpha-2 国家码，再用一份 iso2->中文表查中文。
// 这样数据量小、可维护、且天然覆盖所有国家（含未来新增）。
package module

import (
	"regexp"
	"sort"
	"strings"
)

// regional indicator 范围：U+1F1E6 .. U+1F1FF
// 一个 flag emoji 由两个连续的 regional indicator 组成。
// 例如 🇯🇵 = U+1F1EF(U+1F1E6+'J'-'A') + U+1F1F5
// 反推：每个 regional indicator 减去 U+1F1E6 得到 0-25，对应 A-Z。

// iso2ToZh ISO 3166-1 alpha-2 -> 中文名。
// 数据源：维基百科 ISO 3166-1 官方简短中文名。
var iso2ToZh = map[string]string{
	"AD": "安道尔", "AE": "阿联酋", "AF": "阿富汗", "AG": "安提瓜和巴布达",
	"AI": "安圭拉", "AL": "阿尔巴尼亚", "AM": "亚美尼亚", "AO": "安哥拉",
	"AQ": "南极洲", "AR": "阿根廷", "AS": "美属萨摩亚", "AT": "奥地利",
	"AU": "澳大利亚", "AW": "阿鲁巴", "AX": "奥兰群岛", "AZ": "阿塞拜疆",
	"BA": "波黑", "BB": "巴巴多斯", "BD": "孟加拉国", "BE": "比利时",
	"BF": "布基纳法索", "BG": "保加利亚", "BH": "巴林", "BI": "布隆迪",
	"BJ": "贝宁", "BL": "圣巴泰勒米", "BM": "百慕大", "BN": "文莱",
	"BO": "玻利维亚", "BQ": "荷属加勒比区", "BR": "巴西", "BS": "巴哈马",
	"BT": "不丹", "BV": "布韦岛", "BW": "博茨瓦纳", "BY": "白俄罗斯",
	"BZ": "伯利兹", "CA": "加拿大", "CC": "科科斯群岛", "CD": "刚果(金)",
	"CF": "中非", "CG": "刚果(布)", "CH": "瑞士", "CI": "科特迪瓦",
	"CK": "库克群岛", "CL": "智利", "CM": "喀麦隆", "CN": "中国",
	"CO": "哥伦比亚", "CR": "哥斯达黎加", "CU": "古巴", "CV": "佛得角",
	"CW": "库拉索", "CX": "圣诞岛", "CY": "塞浦路斯", "CZ": "捷克",
	"DE": "德国", "DJ": "吉布提", "DK": "丹麦", "DM": "多米尼克",
	"DO": "多米尼加", "DZ": "阿尔及利亚", "EC": "厄瓜多尔", "EE": "爱沙尼亚",
	"EG": "埃及", "EH": "西撒哈拉", "ER": "厄立特里亚", "ES": "西班牙",
	"ET": "埃塞俄比亚", "FI": "芬兰", "FJ": "斐济", "FK": "福克兰群岛",
	"FM": "密克罗尼西亚", "FO": "法罗群岛", "FR": "法国", "GA": "加蓬",
	"GB": "英国", "GD": "格林纳达", "GE": "格鲁吉亚", "GF": "法属圭亚那",
	"GG": "根西岛", "GH": "加纳", "GI": "直布罗陀", "GL": "格陵兰",
	"GM": "冈比亚", "GN": "几内亚", "GP": "瓜德罗普", "GQ": "赤道几内亚",
	"GR": "希腊", "GS": "南乔治亚和南桑威奇群岛", "GT": "危地马拉",
	"GU": "关岛", "GW": "几内亚比绍", "GY": "圭亚那", "HK": "香港",
	"HM": "赫德岛和麦克唐纳群岛", "HN": "洪都拉斯", "HR": "克罗地亚",
	"HT": "海地", "HU": "匈牙利", "ID": "印度尼西亚", "IE": "爱尔兰",
	"IL": "以色列", "IM": "马恩岛", "IN": "印度", "IO": "英属印度洋领地",
	"IQ": "伊拉克", "IR": "伊朗", "IS": "冰岛", "IT": "意大利",
	"JE": "泽西岛", "JM": "牙买加", "JO": "约旦", "JP": "日本",
	"KE": "肯尼亚", "KG": "吉尔吉斯斯坦", "KH": "柬埔寨", "KI": "基里巴斯",
	"KM": "科摩罗", "KN": "圣基茨和尼维斯", "KP": "朝鲜", "KR": "韩国",
	"KW": "科威特", "KY": "开曼群岛", "KZ": "哈萨克斯坦", "LA": "老挝",
	"LB": "黎巴嫩", "LC": "圣卢西亚", "LI": "列支敦士登", "LK": "斯里兰卡",
	"LR": "利比里亚", "LS": "莱索托", "LT": "立陶宛", "LU": "卢森堡",
	"LV": "拉脱维亚", "LY": "利比亚", "MA": "摩洛哥", "MC": "摩纳哥",
	"MD": "摩尔多瓦", "ME": "黑山", "MF": "法属圣马丁", "MG": "马达加斯加",
	"MH": "马绍尔群岛", "MK": "北马其顿", "ML": "马里", "MM": "缅甸",
	"MN": "蒙古", "MO": "澳门", "MP": "北马里亚纳群岛", "MQ": "马提尼克",
	"MR": "毛里塔尼亚", "MS": "蒙特塞拉特", "MT": "马耳他", "MU": "毛里求斯",
	"MV": "马尔代夫", "MW": "马拉维", "MX": "墨西哥", "MY": "马来西亚",
	"MZ": "莫桑比克", "NA": "纳米比亚", "NC": "新喀里多尼亚", "NE": "尼日尔",
	"NF": "诺福克岛", "NG": "尼日利亚", "NI": "尼加拉瓜", "NL": "荷兰",
	"NO": "挪威", "NP": "尼泊尔", "NR": "瑙鲁", "NU": "纽埃",
	"NZ": "新西兰", "OM": "阿曼", "PA": "巴拿马", "PE": "秘鲁",
	"PF": "法属波利尼西亚", "PG": "巴布亚新几内亚", "PH": "菲律宾",
	"PK": "巴基斯坦", "PL": "波兰", "PM": "圣皮埃尔和密克隆", "PN": "皮特凯恩群岛",
	"PR": "波多黎各", "PS": "巴勒斯坦", "PT": "葡萄牙", "PW": "帕劳",
	"PY": "巴拉圭", "QA": "卡塔尔", "RE": "留尼汪", "RO": "罗马尼亚",
	"RS": "塞尔维亚", "RU": "俄罗斯", "RW": "卢旺达", "SA": "沙特阿拉伯",
	"SB": "所罗门群岛", "SC": "塞舌尔", "SD": "苏丹", "SE": "瑞典",
	"SG": "新加坡", "SH": "圣赫勒拿", "SI": "斯洛文尼亚", "SJ": "斯瓦尔巴和扬马延",
	"SK": "斯洛伐克", "SL": "塞拉利昂", "SM": "圣马力诺", "SN": "塞内加尔",
	"SO": "索马里", "SR": "苏里南", "SS": "南苏丹", "ST": "圣多美和普林西比",
	"SV": "萨尔瓦多", "SX": "荷属圣马丁", "SY": "叙利亚", "SZ": "斯威士兰",
	"TC": "特克斯和凯科斯群岛", "TD": "乍得", "TF": "法属南部领地",
	"TG": "多哥", "TH": "泰国", "TJ": "塔吉克斯坦", "TK": "托克劳",
	"TL": "东帝汶", "TM": "土库曼斯坦", "TN": "突尼斯", "TO": "汤加",
	"TR": "土耳其", "TT": "特立尼达和多巴哥", "TV": "图瓦卢", "TW": "中国台湾",
	"TZ": "坦桑尼亚", "UA": "乌克兰", "UG": "乌干达", "UM": "美属外围小岛",
	"US": "美国", "UY": "乌拉圭", "UZ": "乌兹别克斯坦", "VA": "梵蒂冈",
	"VC": "圣文森特和格林纳丁斯", "VE": "委内瑞拉", "VG": "英属维尔京群岛",
	"VI": "美属维尔京群岛", "VN": "越南", "VU": "瓦努阿图", "WF": "瓦利斯和富图纳",
	"WS": "萨摩亚", "YE": "也门", "YT": "马约特", "ZA": "南非",
	"ZM": "赞比亚", "ZW": "津巴布韦",
	// 特殊：欧盟
	"EU": "欧盟",
}

// 特殊旗帜（非 regional indicator pair）
var specialFlags = map[string]string{
	"🏴󠁧󠁢󠁥󠁮󠁧󠁿": "英格兰",
	"🏴󠁧󠁢󠁳󠁣󠁴󠁿": "苏格兰",
	"🏴󠁧󠁢󠁷󠁬󠁳󠁿": "威尔士",
}

var numSuffixRe = regexp.MustCompile(`^\(\d+\)$`)

// twFlag / cnFlag TW 区域旗帜与中国的旗帜 emoji。
// 按需求约定：TW 区域一律使用中国旗帜展示，避免出现 TW 旗帜 emoji。
const (
	twFlag = "\U0001F1F9\U0001F1FC"
	cnFlag = "\U0001F1E8\U0001F1F3"
)

// occurrence 记录单个 flag emoji 在文本里的一次出现。
type occurrence struct {
	flag    string
	country string
	pos     int
}

// AddFlagEmoji 把文本中的国旗 emoji 替换为 "[emoji] 国家中文名" 格式。
// 规则与原 Python flag.add 保持一致：
//   - 若该国家名已存在于文本中，不重复添加
//   - 若 emoji 出现在末尾（其后仅可能跟 (N) 数字后缀），保留不替换
//   - 否则在 emoji 后追加 " 国家中文名"
func AddFlagEmoji(text string) string {
	// 需求约定：TW 区域旗帜统一替换为中国旗帜，并直接标注「中国台湾」；
	// 后续 CN 旗帜的国家名标注因文本已含「中国」子串而自动跳过，不重复。
	text = strings.ReplaceAll(text, twFlag, cnFlag+" 中国台湾")

	// 先处理特殊旗帜
	for flag, country := range specialFlags {
		if !strings.Contains(text, flag) {
			continue
		}
		if strings.Contains(text, country) {
			// 已有国家名，不重复
			continue
		}
		text = appendCountryAfterFlag(text, flag, country)
	}

	// 处理 regional indicator pair
	// 找出文本里所有 flag emoji（两个连续 regional indicator）
	pairs := findAllFlagEmojis(text)
	// 每个 emoji 可能出现多次，从后向前处理避免索引错乱
	var all []occurrence
	for _, p := range pairs {
		country, ok := iso2ToZh[p.code]
		if !ok {
			continue
		}
		// 找出该 flag 在 text 中所有位置
		start := 0
		for {
			idx := strings.Index(text[start:], p.flag)
			if idx == -1 {
				break
			}
			pos := start + idx
			all = append(all, occurrence{flag: p.flag, country: country, pos: pos})
			start = pos + len(p.flag)
		}
	}
	// 按 pos 降序处理（从后向前）
	sort.Slice(all, func(i, j int) bool { return all[i].pos > all[j].pos })

	for _, oc := range all {
		if strings.Contains(text, oc.country) {
			// 文本里已经有这个国家中文名，跳过
			continue
		}
		// 判断是否为"最后一个 emoji 且在末尾"
		// 末尾定义：其后仅可能为空或 (N) 数字后缀
		after := text[oc.pos+len(oc.flag):]
		afterTrim := strings.TrimSpace(after)
		isAtEnd := false
		if afterTrim == "" {
			isAtEnd = true
		} else if numSuffixRe.MatchString(afterTrim) {
			isAtEnd = true
		}
		if isAtEnd {
			// 检查是否是文本里最后一个 flag emoji
			// 简单判断：oc.pos 是所有 occurrences 里最大的
			if isLastOccurrence(all, oc) {
				continue
			}
		}
		// 在 emoji 后插入 " 国家中文名"
		text = text[:oc.pos+len(oc.flag)] + " " + oc.country + text[oc.pos+len(oc.flag):]
	}
	return text
}

// isLastOccurrence 判断某旗帜出现处是否为最后一次（用于去重后置标注）。
func isLastOccurrence(all []occurrence, oc occurrence) bool {
	maxPos := -1
	for _, o := range all {
		if o.pos > maxPos {
			maxPos = o.pos
		}
	}
	return oc.pos == maxPos
}

// appendCountryAfterFlag 在旗帜后附加国家名（去重时保留首处完整标注）。
func appendCountryAfterFlag(text, flag, country string) string {
	// 与主逻辑类似：仅在 emoji 后追加 " 国家"
	// 这里简单实现：所有出现位置都追加（特殊旗帜数量少，原 Python 也是简单替换）
	for {
		idx := strings.Index(text, flag)
		if idx == -1 {
			break
		}
		text = text[:idx+len(flag)] + " " + country + text[idx+len(flag):]
		// 跳过刚插入的部分避免无限循环
		_ = idx
		break // 简化：只处理第一个
	}
	return text
}

type flagCodePair struct {
	flag string
	code string
}

// findAllFlagEmojis 扫描文本，返回所有出现的 flag emoji（去重）。
// flag emoji = 两个连续的 regional indicator symbol（U+1F1E6..U+1F1FF）。
func findAllFlagEmojis(text string) []flagCodePair {
	runes := []rune(text)
	seen := map[string]bool{}
	var result []flagCodePair
	for i := 0; i+1 < len(runes); i++ {
		a := runes[i]
		b := runes[i+1]
		ca, oka := regionalToLetter(a)
		cb, okb := regionalToLetter(b)
		if oka && okb {
			code := string(ca) + string(cb)
			if !seen[code] {
				seen[code] = true
				// 重建 emoji 字符串
				flag := string(a) + string(b)
				result = append(result, flagCodePair{flag: flag, code: code})
			}
		}
	}
	return result
}

// regionalToLetter 把 regional indicator symbol 转成 A-Z 字母。
// U+1F1E6 -> A, ..., U+1F1FF -> Z
func regionalToLetter(r rune) (byte, bool) {
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return byte('A' + (r - 0x1F1E6)), true
	}
	return 0, false
}
