package main

import (
	"context"
	_ "embed"

	"github.com/urfave/cli/v3"
)

//go:embed completions/larkdown.fish
var fishCompletionScript string

// configureShellCompletion 用手写的 fish 补全脚本替换 urfave/cli 自动生成的版本。
//
// urfave/cli v3 把 `completion` 做成父命令，bash/zsh/fish/pwsh 各是带独立 Action
// 的子命令；`larkdown completion fish` 路由到 fish 子命令的 Action（父命令 Action 为 nil）。
// 因此这里覆盖 fish 子命令的 Action，而非父命令。输出写 cmd.Root().Writer，与 urfave
// 内置子命令一致，确保 brew 的 generate_completions_from_executable 能从 stdout 捕获。
func configureShellCompletion(completionCmd *cli.Command) {
	for _, sub := range completionCmd.Commands {
		if sub.Name == "fish" {
			sub.Action = func(ctx context.Context, cmd *cli.Command) error {
				_, err := cmd.Root().Writer.Write([]byte(fishCompletionScript))
				return err
			}
			return
		}
	}
}
