package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
)

// degrade 把带 ANSI 的渲染结果经 NoTTY profile 的 colorprofile.Writer 输出，
// 模拟管道/非 TTY 下 lipgloss.Writer 的去色降级，供断言纯文本。
func degrade(s string) string {
	var buf bytes.Buffer
	w := colorprofile.NewWriter(&buf, []string{})
	w.Profile = colorprofile.NoTTY
	_, _ = io.WriteString(w, s)
	return buf.String()
}

func TestRenderShareNotice_DegradedNoANSI(t *testing.T) {
	manageURL := "https://miaoda.feishu.cn/app/app_x"
	cases := []struct {
		name        string
		scope       string
		err         error
		isNew       bool
		wantContain string
	}{
		{"tenant", core.MiaodaScopeTenant, nil, true, "本机构全员可见"},
		{"all", core.MiaodaScopeAll, nil, true, "互联网可访问"},
		{"scope error", "", errors.New("boom"), true, "提交失败"},
		{"unset on new", "", nil, true, "仅自己/指定人可见"},
		{"unset on update", "", nil, false, "本次未改动"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := degrade(renderShareNotice(c.scope, c.err, manageURL, c.isNew))
			assert.NotContains(t, out, "\x1b", "degraded output must not contain ANSI escapes")
			assert.Contains(t, out, c.wantContain)
		})
	}
}

func TestRenderShareNotice_TenantShowsScopeAndGuidance(t *testing.T) {
	out := renderShareNotice(core.MiaodaScopeTenant, nil, "https://x", true)
	assert.Contains(t, out, "All org members")
	assert.Contains(t, out, "--share selected")
}

func TestRenderShareNotice_UnsetIsPlainSingleLine(t *testing.T) {
	out := renderShareNotice("", nil, "https://x", false)
	assert.NotContains(t, out, "╭", "unset notice must not be boxed")
	assert.False(t, strings.Contains(out, "\n"), "unset notice should be a single line")
}
