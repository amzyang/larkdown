package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDryRunPruneCandidates 锁清理预览口径：远端缺失的本地文档才是候选（输出有序）；
// follow 开启时 _refs/ 不列入——实际同步中由引用收集保护，预览不拉正文、无法评估引用。
func TestDryRunPruneCandidates(t *testing.T) {
	refsDir := filepath.Join("mirror", "_refs")
	local := map[string]string{
		"tokA": filepath.Join("mirror", "a.md"),
		"tokB": filepath.Join("mirror", "b.md"),
		"tokR": filepath.Join(refsDir, "r.md"),
	}
	remote := map[string]bool{"tokA": true}

	t.Run("follow 关闭时 _refs 一并列入", func(t *testing.T) {
		assert.Equal(t, []string{
			filepath.Join(refsDir, "r.md"),
			filepath.Join("mirror", "b.md"),
		}, dryRunPruneCandidates(local, remote, refsDir, false))
	})

	t.Run("follow 开启时排除 _refs", func(t *testing.T) {
		assert.Equal(t, []string{filepath.Join("mirror", "b.md")},
			dryRunPruneCandidates(local, remote, refsDir, true))
	})

	t.Run("远端齐全时无候选", func(t *testing.T) {
		full := map[string]bool{"tokA": true, "tokB": true, "tokR": true}
		assert.Empty(t, dryRunPruneCandidates(local, full, refsDir, false))
	})
}
