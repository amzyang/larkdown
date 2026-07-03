package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/amzyang/larkdown/core"
	"github.com/urfave/cli/v3"
)

var version = "v2-test"

// globalOpts 全局选项
var globalOpts = struct {
	debug      bool
	clientOpts *core.ClientOptions
}{}

// createClientFromConfig 从配置文件创建客户端，处理 token 刷新和降级逻辑。
// 认证相关提示一律走 stderr，避免污染可管道化的 stdout 输出。
func createClientFromConfig(ctx context.Context, config *core.Config, configPath string) *core.Client {
	if config.Feishu.HasValidUserToken() {
		fmt.Fprintln(os.Stderr, "使用认证方式: user_access_token")
		return core.NewClientWithUserToken(
			config.Feishu.AppId,
			config.Feishu.AppSecret,
			config.Feishu.UserAccessToken,
			globalOpts.clientOpts,
		)
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
			)
		case core.RefreshOutcomeTokenInvalidCleared:
			fmt.Fprintf(os.Stderr, "user_access_token 已失效（%v），请重新执行 larkdown auth login；本次使用应用凭证 (tenant_access_token)\n", err)
		case core.RefreshOutcomeTransientFailure:
			fmt.Fprintf(os.Stderr, "刷新 token 失败（网络波动？%v），本次使用应用凭证；token 保留，下次自动重试\n", err)
		}
	}
	fmt.Fprintln(os.Stderr, "使用认证方式: tenant_access_token (应用凭证)")
	return core.NewClient(config.Feishu.AppId, config.Feishu.AppSecret, globalOpts.clientOpts)
}

