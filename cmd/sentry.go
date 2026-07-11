package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

// sentryDSN 由 GitHub Release 构建经 ldflags -X main.sentryDSN 注入；
// 本地 just build 不注入 → 遥测天然禁用。
var sentryDSN string

// resolveSentryDSN 决定最终生效的 DSN（纯函数，env 等不确定来源由调用方注入）。
// 返回空串 = 禁用遥测。优先级：
// --sentry-dsn flag（显式设置即生效，设空即禁用）> DO_NOT_TRACK（consoledonottrack.com 惯例）
// > env SENTRY_DSN（显式设空即禁用）> 编译期 fallback。
func resolveSentryDSN(flagValue string, flagChanged bool, doNotTrack string, envValue string, envSet bool, buildDSN string) string {
	if flagChanged {
		return strings.TrimSpace(flagValue)
	}
	if dnt := strings.TrimSpace(doNotTrack); dnt != "" && dnt != "0" && dnt != "false" {
		return ""
	}
	if envSet {
		return strings.TrimSpace(envValue)
	}
	return strings.TrimSpace(buildDSN)
}

// scrubSentryEvent 是 BeforeSend 兜底：不依赖 SDK 版本行为，事件永不携带主机名与用户身份。
func scrubSentryEvent(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
	event.ServerName = ""
	event.User = sentry.User{}
	return event
}

// initSentry 初始化遥测（best-effort）：dsn 为空直接禁用；Init 失败仅告警，不阻断命令执行。
func initSentry(dsn, release string, debug bool) {
	if dsn == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		AttachStacktrace: true,
		Debug:            debug,
		DebugWriter:      os.Stderr,
		SendDefaultPII:   false,
		BeforeSend:       scrubSentryEvent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: sentry init failed: %v\n", err)
	}
}

// sentryRecoverRepanic 供 main 与 worker goroutine defer：崩溃前上报 panic，再原样重抛，
// 保留崩溃输出与非零退出码。遥测未初始化时 Recover/Flush 均为 no-op。
func sentryRecoverRepanic() {
	if r := recover(); r != nil {
		sentry.CurrentHub().Recover(r)
		sentry.Flush(2 * time.Second)
		panic(r)
	}
}
