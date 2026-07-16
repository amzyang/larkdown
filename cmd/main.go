package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/amzyang/larkdown/core"
	"github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var version = "v2-test"

// 认证身份（全局 --as flag 取值，对齐 lark-cli 的 --as user|bot 语义）
const (
	identityUser = "user"
	identityBot  = "bot"
)

// globalOpts 全局选项
var globalOpts = struct {
	debug      bool
	as         string
	sentryDSN  string
	configPath string
	clientOpts *core.ClientOptions
}{}

// exitError 对应原 cli.Exit(msg, code)：msg 裸打到 stderr（可为空），以 code 退出。
// diff 命令依赖「空消息 + exit 1」表达 git diff --exit-code 式契约。
type exitError struct {
	msg  string
	code int
}

func (e *exitError) Error() string { return e.msg }

func exitWithMessage(msg string, code int) error { return &exitError{msg: msg, code: code} }

// codedError 包装 handler 运行时错误并携带退出码。与 exitError（用户输入类，裸消息、不上报）
// 不同，它属于运行时故障：main() 照常打印 "Error:" 前缀并按原判定上报 Sentry，仅退出码不同。
// diff 用它区分「有差异」(exit 1) 与「运行出错」(exit 2)，对齐 git diff 的退出码语义。
type codedError struct {
	err  error
	code int
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// createClientFromConfig 按全局身份策略创建客户端。
// 默认 user 策略：user_access_token 是唯一隐式通道（过期自动刷新），未登录/刷新失败直接报错，
// 不再静默降级；应用凭证 (tenant_access_token) 仅在显式 --as bot 时使用。
// 认证相关提示一律走 stderr，避免污染可管道化的 stdout 输出。
func createClientFromConfig(ctx context.Context, config *core.Config, configPath string) (*core.Client, error) {
	if globalOpts.as == identityBot {
		fmt.Fprintln(os.Stderr, "使用认证方式: tenant_access_token (应用凭证, --as bot)")
		return core.NewClient(config.Feishu.AppId, config.Feishu.AppSecret, globalOpts.clientOpts), nil
	}
	if config.Feishu.HasValidUserToken() {
		fmt.Fprintln(os.Stderr, "使用认证方式: user_access_token")
		return core.NewClientWithUserToken(
			config.Feishu.AppId,
			config.Feishu.AppSecret,
			config.Feishu.UserAccessToken,
			globalOpts.clientOpts,
		), nil
	}
	if config.Feishu.NeedsRefresh() {
		fmt.Fprintln(os.Stderr, "Token 已过期，正在刷新...")
		oauthMgr := core.NewOAuthManager(
			config.Feishu.AppId,
			config.Feishu.AppSecret,
			globalOpts.clientOpts,
		)
		outcome, err := core.EnsureFreshUserToken(ctx, config, configPath, oauthMgr)
		switch outcome {
		case core.RefreshOutcomeRefreshed, core.RefreshOutcomeRefreshedByOther:
			if err != nil {
				fmt.Fprintf(os.Stderr, "警告: %v\n", err)
			}
			fmt.Fprintln(os.Stderr, "Token 刷新成功")
			fmt.Fprintln(os.Stderr, "使用认证方式: user_access_token")
			return core.NewClientWithUserToken(
				config.Feishu.AppId,
				config.Feishu.AppSecret,
				config.Feishu.UserAccessToken,
				globalOpts.clientOpts,
			), nil
		case core.RefreshOutcomeTokenInvalidCleared:
			return nil, exitWithMessage(fmt.Sprintf("user_access_token 已失效（%v）。请重新执行 larkdown auth login，或加 --as bot 使用应用凭证 (tenant_access_token)", err), 1)
		default: // RefreshOutcomeTransientFailure
			return nil, exitWithMessage(fmt.Sprintf("刷新 user_access_token 失败（网络波动？%v）。token 已保留、稍后重试即可；或加 --as bot 使用应用凭证 (tenant_access_token)", err), 1)
		}
	}
	if config.Feishu.UserAccessToken != "" || config.Feishu.RefreshToken != "" {
		return nil, exitWithMessage("登录已过期（refresh_token 失效），请重新执行 larkdown auth login，或加 --as bot 使用应用凭证 (tenant_access_token)", 1)
	}
	return nil, exitWithMessage("当前未登录：larkdown 默认使用用户身份（user_access_token）。请先执行 larkdown auth login；批量自动化场景可加 --as bot 使用应用凭证 (tenant_access_token)", 1)
}

// mdFileCompletion 生成「首个位置参数补全 .md 文件」的 ValidArgsFunction；
// variadic 为 true 时所有位置参数都补全 .md（open 命令）。
func mdFileCompletion(variadic bool) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if !variadic && len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return []cobra.Completion{"md"}, cobra.ShellCompDirectiveFilterFileExt
	}
}

