package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandTreeContract 锁住命令树的对外契约：命令名/别名/Hidden 与每个 local flag 的
// 短写/默认值/Hidden。老脚本和 skill 文档依赖这些不变（如 dl/ul 别名、隐藏的 login 命令、
// --comments 默认 true、--port 与 --incremental 隐藏保留）。
func TestCommandTreeContract(t *testing.T) {
	type flagSpec struct {
		Short  string
		Def    string
		Hidden bool
	}
	type cmdSpec struct {
		Aliases []string
		Hidden  bool
		Flags   map[string]flagSpec
	}

	loginFlags := map[string]flagSpec{
		"no-wait":     {Def: "false"},
		"device-code": {},
		"json":        {Def: "false"},
		"port":        {Def: "0", Hidden: true},
	}

	want := map[string]cmdSpec{
		"config": {Flags: map[string]flagSpec{"appId": {}, "appSecret": {}}},
		"config init": {Flags: map[string]flagSpec{
			"force":       {Def: "false"},
			"no-wait":     {Def: "false"},
			"device-code": {},
			"json":        {Def: "false"},
		}},
		"config get": {},
		"config set": {},
		"download": {Aliases: []string{"dl"}, Flags: map[string]flagSpec{
			"output":       {Short: "o", Def: "./"},
			"recursive":    {Short: "r", Def: "false"},
			"comments":     {Short: "c", Def: "true"},
			"no-comments":  {Def: "false"},
			"no-diff":      {Def: "false"},
			"force":        {Short: "f", Def: "false"},
			"follow":       {Def: "false"},
			"follow-depth": {Def: "1"},
			"json":         {Def: "false"},
		}},
		"mirror": {Flags: map[string]flagSpec{
			"output":       {Short: "o", Def: "./"},
			"comments":     {Short: "c", Def: "true"},
			"no-comments":  {Def: "false"},
			"force":        {Short: "f", Def: "false"},
			"no-prune":     {Def: "false"},
			"dry-run":      {Def: "false"},
			"follow":       {Def: "false"},
			"follow-depth": {Def: "1"},
		}},
		"auth":        {},
		"auth login":  {Flags: loginFlags},
		"auth status": {Flags: map[string]flagSpec{"json": {Def: "false"}}},
		"auth logout": {},
		"login":       {Hidden: true, Flags: loginFlags},
		"upload": {Aliases: []string{"ul"}, Flags: map[string]flagSpec{
			"source":      {},
			"space":       {Short: "s"},
			"parent":      {Short: "p"},
			"incremental": {Def: "false", Hidden: true},
			"full":        {Def: "false"},
			"dry-run":     {Def: "false"},
			"verbose":     {Short: "v", Def: "false"},
			"json":        {Def: "false"},
		}},
		"publish": {Flags: map[string]flagSpec{
			"name":   {Short: "n"},
			"app-id": {},
			"new":    {Def: "false"},
			"share":  {},
			"json":   {Def: "false"},
		}},
		"diff": {Flags: map[string]flagSpec{"invert": {Short: "i", Def: "false"}}},
		"open": {},
		"search": {Flags: map[string]flagSpec{
			"doc-types":  {},
			"space":      {},
			"folder":     {},
			"only-title": {Def: "false"},
			"sort":       {},
			"page-size":  {Def: "20"},
			"page-token": {},
			"limit":      {Def: "0"},
			"json":       {Def: "false"},
		}},
		"ocr":                   {},
		"skills":                {},
		"sentry":                {Hidden: true},
		"completion":            {},
		"completion bash":       {},
		"completion zsh":        {},
		"completion fish":       {},
		"completion powershell": {Aliases: []string{"pwsh"}},
	}

	got := map[string]cmdSpec{}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			path := strings.TrimSpace(prefix + " " + sub.Name())
			spec := cmdSpec{Aliases: sub.Aliases, Hidden: sub.Hidden, Flags: map[string]flagSpec{}}
			// 构造期只含显式注册的 local flags（help/version 由 Execute 时注入），结果确定
			sub.Flags().VisitAll(func(f *pflag.Flag) {
				spec.Flags[f.Name] = flagSpec{Short: f.Shorthand, Def: f.DefValue, Hidden: f.Hidden}
			})
			if len(spec.Flags) == 0 {
				spec.Flags = nil
			}
			got[path] = spec
			walk(sub, path)
		}
	}
	walk(newRootCommand(), "")

	assert.Equal(t, want, got)
}

