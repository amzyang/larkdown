package main

import "fmt"

// formatSkillsHelp 生成 skills 子命令的帮助文本行（纯函数，便于测试）。
func formatSkillsHelp() []string {
	return []string{
		"larkdown ships agent skills for Claude Code, maintained in the repo under skills/",
		"(larkdown: Feishu <-> Markdown workflows; release: maintainer release flow).",
		"",
		"Install (recommended, via npx skills):",
		"  npx skills add amzyang/larkdown --skill larkdown -g -a claude-code",
		"",
		"  # list all skills available in the repo",
		"  npx skills add amzyang/larkdown --list",
		"",
		"Upgrade:",
		"  npx skills update larkdown",
		"",
		"  # or update all installed skills",
		"  npx skills update",
		"",
		"Manual install (alternative):",
		"  clone the repo and symlink/copy skills/larkdown/ to ~/.claude/skills/larkdown/;",
		"  upgrade by pulling the repo and re-copying.",
		"",
		"More: https://github.com/amzyang/larkdown#agent-skills",
	}
}

func handleSkillsCommand() error {
	for _, line := range formatSkillsHelp() {
		fmt.Println(line)
	}
	return nil
}
