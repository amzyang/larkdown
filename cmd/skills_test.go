package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatSkillsHelp(t *testing.T) {
	joined := strings.Join(formatSkillsHelp(), "\n")

	assert.Contains(t, joined, "npx skills add amzyang/larkdown --skill larkdown -g -a claude-code")
	assert.Contains(t, joined, "npx skills update larkdown")
	assert.Contains(t, joined, "skills/larkdown")
	assert.Contains(t, joined, "https://github.com/amzyang/larkdown#agent-skills")
}
