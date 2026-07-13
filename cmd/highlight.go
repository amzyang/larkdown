package main

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/amzyang/larkdown/core"
)

// 警示框配色（ANSI 256 色号）。颜色降级由输出层的 lipgloss.Writer（colorprofile）
// 按终端能力处理：管道/重定向/NO_COLOR 下自动去色为纯文本，CI 可读。
const (
	colorYellow = "11"  // 机构可见（提醒暴露面）
	colorRed    = "9"   // 互联网可访问
	colorOrange = "208" // 提交失败告警
)

// renderShareNotice 生成发布后的访问权限提示。
// larkdown 经官方 spark/v1 access-scope 提交档位（AppliedScope），已验证对妙搭 HTML 应用
// 会同步到网页「App availability」（PUT Tenant → All org members）。故对 Tenant/All 档
// 确认已生效并提醒暴露面 + 给出收窄方式。
//
// 返回值可能带 ANSI 样式；打印时须经 lipgloss.Writer 输出以按终端能力降级。
func renderShareNotice(appliedScope string, scopeErr error, manageURL string, isNew bool) string {
	switch {
	case scopeErr != nil:
		return warnBox(colorOrange, "⚠ 访问权限提交失败", []string{
			"发布已成功，但提交访问范围时出错：",
			"  " + scopeErr.Error(),
			"请打开编辑管理链接手动设置访问权限：",
			"  " + manageURL,
		})
	case appliedScope == core.MiaodaScopeTenant:
		return warnBox(colorYellow, "✓ 访问权限已设为：本机构全员可见（All org members）", []string{
			"机构内任何登录用户凭此 URL 即可访问本页全部内容。",
			"如需收窄：重新发布时加 --share selected，或打开编辑管理链接：",
			"  " + manageURL,
		})
	case appliedScope == core.MiaodaScopeAll:
		return warnBox(colorRed, "✓ 访问权限已设为：互联网可访问（需登录）", []string{
			"任何登录用户凭此 URL 即可访问本页全部内容。",
			"如需收窄：重新发布时加 --share selected / --share tenant，或打开编辑管理链接：",
			"  " + manageURL,
		})
	default:
		// 未提交权限（--share selected 或更新已有应用保持不变）：常规单行，不加框。
		if isNew {
			return "访问范围：仅自己/指定人可见（他人打开会提示无权限）"
		}
		return "访问范围：本次未改动（如需调整请打开编辑管理链接）"
	}
}

// warnBox 用带色边框的框渲染标题 + 正文行。
func warnBox(color, title string, lines []string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(color)).
		Padding(0, 1)
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
	content := titleStyle.Render(title) + "\n" + strings.Join(lines, "\n")
	return box.Render(content)
}
