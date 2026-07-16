package main

import (
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveSentryDSN 锁 DSN 解析优先级契约：
// --sentry-dsn flag（显式设置即生效，含设空禁用）> DO_NOT_TRACK > env SENTRY_DSN > 编译期 fallback。
// 空串输出 = 禁用遥测；source 标记本次决策由哪一层拍板（sentry 诊断命令展示用）。
func TestResolveSentryDSN(t *testing.T) {
	cases := []struct {
		name        string
		flagValue   string
		flagChanged bool
		doNotTrack  string
		envValue    string
		envSet      bool
		buildDSN    string
		want        string
		wantSource  string
	}{
		{"flag 覆盖编译期", "dsn-A", true, "", "", false, "dsn-B", "dsn-A", dsnSourceFlag},
		{"flag 覆盖 env", "dsn-A", true, "", "dsn-E", true, "dsn-B", "dsn-A", dsnSourceFlag},
		{"显式 --sentry-dsn= 空值是最高级 opt-out", "", true, "", "dsn-E", true, "dsn-B", "", dsnSourceFlag},
		{"flag 空白 trim 后视同禁用", "  ", true, "", "", false, "dsn-B", "", dsnSourceFlag},
		{"flag 显式意图压过 DO_NOT_TRACK", "dsn-A", true, "1", "", false, "dsn-B", "dsn-A", dsnSourceFlag},
		{"DO_NOT_TRACK 压过 env 与编译期", "", false, "1", "dsn-E", true, "dsn-B", "", dsnSourceDoNotTrack},
		{"DO_NOT_TRACK=0 不生效", "", false, "0", "dsn-E", true, "dsn-B", "dsn-E", dsnSourceEnv},
		{"DO_NOT_TRACK=false 不生效", "", false, "false", "dsn-E", true, "dsn-B", "dsn-E", dsnSourceEnv},
		{"env 覆盖编译期", "", false, "", "dsn-E", true, "dsn-B", "dsn-E", dsnSourceEnv},
		{"SENTRY_DSN= 空值是 env 层 opt-out", "", false, "", "", true, "dsn-B", "", dsnSourceEnv},
		{"release 二进制默认走编译期 fallback", "", false, "", "", false, "dsn-B", "dsn-B", dsnSourceBuild},
		{"本地构建无编译期 DSN 默认禁用", "", false, "", "", false, "", "", dsnSourceNone},
		{"编译期误注入空白也禁用", "", false, "", "", false, "  ", "", dsnSourceNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, source := resolveSentryDSN(tc.flagValue, tc.flagChanged, tc.doNotTrack, tc.envValue, tc.envSet, tc.buildDSN)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantSource, source)
		})
	}
}

// TestRenderSentryStatus 锁诊断输出的关键信息：禁用/启用/初始化失败三态，
// 启用时拆解 DSN 展示 host / project / public key。
func TestRenderSentryStatus(t *testing.T) {
	t.Run("禁用时不展示 DSN 细节", func(t *testing.T) {
		joined := strings.Join(renderSentryStatus("", dsnSourceNone, false, "v1", false), "\n")
		assert.Contains(t, joined, "禁用")
		assert.NotContains(t, joined, "Host")
	})
	t.Run("启用时拆解 DSN", func(t *testing.T) {
		joined := strings.Join(renderSentryStatus("https://pubkey@sentry.example.com/42", dsnSourceEnv, true, "v1", true), "\n")
		assert.Contains(t, joined, "启用")
		assert.Contains(t, joined, "sentry.example.com")
		assert.Contains(t, joined, "42")
		assert.Contains(t, joined, "pubkey")
		assert.Contains(t, joined, dsnSourceLabels[dsnSourceEnv])
	})
	t.Run("DSN 已配置但 SDK 未初始化标记异常", func(t *testing.T) {
		joined := strings.Join(renderSentryStatus("https://pubkey@sentry.example.com/42", dsnSourceBuild, false, "v1", false), "\n")
		assert.Contains(t, joined, "初始化失败")
	})
	t.Run("DSN 非法时展示解析错误而非崩溃", func(t *testing.T) {
		joined := strings.Join(renderSentryStatus("not-a-dsn", dsnSourceFlag, false, "v1", false), "\n")
		assert.Contains(t, joined, "解析失败")
	})
}

// TestScrubSentryEvent 锁隐私契约：无论 SDK 版本行为如何，事件不携带主机名与用户身份。
func TestScrubSentryEvent(t *testing.T) {
	event := sentry.NewEvent()
	event.ServerName = "my-laptop.local"
	event.User = sentry.User{ID: "u1", Username: "zouyang", IPAddress: "1.2.3.4"}

	got := scrubSentryEvent(event, nil)

	require.NotNil(t, got)
	assert.Empty(t, got.ServerName)
	assert.Equal(t, sentry.User{}, got.User)
}