// noFileCompletion 位置参数是 URL 等非文件输入时禁用文件名补全。
func noFileCompletion(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read config file or set field(s) if provided",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitWithMessage(fmt.Sprintf("config 不接受位置参数: %s（子命令见 larkdown config --help）", args[0]), 1)
			}
			return handleConfigCommand()
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&configOpts.appId, "appId", "", "Set app id for the OPEN API")
	fl.StringVar(&configOpts.appSecret, "appSecret", "", "Set app secret for the OPEN API")
	cmd.AddCommand(newConfigInitCommand(), newConfigGetCommand(), newConfigSetCommand())
	return cmd
}

func newDownloadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "download <url|file.md> [more...]",
		Aliases: []string{"dl"},
		Short:   "Download feishu/larksuite documents to markdown files",
		Long: "Download Feishu/Lark documents to local Markdown files.\n\n" +
			"Accepts one or more document/folder/wiki URLs. A local .md file path is\n" +
			"also accepted: its frontmatter source URL is used to re-download the\n" +
			"document, with the output directory defaulting to the file's own directory.",
		ValidArgsFunction: mdFileCompletion(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("请指定要下载的文档/文件夹/知识库 URL 或本地 .md 文件", 1)
			}
			if cmd.Flags().Changed("follow-depth") && !dlOpts.follow {
				return exitWithMessage("--follow-depth 需要与 --follow 一起使用", 1)
			}
			if dlOpts.followDepth < 1 {
				return exitWithMessage("--follow-depth 必须 >= 1", 1)
			}
			if dlOpts.noComments {
				dlOpts.comments = false
			}
			return handleDownloadCommand(args)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&dlOpts.outputDir, "output", "o", "./", "Specify the output directory for the markdown files")
	fl.BoolVarP(&dlOpts.recursive, "recursive", "r", false, "Recursively download all child nodes of a wiki node")
	fl.BoolVarP(&dlOpts.comments, "comments", "c", true, "Include document comments in the exported Markdown")
	fl.BoolVar(&dlOpts.noComments, "no-comments", false, "Exclude document comments (overrides --comments)")
	fl.BoolVar(&dlOpts.noDiff, "no-diff", false, "Disable diff output when downloading")
	fl.BoolVarP(&dlOpts.force, "force", "f", false, "Force re-download even if the remote document is unchanged")
	fl.BoolVar(&dlOpts.follow, "follow", false, "Also download referenced docx/wiki documents (mentions and inline links) into _refs/")
	fl.IntVar(&dlOpts.followDepth, "follow-depth", 1, "How many levels of references to follow (requires --follow)")
	fl.BoolVar(&dlOpts.asJSON, "json", false, "Emit a machine-readable JSON summary (documents, files, failed); progress goes to stderr and implies --no-diff")
	_ = cmd.MarkFlagDirname("output")
	return cmd
}

func newMirrorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror [url]",
		Short: "One-way sync (download-only) a wiki/folder into a local mirror directory with CLAUDE.md and index files",
		Long: "Mirror a Feishu wiki space, wiki subtree or drive folder into a local directory:\n" +
			"the output directory itself is the mirror root. Always generates llms.txt,\n" +
			"docs_map.md and a CLAUDE.md explaining the layout, and prunes local documents\n" +
			"that no longer exist remotely (moved to trash). Re-run without <url> inside a\n" +
			"mirror directory to re-sync from the recorded source.",
		ValidArgsFunction: noFileCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitWithMessage("mirror 只接受一个 URL 参数", 1)
			}
			mirrorOpts.followSet = cmd.Flags().Changed("follow")
			mirrorOpts.depthSet = cmd.Flags().Changed("follow-depth")
			if mirrorOpts.noComments {
				mirrorOpts.comments = false
			}
			var url string
			if len(args) > 0 {
				url = args[0]
			}
			return handleMirrorCommand(url)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&mirrorOpts.outputDir, "output", "o", "./", "Mirror root directory")
	fl.BoolVarP(&mirrorOpts.comments, "comments", "c", true, "Include document comments in the exported Markdown")
	fl.BoolVar(&mirrorOpts.noComments, "no-comments", false, "Exclude document comments (overrides --comments)")
	fl.BoolVarP(&mirrorOpts.force, "force", "f", false, "Force re-download even if the remote document is unchanged")
	fl.BoolVar(&mirrorOpts.noPrune, "no-prune", false, "Keep local files whose remote documents were deleted")
	fl.BoolVar(&mirrorOpts.dryRun, "dry-run", false, "Preview what would be synced and pruned without writing anything")
	fl.BoolVar(&mirrorOpts.follow, "follow", false, "Also download referenced docx/wiki documents (mentions and inline links) into _refs/; recorded in the mirror manifest for re-sync")
	fl.IntVar(&mirrorOpts.followDepth, "follow-depth", 1, "How many levels of references to follow (requires --follow)")
	_ = cmd.MarkFlagDirname("output")
	return cmd
}

func newUploadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "upload <file.md>",
		Aliases:           []string{"ul"},
		Short:             "Upload local markdown file to Feishu Wiki",
		ValidArgsFunction: mdFileCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("请指定要上传的 Markdown 文件", 1)
			}
			if len(args) > 1 {
				return exitWithMessage("upload 只接受一个 Markdown 文件参数", 1)
			}
			full, _ := cmd.Flags().GetBool("full")
			incremental, _ := cmd.Flags().GetBool("incremental")
			if full && incremental {
				return exitWithMessage("--full 不能与 --incremental/--incr 同时使用", 1)
			}
			uploadOpts.incremental = !full
			if uploadOpts.source != "" && (uploadOpts.spaceID != "" || uploadOpts.parentNodeToken != "") {
				return exitWithMessage("--source 不能与 --space/--parent 同时使用", 1)
			}
			if uploadOpts.dryRun && !uploadOpts.incremental {
				return exitWithMessage("--dry-run 不能与 --full 同时使用", 1)
			}
			if uploadOpts.json && uploadOpts.dryRun {
				return exitWithMessage("--json 不能与 --dry-run 同时使用", 1)
			}
			return handleUploadCommand(args[0])
		},
	}
	// --incr 是 --incremental 的历史多字符别名，归一到同一 flag（老脚本零迁移）
	cmd.Flags().SetNormalizeFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "incr" {
			name = "incremental"
		}
		return pflag.NormalizedName(name)
	})
	fl := cmd.Flags()
	fl.StringVar(&uploadOpts.source, "source", "", "Target Feishu document URL (mutually exclusive with --space/--parent)")
	fl.StringVarP(&uploadOpts.spaceID, "space", "s", "", "Wiki space ID (optional, defaults to My Document Library)")
	fl.StringVarP(&uploadOpts.parentNodeToken, "parent", "p", "", "Parent node token (optional, for specifying location)")
	fl.Bool("incremental", false, "Incremental update (default behavior; kept for backward compatibility)")
	fl.Bool("full", false, "Full update (delete all remote blocks and re-upload) instead of the default incremental update")
	fl.BoolVar(&uploadOpts.dryRun, "dry-run", false, "Show what incremental update would do without making changes (incompatible with --full)")
	fl.BoolVarP(&uploadOpts.verbose, "verbose", "v", false, "Show all blocks including unchanged ones (used with --dry-run)")
	fl.BoolVar(&uploadOpts.json, "json", false, "Emit machine-readable JSON (file, is_new, url); progress goes to stderr (incompatible with --dry-run)")
	_ = fl.MarkHidden("incremental")
	return cmd
}

func newPublishCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <dir-or-html>",
		Short: "Publish a local HTML file or directory as an online Feishu Miaoda (妙搭) app",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return []cobra.Completion{"html"}, cobra.ShellCompDirectiveFilterFileExt
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("请指定要发布的 HTML 文件或目录", 1)
			}
			if len(args) > 1 {
				return exitWithMessage("publish 只接受一个路径参数", 1)
			}
			if publishOpts.appID != "" && publishOpts.forceNew {
				return exitWithMessage("--app-id 不能与 --new 同时使用", 1)
			}
			if publishOpts.share != "" && !slices.Contains(core.ValidShareValues, publishOpts.share) {
				return exitWithMessage(fmt.Sprintf("--share 必须是 %s 之一（收到 %q）", strings.Join(core.ValidShareValues, " / "), publishOpts.share), 1)
			}
			return handlePublishCommand(args[0])
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&publishOpts.name, "name", "n", "", "App display name (defaults to the file/dir name)")
	fl.StringVar(&publishOpts.appID, "app-id", "", "Reuse an existing app to update it (app_xxx or https://miaoda.feishu.cn/app/app_xxx)")
	fl.BoolVar(&publishOpts.forceNew, "new", false, "Force creating a new app even if a publish record exists")
	fl.StringVar(&publishOpts.share, "share", "", "App visibility: selected | tenant | public (default: open to the whole org on a new app, unchanged on update)")
	fl.BoolVar(&publishOpts.json, "json", false, "Emit machine-readable JSON (app_id, url, manage_url, scope); progress goes to stderr")
	_ = cmd.RegisterFlagCompletionFunc("share", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return core.ValidShareValues, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newDiffCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "diff <file.md>",
		Short:             "Show diff between local markdown and remote Feishu document",
		ValidArgsFunction: mdFileCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("请指定 Markdown 文件", 1)
			}
			if len(args) > 1 {
				return exitWithMessage("diff 只接受一个 Markdown 文件参数", 1)
			}
			// 运行时错误以 exit 2 区分「有差异」(exitError, exit 1)，对齐 git diff 语义
			err := handleDiffCommand(args[0])
			var ee *exitError
			if err != nil && !errors.As(err, &ee) {
				return &codedError{err: err, code: 2}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&diffOpts.invert, "invert", "i", false, "Invert diff direction (remote → local)")
	return cmd
}

func newOpenCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "open <file.md> [file2.md ...]",
		Short:             "Open the source Feishu document URL in the browser",
		ValidArgsFunction: mdFileCompletion(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("请至少指定一个 Markdown 文件", 1)
			}
			return handleOpenCommand(args)
		},
	}
}

func newSearchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "search <query>",
		Short:             "Search docs and wiki visible to the current user (requires user_access_token)",
		ValidArgsFunction: noFileCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSearchArgs(args, searchOpts); err != nil {
				return err
			}
			return handleSearchCommand(cmd.Context(), args[0])
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&searchOpts.docTypes, "doc-types", "", "Filter by document types, comma-separated (doc, sheet, bitable, mindnote, file, wiki, docx, folder, catalog, slides, shortcut)")
	fl.StringVar(&searchOpts.space, "space", "", "Restrict search to wiki space IDs, comma-separated (mutually exclusive with --folder)")
	fl.StringVar(&searchOpts.folder, "folder", "", "Restrict search to drive folder tokens, comma-separated (mutually exclusive with --space)")
	fl.BoolVar(&searchOpts.onlyTitle, "only-title", false, "Match document titles only")
	fl.StringVar(&searchOpts.sort, "sort", "", "Sort order: default, edit_time, edit_time_asc, open_time, create_time")
	fl.Int64Var(&searchOpts.pageSize, "page-size", 20, "Number of results per page (1-20)")
	fl.StringVar(&searchOpts.pageToken, "page-token", "", "Page token returned by the previous page to fetch the next page")
	fl.Int64Var(&searchOpts.limit, "limit", 0, "Fetch up to N results by paging automatically, 1-1000 (0 = single page)")
	fl.BoolVar(&searchOpts.asJSON, "json", false, "Emit machine-readable JSON (results, total, has_more, page_token)")
	_ = cmd.RegisterFlagCompletionFunc("doc-types", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return searchDocTypes, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("sort", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return searchSortTypes, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newOCRCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ocr [image-file]",
		Short: "Recognize text from an image using Feishu AI OCR",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return []cobra.Completion{"png", "jpg", "jpeg", "gif", "bmp", "webp"}, cobra.ShellCompDirectiveFilterFileExt
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitWithMessage("ocr 只接受一个图片文件参数", 1)
			}
			var imagePath string
			if len(args) > 0 {
				imagePath = args[0]
			}
			return handleOCRCommand(cmd.Context(), imagePath)
		},
	}
}

func newSkillsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "Show how to install and upgrade the larkdown agent skill for Claude Code",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitWithMessage(fmt.Sprintf("skills 不接受位置参数: %s", args[0]), 1)
			}
			return handleSkillsCommand()
		},
	}
}

