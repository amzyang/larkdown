package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompletionSubcommands 锁住「larkdown completion <shell> 向 stdout 输出补全脚本」
// 这一公开行为：brew 的 generate_completions_from_executable 依赖 bash/zsh/fish 三种输出；
// pwsh 是 powershell 的兼容别名（urfave/cli 时代的子命令名）。
func TestCompletionSubcommands(t *testing.T) {
	cases := []struct{ shell, marker string }{
		{"bash", "__start_larkdown"},
		{"zsh", "#compdef larkdown"},
		{"fish", "__larkdown_perform_completion"},
		{"powershell", "Register-ArgumentCompleter"},
		{"pwsh", "Register-ArgumentCompleter"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			root := newRootCommand()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"completion", tc.shell})

			require.NoError(t, root.ExecuteContext(context.Background()))
			assert.Contains(t, out.String(), tc.marker)
		})
	}
}
