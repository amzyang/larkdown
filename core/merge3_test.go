package core

import (
	"strings"
	"testing"
)

const merge3Base = `# 会议纪要

第一段落的内容。

## 决议

- 事项一
- 事项二

结尾段落。
`

func TestMergeMarkdownRemoteOnlyChange(t *testing.T) {
	remote := strings.Replace(merge3Base, "第一段落的内容。", "第一段落的内容（远端修订）。", 1)
	out, err := MergeMarkdown(merge3Base, merge3Base, remote, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Conflicts {
		t.Fatalf("仅远端变化不应冲突: %s", out.Body)
	}
	if !strings.Contains(out.Body, "远端修订") {
		t.Errorf("远端改动未合入: %s", out.Body)
	}
}

func TestMergeMarkdownLocalOnlyChange(t *testing.T) {
	local := strings.Replace(merge3Base, "结尾段落。", "结尾段落（本地补充）。", 1)
	out, err := MergeMarkdown(merge3Base, local, merge3Base, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Conflicts {
		t.Fatalf("仅本地变化不应冲突: %s", out.Body)
	}
	if !strings.Contains(out.Body, "本地补充") {
		t.Errorf("本地改动未保留: %s", out.Body)
	}
}

func TestMergeMarkdownNonOverlappingBothSides(t *testing.T) {
	local := strings.Replace(merge3Base, "结尾段落。", "结尾段落（本地补充）。", 1)
	remote := strings.Replace(merge3Base, "第一段落的内容。", "第一段落的内容（远端修订）。", 1)
	out, err := MergeMarkdown(merge3Base, local, remote, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Conflicts {
		t.Fatalf("互不重叠的双侧改动不应冲突: %s", out.Body)
	}
	if !strings.Contains(out.Body, "本地补充") || !strings.Contains(out.Body, "远端修订") {
		t.Errorf("双侧改动未同时合入: %s", out.Body)
	}
}

func TestMergeMarkdownConflictOnSameLine(t *testing.T) {
	local := strings.Replace(merge3Base, "第一段落的内容。", "第一段落（本地版本）。", 1)
	remote := strings.Replace(merge3Base, "第一段落的内容。", "第一段落（远端版本）。", 1)
	out, err := MergeMarkdown(merge3Base, local, remote, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Conflicts {
		t.Fatalf("同一行双侧改动应冲突: %s", out.Body)
	}
	if !HasUnresolvedConflictMarkers(out.Body) {
		t.Errorf("冲突输出应含自产标记: %s", out.Body)
	}
	if !strings.Contains(out.Body, "本地版本") || !strings.Contains(out.Body, "远端版本") {
		t.Errorf("冲突两侧内容应都在标记内: %s", out.Body)
	}
}

// 本地是手写 markdown（* 号列表、setext 标题等非规范拼写），语义与 base 相同、
// 仅一处真实编辑；远端另一处真实修订。归一化应消除拼写噪音，只合并真实改动。
func TestMergeMarkdownNormalizationKillsFalseConflicts(t *testing.T) {
	local := `会议纪要
=========

第一段落的内容。

## 决议

* 事项一
* 事项二

结尾段落（本地补充）。
`
	remote := strings.Replace(merge3Base, "- 事项二", "- 事项二（远端修订）", 1)
	out, err := MergeMarkdown(merge3Base, local, remote, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Conflicts {
		t.Fatalf("拼写噪音不应产生假冲突: %s", out.Body)
	}
	if !strings.Contains(out.Body, "本地补充") || !strings.Contains(out.Body, "远端修订") {
		t.Errorf("真实改动未合入: %s", out.Body)
	}
	if !strings.Contains(out.Body, "# 会议纪要") {
		t.Errorf("输出应为归一化形态（ATX 标题）: %s", out.Body)
	}
}