func newRootCommand() *cli.Command {
	return &cli.Command{
		Name:                            "larkdown",
		Version:                         strings.TrimSpace(string(version)),
		Usage:                           "Feishu/Lark documents <-> Markdown: download, upload, sync and publish",
		EnableShellCompletion:           true,
		ConfigureShellCompletionCommand: configureShellCompletion,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable HTTP request/response logging to stderr (JSONL format)",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			globalOpts.debug = cmd.Bool("debug")
			globalOpts.clientOpts = &core.ClientOptions{Debug: globalOpts.debug}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cli.ShowAppHelp(cmd)
			return nil
		},
		Commands: []*cli.Command{
			{
				Name:  "config",
				Usage: "Read config file or set field(s) if provided",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "appId", Usage: "Set app id for the OPEN API"},
					&cli.StringFlag{Name: "appSecret", Usage: "Set app secret for the OPEN API"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					configOpts.appId = cmd.String("appId")
					configOpts.appSecret = cmd.String("appSecret")
					return handleConfigCommand()
				},
			},
			{
				Name:    "download",
				Aliases: []string{"dl"},
				Usage:   "Download feishu/larksuite document to markdown file",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: "./", Usage: "Specify the output directory for the markdown files"},
					&cli.BoolFlag{Name: "recursive", Aliases: []string{"r"}, Usage: "Recursively download all child nodes of a wiki node"},
					&cli.BoolFlag{Name: "comments", Aliases: []string{"c"}, Value: true, Usage: "Include document comments in the exported Markdown"},
					&cli.BoolFlag{Name: "no-diff", Usage: "Disable diff output when downloading"},
					&cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Force re-download even if the remote document is unchanged"},
					&cli.BoolFlag{Name: "follow", Usage: "Also download referenced docx/wiki documents (mentions and inline links) into _refs/"},
					&cli.IntFlag{Name: "follow-depth", Value: 1, Usage: "How many levels of references to follow (requires --follow)"},
				},
				ArgsUsage: "<url>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return cli.Exit("Please specify the document/folder/wiki url", 1)
					}
					dlOpts.outputDir = cmd.String("output")
					dlOpts.recursive = cmd.Bool("recursive")
					dlOpts.comments = cmd.Bool("comments")
					dlOpts.noDiff = cmd.Bool("no-diff")
					dlOpts.force = cmd.Bool("force")
					dlOpts.follow = cmd.Bool("follow")
					dlOpts.followDepth = cmd.Int("follow-depth")
					if cmd.IsSet("follow-depth") && !dlOpts.follow {
						return cli.Exit("--follow-depth requires --follow", 1)
					}
					if dlOpts.followDepth < 1 {
						return cli.Exit("--follow-depth must be >= 1", 1)
					}

					url := cmd.Args().First()
					return handleDownloadCommand(url)
				},
			},
			{
				Name:      "mirror",
				Usage:     "One-way sync (download-only) a wiki/folder into a local mirror directory with CLAUDE.md and index files",
				ArgsUsage: "[url]",
				Description: "Mirror a Feishu wiki space, wiki subtree or drive folder into a local directory:\n" +
					"the output directory itself is the mirror root. Always generates llms.txt,\n" +
					"docs_map.md and a CLAUDE.md explaining the layout, and prunes local documents\n" +
					"that no longer exist remotely (moved to trash). Re-run without <url> inside a\n" +
					"mirror directory to re-sync from the recorded source.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: "./", Usage: "Mirror root directory"},
					&cli.BoolFlag{Name: "comments", Aliases: []string{"c"}, Value: true, Usage: "Include document comments in the exported Markdown"},
					&cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Force re-download even if the remote document is unchanged"},
					&cli.BoolFlag{Name: "no-prune", Usage: "Keep local files whose remote documents were deleted"},
					&cli.BoolFlag{Name: "follow", Usage: "Also download referenced docx/wiki documents (mentions and inline links) into _refs/; recorded in the mirror manifest for re-sync"},
					&cli.IntFlag{Name: "follow-depth", Value: 1, Usage: "How many levels of references to follow (requires --follow)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					mirrorOpts.outputDir = cmd.String("output")
					mirrorOpts.comments = cmd.Bool("comments")
					mirrorOpts.force = cmd.Bool("force")
					mirrorOpts.noPrune = cmd.Bool("no-prune")
					mirrorOpts.follow = cmd.Bool("follow")
					mirrorOpts.followDepth = cmd.Int("follow-depth")
					mirrorOpts.followSet = cmd.IsSet("follow")
					mirrorOpts.depthSet = cmd.IsSet("follow-depth")
					return handleMirrorCommand(cmd.Args().First())
				},
			},
			newAuthCommand(),
			newLoginAliasCommand(),
			{
				Name:      "upload",
				Aliases:   []string{"ul"},
				Usage:     "Upload local markdown file to Feishu Wiki",
				ArgsUsage: "<file.md>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "source", Usage: "Target Feishu document URL (mutually exclusive with --space/--parent)"},
					&cli.StringFlag{Name: "space", Aliases: []string{"s"}, Usage: "Wiki space ID (optional, defaults to My Document Library)"},
					&cli.StringFlag{Name: "parent", Aliases: []string{"p"}, Usage: "Parent node token (optional, for specifying location)"},
					&cli.BoolFlag{Name: "incremental", Aliases: []string{"incr"}, Hidden: true, Usage: "Incremental update (default behavior; kept for backward compatibility)"},
					&cli.BoolFlag{Name: "full", Usage: "Full update (delete all remote blocks and re-upload) instead of the default incremental update"},
					&cli.BoolFlag{Name: "dryrun", Usage: "Show what incremental update would do without making changes (incompatible with --full)"},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Show all blocks including unchanged ones (used with --dryrun)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return cli.Exit("Please specify the markdown file to upload", 1)
					}
					uploadOpts.source = cmd.String("source")
					uploadOpts.spaceID = cmd.String("space")
					uploadOpts.parentNodeToken = cmd.String("parent")
					if cmd.Bool("full") && cmd.Bool("incremental") {
						return cli.Exit("--full cannot be used with --incremental/--incr", 1)
					}
					uploadOpts.incremental = !cmd.Bool("full")
					uploadOpts.dryRun = cmd.Bool("dryrun")
					uploadOpts.verbose = cmd.Bool("verbose")
					if uploadOpts.source != "" && (uploadOpts.spaceID != "" || uploadOpts.parentNodeToken != "") {
						return cli.Exit("--source cannot be used with --space or --parent", 1)
					}
					if uploadOpts.dryRun && !uploadOpts.incremental {
						return cli.Exit("--dryrun cannot be used with --full", 1)
					}
					return handleUploadCommand(cmd.Args().First())
				},
			},
			{
				Name:      "publish",
				Usage:     "Publish a local HTML file or directory as an online Feishu Miaoda (妙搭) app",
				ArgsUsage: "<dir-or-html>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Aliases: []string{"n"}, Usage: "App display name (defaults to the file/dir name)"},
					&cli.StringFlag{Name: "app-id", Usage: "Reuse an existing app to update it (app_xxx or https://miaoda.feishu.cn/app/app_xxx)"},
					&cli.BoolFlag{Name: "new", Usage: "Force creating a new app even if a publish record exists"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return cli.Exit("Please specify the HTML file or directory to publish", 1)
					}
					publishOpts.name = cmd.String("name")
					publishOpts.appID = cmd.String("app-id")
					publishOpts.forceNew = cmd.Bool("new")
					if publishOpts.appID != "" && publishOpts.forceNew {
						return cli.Exit("--app-id cannot be used with --new", 1)
					}
					return handlePublishCommand(cmd.Args().First())
				},
			},
			{
				Name:      "diff",
				Usage:     "Show diff between local markdown and remote Feishu document",
				ArgsUsage: "<file.md>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "invert", Aliases: []string{"i"}, Usage: "Invert diff direction (remote → local)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return cli.Exit("Please specify the markdown file", 1)
					}
					diffOpts.invert = cmd.Bool("invert")
					return handleDiffCommand(cmd.Args().First())
				},
			},
			{
				Name:      "open",
				Usage:     "Open the source Feishu document URL in the browser",
				ArgsUsage: "<file.md> [file2.md ...]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return cli.Exit("Please specify at least one markdown file", 1)
					}
					return handleOpenCommand(cmd.Args().Slice())
				},
			},
			{
				Name:      "ocr",
				Usage:     "Recognize text from an image using Feishu AI OCR",
				ArgsUsage: "[image-file]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					imagePath := cmd.Args().First()
					return handleOCRCommand(ctx, imagePath)
				},
			},
		},
	}
}

func main() {
	if err := newRootCommand().Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
