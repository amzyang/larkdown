package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/amzyang/larkdown/core"
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
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
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
			return nil, fmt.Errorf("user_access_token 已失效（%v）。请重新执行 larkdown auth login，或加 --as bot 使用应用凭证 (tenant_access_token)", err)
		default: // RefreshOutcomeTransientFailure
			return nil, fmt.Errorf("刷新 user_access_token 失败（网络波动？%v）。token 已保留、稍后重试即可；或加 --as bot 使用应用凭证 (tenant_access_token)", err)
		}
	}
	if config.Feishu.UserAccessToken != "" || config.Feishu.RefreshToken != "" {
		return nil, errors.New("登录已过期（refresh_token 失效），请重新执行 larkdown auth login，或加 --as bot 使用应用凭证 (tenant_access_token)")
	}
	return nil, errors.New("当前未登录：larkdown 默认使用用户身份（user_access_token）。请先执行 larkdown auth login；批量自动化场景可加 --as bot 使用应用凭证 (tenant_access_token)")
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
			return handleConfigCommand()
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&configOpts.appId, "appId", "", "Set app id for the OPEN API")
	fl.StringVar(&configOpts.appSecret, "appSecret", "", "Set app secret for the OPEN API")
	cmd.AddCommand(newConfigInitCommand())
	return cmd
}

func newDownloadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "download <url>",
		Aliases:           []string{"dl"},
		Short:             "Download feishu/larksuite document to markdown file",
		ValidArgsFunction: noFileCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("Please specify the document/folder/wiki url", 1)
			}
			if cmd.Flags().Changed("follow-depth") && !dlOpts.follow {
				return exitWithMessage("--follow-depth requires --follow", 1)
			}
			if dlOpts.followDepth < 1 {
				return exitWithMessage("--follow-depth must be >= 1", 1)
			}
			return handleDownloadCommand(args[0])
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&dlOpts.outputDir, "output", "o", "./", "Specify the output directory for the markdown files")
	fl.BoolVarP(&dlOpts.recursive, "recursive", "r", false, "Recursively download all child nodes of a wiki node")
	fl.BoolVarP(&dlOpts.comments, "comments", "c", true, "Include document comments in the exported Markdown")
	fl.BoolVar(&dlOpts.noDiff, "no-diff", false, "Disable diff output when downloading")
	fl.BoolVarP(&dlOpts.force, "force", "f", false, "Force re-download even if the remote document is unchanged")
	fl.BoolVar(&dlOpts.follow, "follow", false, "Also download referenced docx/wiki documents (mentions and inline links) into _refs/")
	fl.IntVar(&dlOpts.followDepth, "follow-depth", 1, "How many levels of references to follow (requires --follow)")
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
			mirrorOpts.followSet = cmd.Flags().Changed("follow")
			mirrorOpts.depthSet = cmd.Flags().Changed("follow-depth")
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
	fl.BoolVarP(&mirrorOpts.force, "force", "f", false, "Force re-download even if the remote document is unchanged")
	fl.BoolVar(&mirrorOpts.noPrune, "no-prune", false, "Keep local files whose remote documents were deleted")
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
				return exitWithMessage("Please specify the markdown file to upload", 1)
			}
			full, _ := cmd.Flags().GetBool("full")
			incremental, _ := cmd.Flags().GetBool("incremental")
			if full && incremental {
				return exitWithMessage("--full cannot be used with --incremental/--incr", 1)
			}
			uploadOpts.incremental = !full
			if uploadOpts.source != "" && (uploadOpts.spaceID != "" || uploadOpts.parentNodeToken != "") {
				return exitWithMessage("--source cannot be used with --space or --parent", 1)
			}
			if uploadOpts.dryRun && !uploadOpts.incremental {
				return exitWithMessage("--dryrun cannot be used with --full", 1)
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
	fl.BoolVar(&uploadOpts.dryRun, "dryrun", false, "Show what incremental update would do without making changes (incompatible with --full)")
	fl.BoolVarP(&uploadOpts.verbose, "verbose", "v", false, "Show all blocks including unchanged ones (used with --dryrun)")
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
				return exitWithMessage("Please specify the HTML file or directory to publish", 1)
			}
			if publishOpts.appID != "" && publishOpts.forceNew {
				return exitWithMessage("--app-id cannot be used with --new", 1)
			}
			return handlePublishCommand(args[0])
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&publishOpts.name, "name", "n", "", "App display name (defaults to the file/dir name)")
	fl.StringVar(&publishOpts.appID, "app-id", "", "Reuse an existing app to update it (app_xxx or https://miaoda.feishu.cn/app/app_xxx)")
	fl.BoolVar(&publishOpts.forceNew, "new", false, "Force creating a new app even if a publish record exists")
	return cmd
}

func newDiffCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "diff <file.md>",
		Short:             "Show diff between local markdown and remote Feishu document",
		ValidArgsFunction: mdFileCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitWithMessage("Please specify the markdown file", 1)
			}
			return handleDiffCommand(args[0])
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
				return exitWithMessage("Please specify at least one markdown file", 1)
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
	fl.BoolVar(&searchOpts.asJSON, "json", false, "Emit machine-readable JSON (results, total, has_more, page_token)")
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
			globalOpts.clientOpts = &core.ClientOptions{Debug: globalOpts.debug}
			if globalOpts.as != identityUser && globalOpts.as != identityBot {
				return exitWithMessage(fmt.Sprintf("--as must be 'user' or 'bot' (got %q)", globalOpts.as), 1)
			}
			return nil
		},
		// 无 RunE：裸 larkdown 打印 help 到 stdout，exit 0
	}
	root.PersistentFlags().BoolVar(&globalOpts.debug, "debug", false, "Enable HTTP request/response logging to stderr (JSONL format)")
	root.PersistentFlags().StringVar(&globalOpts.as, "as", identityUser, "Identity for Feishu API calls: user (user_access_token, default) or bot (tenant_access_token app credentials)")
	_ = root.RegisterFlagCompletionFunc("as", cobra.FixedCompletions(
		[]cobra.Completion{identityUser, identityBot}, cobra.ShellCompDirectiveNoFileComp))
	// flag 解析错误：单行错误 + --help 提示（不 dump 整页 usage），退出码 1
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &exitError{
			msg:  fmt.Sprintf("%v\nRun '%s --help' for usage.", err, cmd.CommandPath()),
			code: 1,
		}
	})
	root.AddCommand(
		newConfigCommand(),
		newDownloadCommand(),
		newMirrorCommand(),
		newAuthCommand(),
		newLoginAliasCommand(),
		newUploadCommand(),
		newPublishCommand(),
		newDiffCommand(),
		newOpenCommand(),
		newSearchCommand(),
		newOCRCommand(),
		newSkillsCommand(),
		newCompletionCommand(),
	)
	return root
}

func main() {
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
	// 未知子命令与 handler 运行时错误：干净单行到 stderr（不带 log 时间戳），退出码 1
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}
