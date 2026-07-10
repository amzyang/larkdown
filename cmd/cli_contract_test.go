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
		"download": {Aliases: []string{"dl"}, Flags: map[string]flagSpec{
			"output":       {Short: "o", Def: "./"},
			"recursive":    {Short: "r", Def: "false"},
			"comments":     {Short: "c", Def: "true"},
			"no-diff":      {Def: "false"},
			"force":        {Short: "f", Def: "false"},
			"follow":       {Def: "false"},
			"follow-depth": {Def: "1"},
		}},
		"mirror": {Flags: map[string]flagSpec{
			"output":       {Short: "o", Def: "./"},
			"comments":     {Short: "c", Def: "true"},
			"force":        {Short: "f", Def: "false"},
			"no-prune":     {Def: "false"},
			"follow":       {Def: "false"},
			"follow-depth": {Def: "1"},
		}},
		"auth":        {},
		"auth login":  {Flags: loginFlags},
		"auth status": {},
		"auth logout": {},
		"login":       {Hidden: true, Flags: loginFlags},
		"upload": {Aliases: []string{"ul"}, Flags: map[string]flagSpec{
			"source":      {},
			"space":       {Short: "s"},
			"parent":      {Short: "p"},
			"incremental": {Def: "false", Hidden: true},
			"full":        {Def: "false"},
			"dryrun":      {Def: "false"},
			"verbose":     {Short: "v", Def: "false"},
		}},
		"publish": {Flags: map[string]flagSpec{
			"name":   {Short: "n"},
			"app-id": {},
			"new":    {Def: "false"},
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
			"json":       {Def: "false"},
		}},
		"ocr":                   {},
		"skills":                {},
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

// TestRootContract 锁 root 级契约：--debug/--as 是 persistent flag（可置于子命令前）、
// --as 默认 user（user_access_token 策略）、版本号注入生效。
func TestRootContract(t *testing.T) {
	root := newRootCommand()
	require.NotNil(t, root.PersistentFlags().Lookup("debug"))
	asFlag := root.PersistentFlags().Lookup("as")
	require.NotNil(t, asFlag)
	assert.Equal(t, "user", asFlag.DefValue)
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
		{[]string{"--as", "bogus", "download", "https://example.feishu.cn/docx/x"}, "--as must be 'user' or 'bot'"},
		{[]string{"download"}, "Please specify the document/folder/wiki url"},
		{[]string{"download", "--follow-depth", "2", "https://example.feishu.cn/docx/x"}, "--follow-depth requires --follow"},
		{[]string{"download", "--follow", "--follow-depth", "0", "https://example.feishu.cn/docx/x"}, "--follow-depth must be >= 1"},
		{[]string{"upload", "--full", "--incr", "x.md"}, "--full cannot be used with --incremental/--incr"},
		{[]string{"upload", "--source", "u", "--space", "s", "x.md"}, "--source cannot be used with --space or --parent"},
		{[]string{"upload", "--full", "--dryrun", "x.md"}, "--dryrun cannot be used with --full"},
		{[]string{"upload"}, "Please specify the markdown file to upload"},
		{[]string{"publish", "--app-id", "app_x", "--new", "dir"}, "--app-id cannot be used with --new"},
		{[]string{"publish"}, "Please specify the HTML file or directory to publish"},
		{[]string{"diff"}, "Please specify the markdown file"},
		{[]string{"open"}, "Please specify at least one markdown file"},
		{[]string{"search"}, "Please specify the search query"},
		{[]string{"search", strings.Repeat("字", 51)}, "query must be at most 50 characters"},
		{[]string{"search", "--folder", "f", "--space", "s", "q"}, "--folder cannot be used with --space"},
		{[]string{"search", "--page-size", "21", "q"}, "--page-size must be between 1 and 20"},
		{[]string{"search", "--doc-types", "bogus", "q"}, "--doc-types contains unknown value"},
		{[]string{"search", "--sort", "bogus", "q"}, "--sort must be one of"},
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
