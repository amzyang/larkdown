package main

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveSentryDSN 锁 DSN 解析优先级契约：
// --sentry-dsn flag（显式设置即生效，含设空禁用）> DO_NOT_TRACK > env SENTRY_DSN > 编译期 fallback。
// 空串输出 = 禁用遥测。
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
	}{
		{"flag 覆盖编译期", "dsn-A", true, "", "", false, "dsn-B", "dsn-A"},
		{"flag 覆盖 env", "dsn-A", true, "", "dsn-E", true, "dsn-B", "dsn-A"},
		{"显式 --sentry-dsn= 空值是最高级 opt-out", "", true, "", "dsn-E", true, "dsn-B", ""},
		{"flag 空白 trim 后视同禁用", "  ", true, "", "", false, "dsn-B", ""},
		{"flag 显式意图压过 DO_NOT_TRACK", "dsn-A", true, "1", "", false, "dsn-B", "dsn-A"},
		{"DO_NOT_TRACK 压过 env 与编译期", "", false, "1", "dsn-E", true, "dsn-B", ""},
		{"DO_NOT_TRACK=0 不生效", "", false, "0", "dsn-E", true, "dsn-B", "dsn-E"},
		{"DO_NOT_TRACK=false 不生效", "", false, "false", "dsn-E", true, "dsn-B", "dsn-E"},
		{"env 覆盖编译期", "", false, "", "dsn-E", true, "dsn-B", "dsn-E"},
		{"SENTRY_DSN= 空值是 env 层 opt-out", "", false, "", "", true, "dsn-B", ""},
		{"release 二进制默认走编译期 fallback", "", false, "", "", false, "dsn-B", "dsn-B"},
		{"本地构建无编译期 DSN 默认禁用", "", false, "", "", false, "", ""},
		{"编译期误注入空白也禁用", "", false, "", "", false, "  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSentryDSN(tc.flagValue, tc.flagChanged, tc.doNotTrack, tc.envValue, tc.envSet, tc.buildDSN)
			assert.Equal(t, tc.want, got)
		})
	}
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