// TestRootContract 锁 root 级契约：--debug/--as/--sentry-dsn 是 persistent flag（可置于子命令前）、
// --as 默认 user（user_access_token 策略）、--sentry-dsn 默认空（走 env/编译期 fallback）、版本号注入生效。
func TestRootContract(t *testing.T) {
	root := newRootCommand()
	require.NotNil(t, root.PersistentFlags().Lookup("debug"))
	asFlag := root.PersistentFlags().Lookup("as")
	require.NotNil(t, asFlag)
	assert.Equal(t, "user", asFlag.DefValue)
	sentryFlag := root.PersistentFlags().Lookup("sentry-dsn")
	require.NotNil(t, sentryFlag)
	assert.Equal(t, "", sentryFlag.DefValue)
	configFlag := root.PersistentFlags().Lookup("config")
	require.NotNil(t, configFlag)
	assert.Equal(t, "", configFlag.DefValue)
	assert.NotEmpty(t, root.Version)
}

// TestValidationExitErrors 锁参数校验错误路径：全部在 handler 之前返回（不触网络），
// 以 *exitError 携带裸消息 + 退出码 1（与原 cli.Exit 行为一致）。
// upload --full --incr 同时验证 --incr → --incremental 的 NormalizeFunc 别名：
// 若别名失效会报 unknown flag 而非互斥消息。
func TestValidationExitErrors(t *testing.T) {
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"--as", "bogus", "download", "https://example.feishu.cn/docx/x"}, "--as 必须是 'user' 或 'bot'"},
		{[]string{"download"}, "请指定要下载的文档/文件夹/知识库 URL 或本地 .md 文件"},
		{[]string{"download", "--follow-depth", "2", "https://example.feishu.cn/docx/x"}, "--follow-depth 需要与 --follow 一起使用"},
		{[]string{"download", "--follow", "--follow-depth", "0", "https://example.feishu.cn/docx/x"}, "--follow-depth 必须 >= 1"},
		{[]string{"mirror", "url1", "url2"}, "mirror 只接受一个 URL 参数"},
		{[]string{"upload", "--full", "--incr", "x.md"}, "--full 不能与 --incremental/--incr 同时使用"},
		{[]string{"upload", "--source", "u", "--space", "s", "x.md"}, "--source 不能与 --space/--parent 同时使用"},
		{[]string{"upload", "--full", "--dry-run", "x.md"}, "--dry-run 不能与 --full 同时使用"},
		{[]string{"upload"}, "请指定要上传的 Markdown 文件"},
		{[]string{"upload", "--source", "u", "a.md", "b.md"}, "--source 不能与多个文件参数同时使用"},
		{[]string{"upload", "--json", "--dry-run", "x.md"}, "--json 不能与 --dry-run 同时使用"},
		{[]string{"publish", "--app-id", "app_x", "--new", "dir"}, "--app-id 不能与 --new 同时使用"},
		{[]string{"publish", "--share", "bogus", "dir"}, "--share 必须是"},
		{[]string{"publish"}, "请指定要发布的 HTML 文件或目录"},
		{[]string{"publish", "a", "b"}, "publish 只接受一个路径参数"},
		{[]string{"diff"}, "请指定 Markdown 文件"},
		{[]string{"diff", "a.md", "b.md"}, "diff 只接受一个 Markdown 文件参数"},
		{[]string{"open"}, "请至少指定一个 Markdown 文件"},
		{[]string{"ocr", "a.png", "b.png"}, "ocr 只接受一个图片文件参数"},
		{[]string{"config", "bogus"}, "config 不接受位置参数"},
		{[]string{"search"}, "请指定搜索关键词"},
		{[]string{"search", "a", "b"}, "search 只接受一个关键词参数"},
		{[]string{"search", strings.Repeat("字", 51)}, "搜索关键词最多 50 个字符"},
		{[]string{"search", "--folder", "f", "--space", "s", "q"}, "--folder 不能与 --space 同时使用"},
		{[]string{"search", "--page-size", "21", "q"}, "--page-size 必须在 1-20 之间"},
		{[]string{"search", "--limit", "1001", "q"}, "--limit 必须在 1-1000 之间"},
		{[]string{"search", "--doc-types", "bogus", "q"}, "--doc-types 含未知取值"},
		{[]string{"search", "--sort", "bogus", "q"}, "--sort 必须是以下之一"},
	}
	for _, tc := range cases {
		root := newRootCommand()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(tc.args)

		err := root.ExecuteContext(context.Background())

		var ee *exitError
		require.ErrorAs(t, err, &ee, "args=%v", tc.args)
		assert.Equal(t, 1, ee.code, "args=%v", tc.args)
		assert.Contains(t, ee.msg, tc.msg, "args=%v", tc.args)
	}
}
