package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

// mkTextBlock 构造一个含单段纯文本的 text-like 块。
func mkTextBlock(bt lark.DocxBlockType, content string) *lark.DocxBlock {
	text := &lark.DocxBlockText{Elements: []*lark.DocxTextElement{
		{TextRun: &lark.DocxTextElementTextRun{Content: content}},
	}}
	b := &lark.DocxBlock{BlockType: bt}
	switch bt {
	case lark.DocxBlockTypeText:
		b.Text = text
	case lark.DocxBlockTypeHeading2:
		b.Heading2 = text
	case lark.DocxBlockTypeBullet:
		b.Bullet = text
	case lark.DocxBlockTypeOrdered:
		b.Ordered = text
	case lark.DocxBlockTypeQuote:
		b.Quote = text
	case lark.DocxBlockTypeTodo:
		b.Todo = text
	}
	return b
}

func TestLocalBlockMarkdownContent(t *testing.T) {
	u := NewUploaderWithPaths(nil, NewStatePaths(t.TempDir()))

	cases := []struct {
		bt   lark.DocxBlockType
		want string
	}{
		{lark.DocxBlockTypeText, "hello"},
		{lark.DocxBlockTypeHeading2, "## hello"},
		{lark.DocxBlockTypeBullet, "- hello"},
		{lark.DocxBlockTypeOrdered, "1. hello"},
		{lark.DocxBlockTypeQuote, "> hello"},
	}
	for _, c := range cases {
		got, ok := u.localBlockMarkdownContent(mkTextBlock(c.bt, "hello"))
		assert.True(t, ok)
		assert.Equal(t, c.want, got)
	}

	// 非 text-like → ok=false（留给删除重建）
	_, ok := u.localBlockMarkdownContent(&lark.DocxBlock{BlockType: lark.DocxBlockTypeDivider})
	assert.False(t, ok)
}

func TestLocalBlockMarkdownContent_Todo(t *testing.T) {
	u := NewUploaderWithPaths(nil, NewStatePaths(t.TempDir()))

	done := mkTextBlock(lark.DocxBlockTypeTodo, "task")
	done.Todo.Style = &lark.DocxTextStyle{Done: true}
	got, ok := u.localBlockMarkdownContent(done)
	assert.True(t, ok)
	assert.Equal(t, "- [x] task", got)

	undone := mkTextBlock(lark.DocxBlockTypeTodo, "task")
	got, ok = u.localBlockMarkdownContent(undone)
	assert.True(t, ok)
	assert.Equal(t, "- [ ] task", got)
}

// TestClassifyDeletion 验证 dryrun 报告与 collectDeletions（执行）共用的删除分类逻辑。
func TestClassifyDeletion(t *testing.T) {
	blockMap := map[string]*lark.DocxBlock{}

	// 普通文本块 → 真实删除
	normal := mkTextBlock(lark.DocxBlockTypeText, "hello")
	normal.BlockID = "b_normal"
	assert.Equal(t, deletionExecute, classifyDeletion(normal, blockMap, nil))

	// 空文本块 → 跳过删除
	empty := &lark.DocxBlock{BlockID: "b_empty", BlockType: lark.DocxBlockTypeText}
	assert.Equal(t, deletionSkipEmpty, classifyDeletion(empty, blockMap, nil))

	// 实体块且 token 仍被本地 markdown 引用 → 保留（重排而非删除）
	img := &lark.DocxBlock{
		BlockID:   "b_img",
		BlockType: lark.DocxBlockTypeImage,
		Image:     &lark.DocxBlockImage{Token: "img_token"},
	}
	assert.Equal(t, deletionPreserveEntity, classifyDeletion(img, blockMap, map[string]bool{"img_token": true}))
	// token 不再被引用 → 真实删除
	assert.Equal(t, deletionExecute, classifyDeletion(img, blockMap, map[string]bool{}))
}

// TestCollectDeletionsMirrorsClassify 验证 collectDeletions 与 classifyDeletion 口径一致：
// 仅 deletionExecute 的块进入删除列表。
func TestCollectDeletionsMirrorsClassify(t *testing.T) {
	normal := mkTextBlock(lark.DocxBlockTypeText, "hello")
	normal.BlockID = "b_normal"
	empty := &lark.DocxBlock{BlockID: "b_empty", BlockType: lark.DocxBlockTypeText}
	img := &lark.DocxBlock{
		BlockID:   "b_img",
		BlockType: lark.DocxBlockTypeImage,
		Image:     &lark.DocxBlockImage{Token: "img_token"},
	}
	rootBlocks := []*lark.DocxBlock{normal, empty, img}
	pairedOps := []PairedOp{
		{Type: PairedOpDelete, RemoteIdx: 0},
		{Type: PairedOpDelete, RemoteIdx: 1},
		{Type: PairedOpDelete, RemoteIdx: 2},
	}

	got := collectDeletions(pairedOps, rootBlocks, nil, map[string]bool{"img_token": true})
	assert.Equal(t, []string{"b_normal"}, got)
}
