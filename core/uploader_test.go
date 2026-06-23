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
	u := NewUploader(nil)

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
	u := NewUploader(nil)

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
