package main

import (
	"context"
	_ "embed"

	"github.com/urfave/cli/v3"
)

//go:embed completions/larkdown.fish
var fishCompletionScript string

func configureShellCompletion(completionCmd *cli.Command) {
	originalAction := completionCmd.Action
	completionCmd.Action = func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Args().First() == "fish" {
			_, err := cmd.Writer.Write([]byte(fishCompletionScript))
			return err
		}
		return originalAction(ctx, cmd)
	}
}