func newRootCommand() *cobra.Command {
	short := "Feishu/Lark documents <-> Markdown: download, upload, sync and publish"
	root := &cobra.Command{
		Use:               "larkdown",
		Short:             short,
		Long:              short + "\n\nProject home: https://github.com/amzyang/larkdown",
		Version:           strings.TrimSpace(version),
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// 为所有子命令构造客户端选项，并校验全局身份取值
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// shell 补全路径（__complete）必须零副作用、低延迟，跳过遥测初始化
			if cmd.Name() != cobra.ShellCompRequestCmd && cmd.Name() != cobra.ShellCompNoDescRequestCmd {
				envValue, envSet := os.LookupEnv("SENTRY_DSN")
				dsn, _ := resolveSentryDSN(globalOpts.sentryDSN, cmd.Flags().Changed("sentry-dsn"),
					os.Getenv("DO_NOT_TRACK"), envValue, envSet, sentryDSN)
				initSentry(dsn, strings.TrimSpace(version), globalOpts.debug)
			}
			globalOpts.clientOpts = &core.ClientOptions{Debug: globalOpts.debug}
			if globalOpts.as != identityUser && globalOpts.as != identityBot {
				return exitWithMessage(fmt.Sprintf("--as 必须是 'user' 或 'bot'（收到 %q）", globalOpts.as), 1)
			}
			return nil
		},
		// 无 RunE：裸 larkdown 打印 help 到 stdout，exit 0
	}
	root.PersistentFlags().BoolVar(&globalOpts.debug, "debug", false, "Enable HTTP request/response logging to stderr (JSONL format)")
	root.PersistentFlags().StringVar(&globalOpts.as, "as", identityUser, "Identity for Feishu API calls: user (user_access_token, default) or bot (tenant_access_token app credentials)")
	root.PersistentFlags().StringVar(&globalOpts.sentryDSN, "sentry-dsn", "", "Sentry DSN for crash/error reporting (overrides SENTRY_DSN env and the build-time default; pass an empty value to disable)")
	root.PersistentFlags().StringVar(&globalOpts.configPath, "config", "", "Path to the config file (overrides LARKDOWN_CONFIG env; default ~/.config/feishu2md/config.json)")
	_ = root.MarkPersistentFlagFilename("config", "json")
	_ = root.RegisterFlagCompletionFunc("as", cobra.FixedCompletions(
		[]cobra.Completion{identityUser, identityBot}, cobra.ShellCompDirectiveNoFileComp))
	// flag 解析错误：单行错误 + --help 提示（不 dump 整页 usage），退出码 1
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &exitError{
			msg:  fmt.Sprintf("%v\nRun '%s --help' for usage.", err, cmd.CommandPath()),
			code: 1,
		}
	})
	// 帮助页按用途分组：核心转换 / 认证配置 / 辅助工具；隐藏命令（login 别名、sentry）不分组
	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core Commands:"},
		&cobra.Group{ID: "setup", Title: "Setup Commands:"},
		&cobra.Group{ID: "extras", Title: "Additional Commands:"},
	)
	addToGroup := func(groupID string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = groupID
			root.AddCommand(c)
		}
	}
	addToGroup("core",
		newDownloadCommand(),
		newMirrorCommand(),
		newUploadCommand(),
		newDiffCommand(),
		newOpenCommand(),
		newSearchCommand(),
	)
	addToGroup("setup",
		newAuthCommand(),
		newConfigCommand(),
	)
	addToGroup("extras",
		newPublishCommand(),
		newOCRCommand(),
		newSkillsCommand(),
		newCompletionCommand(),
	)
	root.AddCommand(newLoginAliasCommand(), newSentryCommand())
	root.SetHelpCommandGroupID("extras")
	return root
}

func main() {
	defer sentryRecoverRepanic()
	err := newRootCommand().ExecuteContext(context.Background())
	if err == nil {
		return
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(os.Stderr, ee.msg)
		}
		os.Exit(ee.code)
	}
	// 未知子命令与 handler 运行时错误：干净单行到 stderr（不带 log 时间戳），退出码
	// 默认 1、codedError 可覆写（diff 运行错误 = 2）。先打印再上报，Flush 阻塞不影响
	// 用户第一时间看到错误；exitError（用户输入类）与用户主动取消/超时不上报 Sentry。
	code := 1
	var ce *codedError
	if errors.As(err, &ce) {
		code = ce.code
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		sentry.CaptureException(err)
		sentry.Flush(2 * time.Second)
	}
	os.Exit(code)
}
