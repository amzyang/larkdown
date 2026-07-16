package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"
)

// sentryDSN 由 GitHub Release 构建经 ldflags -X main.sentryDSN 注入；
// 本地 just build 不注入 → 遥测天然禁用。
var sentryDSN string

// DSN 决策来源（resolveSentryDSN 第二返回值），供 sentry 诊断命令展示决策依据。
const (
	dsnSourceFlag       = "flag"
	dsnSourceDoNotTrack = "do-not-track"
	dsnSourceEnv        = "env"
	dsnSourceBuild      = "build"
	dsnSourceNone       = "none"
)

var dsnSourceLabels = map[string]string{
	dsnSourceFlag:       "--sentry-dsn flag",
	dsnSourceDoNotTrack: "DO_NOT_TRACK 环境变量（opt-out）",
	dsnSourceEnv:        "SENTRY_DSN 环境变量",
	dsnSourceBuild:      "编译期注入（ldflags）",
	dsnSourceNone:       "无（编译期未注入，运行时也未配置）",
}

// resolveSentryDSN 决定最终生效的 DSN（纯函数，env 等不确定来源由调用方注入）。
// dsn 为空串 = 禁用遥测；source 标记由哪一层拍板。优先级：
// --sentry-dsn flag（显式设置即生效，设空即禁用）> DO_NOT_TRACK（consoledonottrack.com 惯例）
// > env SENTRY_DSN（显式设空即禁用）> 编译期 fallback。
func resolveSentryDSN(flagValue string, flagChanged bool, doNotTrack string, envValue string, envSet bool, buildDSN string) (dsn, source string) {
	if flagChanged {
		return strings.TrimSpace(flagValue), dsnSourceFlag
	}
	if dnt := strings.TrimSpace(doNotTrack); dnt != "" && dnt != "0" && dnt != "false" {
		return "", dsnSourceDoNotTrack
	}
	if envSet {
		return strings.TrimSpace(envValue), dsnSourceEnv
	}
	if dsn := strings.TrimSpace(buildDSN); dsn != "" {
		return dsn, dsnSourceBuild
	}
	return "", dsnSourceNone
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

// renderSentryStatus 渲染遥测诊断输出（纯函数）。inited 为 SDK 客户端是否已初始化。
func renderSentryStatus(dsn, source string, inited bool, release string, debug bool) []string {
	state := "禁用"
	if dsn != "" {
		if inited {
			state = "启用（SDK 已初始化）"
		} else {
			state = "异常（DSN 已配置但 SDK 初始化失败，见启动 warning）"
		}
	}
	lines := []string{
		"Sentry 遥测状态",
		"  状态:       " + state,
		"  DSN 来源:   " + dsnSourceLabels[source],
		"  Release:    " + release,
		fmt.Sprintf("  Debug:      %t", debug),
	}
	if dsn == "" {
		return lines
	}
	lines = append(lines, "  DSN:        "+dsn)
	parsed, err := sentry.NewDsn(dsn)
	if err != nil {
		return append(lines, "  DSN 解析失败: "+err.Error())
	}
	return append(lines,
		fmt.Sprintf("  Host:       %s://%s:%d", parsed.GetScheme(), parsed.GetHost(), parsed.GetPort()),
		"  Project ID: "+parsed.GetProjectID(),
		"  Public Key: "+parsed.GetPublicKey(),
		"  API URL:    "+parsed.GetAPIURL().String(),
	)
}

func eventIDString(id *sentry.EventID) string {
	if id == nil {
		return "(无 — 事件被 SDK 丢弃)"
	}
	return string(*id)
}

// newSentryCommand 隐藏诊断命令：展示遥测状态；已启用时主动发送 test message +
// test exception 做 e2e 验证（事件带 larkdown.e2e=1 tag，Sentry 侧可过滤）。
func newSentryCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "sentry",
		Short:  "Show Sentry telemetry status and send test events (e2e diagnostics)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitWithMessage("sentry 不接受位置参数", 1)
			}
			return handleSentryCommand(cmd.Flags().Changed("sentry-dsn"))
		},
	}
}

func handleSentryCommand(dsnFlagChanged bool) error {
	envValue, envSet := os.LookupEnv("SENTRY_DSN")
	dsn, source := resolveSentryDSN(globalOpts.sentryDSN, dsnFlagChanged,
		os.Getenv("DO_NOT_TRACK"), envValue, envSet, sentryDSN)
	// inited 是发送 test 事件的唯一门槛：initSentry 对空 DSN 不 Init，且本函数与
	// PersistentPreRunE 用同一组进程内不变输入 resolve，故 inited=true ⟹ 此处 dsn 非空。
	// 若未来两条 resolve 路径分叉，opt-out 用户可能在展示「禁用」的同时被发送事件。
	inited := sentry.CurrentHub().Client() != nil
	for _, line := range renderSentryStatus(dsn, source, inited, strings.TrimSpace(version), globalOpts.debug) {
		fmt.Println(line)
	}
	if !inited {
		return nil
	}

	var msgID, excID *sentry.EventID
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("larkdown.e2e", "1")
		msgID = sentry.CaptureMessage("larkdown sentry e2e test message")
		excID = sentry.CaptureException(errors.New("larkdown sentry e2e test exception"))
	})
	fmt.Println()
	fmt.Println("已发送 test message，  event_id: " + eventIDString(msgID))
	fmt.Println("已发送 test exception，event_id: " + eventIDString(excID))
	if !sentry.Flush(5 * time.Second) {
		return exitWithMessage("sentry flush 超时（5s），事件可能未送出：请检查网络，或加 --debug 查看 SDK 传输日志", 1)
	}
	fmt.Println("flush 完成（传输队列已清空）。flush 成功不代表服务端已接收（4xx/5xx 拒收不体现在返回值），请到 Sentry UI 用上述 event_id 核验。")
	return nil
}
