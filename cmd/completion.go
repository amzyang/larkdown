package main

import (
	"github.com/spf13/cobra"
)

// newCompletionCommand 构造 `larkdown completion` 命令：四种 shell 的补全脚本
// 全部由 cobra 运行时生成（动态补全，与命令定义永不漂移）。
//
// 输出必须走 stdout：brew 的 generate_completions_from_executable 依赖
// `larkdown completion bash|zsh|fish` 从 stdout 捕获脚本（.goreleaser.yaml）。
// `pwsh` 保留为 powershell 的别名，兼容 urfave/cli 时代的子命令名。
func newCompletionCommand() *cobra.Command {
	completion := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Output shell completion script for bash, zsh, fish, or PowerShell",
		// 拼错 shell 名报错而非 help + exit 0；RunE 仅为让 Args 校验生效（cobra 对非
		// Runnable 命令跳过 ValidateArgs），裸 `larkdown completion` 仍打印 help
		Args: rejectUnknownSubcommand,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
		Long: `Output shell completion script. Source the output to enable completion.

  # bash (~/.bashrc)
  source <(larkdown completion bash)

  # zsh (~/.zshrc)
  source <(larkdown completion zsh)

  # fish
  larkdown completion fish > ~/.config/fish/completions/larkdown.fish

  # PowerShell
  larkdown completion powershell | Out-String | Invoke-Expression

Homebrew installs completions for bash/zsh/fish automatically.`,
	}
	completion.AddCommand(
		&cobra.Command{
			Use:   "bash",
			Short: "Output bash completion script",
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			},
		},
		&cobra.Command{
			Use:   "zsh",
			Short: "Output zsh completion script",
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			},
		},
		&cobra.Command{
			Use:   "fish",
			Short: "Output fish completion script",
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			},
		},
		&cobra.Command{
			Use:     "powershell",
			Aliases: []string{"pwsh"},
			Short:   "Output PowerShell completion script",
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			},
		},
	)
	return completion
}
