// Package ui 提供 Catppuccin Mocha/Latte 双口味终端配色。
// 基于 lipgloss 的 AdaptiveColor 实现深浅自适应。
package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Catppuccin 配色（Mocha 深色 / Latte 浅色）。
var (
	// 标题/强调 - 紫色加粗
	Mauve = lipgloss.AdaptiveColor{Light: "#8839ef", Dark: "#cba6f7"}
	// 正文（少用，多数用 Dim）
	Text = lipgloss.AdaptiveColor{Light: "#4c4f69", Dark: "#cdd6f4"}
	// 暗淡 - 次要信息
	Subtext = lipgloss.AdaptiveColor{Light: "#6c6f85", Dark: "#a6adc8"}
	// 成功 - 绿色加粗
	Green = lipgloss.AdaptiveColor{Light: "#40a02b", Dark: "#a6e3a1"}
	// 警告 - 橙色加粗
	Peach = lipgloss.AdaptiveColor{Light: "#fe640b", Dark: "#fab387"}
	// 错误 - 红色加粗
	Red = lipgloss.AdaptiveColor{Light: "#d20f39", Dark: "#f38ba8"}
	// 路径/URL - 蓝色
	Blue = lipgloss.AdaptiveColor{Light: "#1e66f5", Dark: "#89b4fa"}
	// 数字/高亮 - 黄色（仅数字，不当正文）
	Yellow = lipgloss.AdaptiveColor{Light: "#df8e1d", Dark: "#f9e2af"}
	// 信息 - 天蓝
	Sky = lipgloss.AdaptiveColor{Light: "#04a5e5", Dark: "#89dceb"}
	// 关键字 - 粉色加粗（选项编号、参数名）
	Pink = lipgloss.AdaptiveColor{Light: "#ea76cb", Dark: "#f5c2e7"}
)

// 样式集合
var (
	TitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(Mauve)
	BodyStyle    = lipgloss.NewStyle().Foreground(Text)
	DimStyle     = lipgloss.NewStyle().Foreground(Subtext)
	SuccessStyle = lipgloss.NewStyle().Bold(true).Foreground(Green)
	WarnStyle    = lipgloss.NewStyle().Bold(true).Foreground(Peach)
	ErrorStyle   = lipgloss.NewStyle().Bold(true).Foreground(Red)
	PathStyle    = lipgloss.NewStyle().Foreground(Blue)
	NumberStyle  = lipgloss.NewStyle().Foreground(Yellow)
	InfoStyle    = lipgloss.NewStyle().Foreground(Sky)
	KeywordStyle = lipgloss.NewStyle().Bold(true).Foreground(Pink)
)

// 符号前缀（色盲友好，部分终端不显示 Unicode 故用 ASCII）
const (
	SymSuccess = "OK" // 替代 ✓
	SymFail    = "X"  // 替代 ✗
	SymWarn    = "!"  // 替代 ⚠
	SymInfo    = "i"  // 替代 ℹ
	SymArrow   = "->" // 替代 →
	SymBullet  = "*"  // 替代 •
)

// 圆角边框样式
var BoxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(Mauve).
	Padding(0, 2)

// Init 在 main 启动时强制启用 TrueColor（除非 NO_COLOR 已设置）。
func Init() {
	if os.Getenv("NO_COLOR") == "" {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
}

// Println 用指定样式输出一行到 stderr（带自动换行）。
func Println(style lipgloss.Style, args ...interface{}) {
	render := renderMultiline(style, fmt.Sprint(args...))
	fmt.Fprintln(os.Stderr, render)
}

// Printf 用指定样式格式化输出到 stderr。
func Printf(style lipgloss.Style, format string, args ...interface{}) {
	render := renderMultiline(style, fmt.Sprintf(format, args...))
	fmt.Fprint(os.Stderr, render)
}

// renderMultiline 处理多行渲染：lipgloss.Style.Render 会给多行加 padding 让它们等宽，
// 直接对含 \n 的字符串 Render 会让后续行被填充空格。这里按行拆分分别渲染再拼接。
func renderMultiline(style lipgloss.Style, text string) string {
	if !strings.Contains(text, "\n") {
		return style.Render(text)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// BoxWithTitle 带标题的圆角框，标题叠在顶边框中间。
//
// ╭────────── 标题 ──────────╮
// │ line1                    │
// ╰──────────────────────────╯
func BoxWithTitle(title, content string) string {
	// 计算内容最大宽度（按 rune，lipgloss.Width 对 CJK 返回正确显示宽）
	maxWidth := lipgloss.Width(title) + 4 // 两侧 "  " 留白
	for _, line := range strings.Split(content, "\n") {
		if w := lipgloss.Width(line); w > maxWidth {
			maxWidth = w
		}
	}
	styledTitle := TitleStyle.Render(" " + title + " ")

	// 圆角字符 ╭ 是 3 字节 UTF-8，必须用 []rune 切。
	top := strings.Repeat("─", maxWidth+2)
	runes := []rune(top)
	leftCorner := string(runes[0])
	rightCorner := string(runes[len(runes)-1])
	// 这里 top 全是 ─，所以 leftCorner==rightCorner==─，我们想要 ╭ ╮
	leftCorner = "╭"
	rightCorner = "╮"

	totalWidth := maxWidth
	leftPad := (totalWidth - lipgloss.Width(title) - 2) / 2
	if leftPad < 1 {
		leftPad = 1
	}
	rightPad := totalWidth - lipgloss.Width(title) - 2 - leftPad
	if rightPad < 1 {
		rightPad = 1
	}
	newTop := leftCorner + strings.Repeat("─", leftPad) + styledTitle + strings.Repeat("─", rightPad) + rightCorner

	// 内容区
	bodyLines := strings.Split(content, "\n")
	var b strings.Builder
	b.WriteString(newTop)
	b.WriteString("\n")
	for _, line := range bodyLines {
		w := lipgloss.Width(line)
		pad := ""
		if totalWidth > w {
			pad = strings.Repeat(" ", totalWidth-w)
		}
		b.WriteString("│ ")
		b.WriteString(line)
		b.WriteString(pad)
		b.WriteString(" │\n")
	}
	b.WriteString("╰")
	b.WriteString(strings.Repeat("─", totalWidth+2))
	b.WriteString("╯")
	return b.String()
}
