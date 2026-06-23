package core

import (
	"errors"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mentionFallbackText：有显示名用显示名，空/缺失回退 user-id 本身。
func TestMentionFallbackText(t *testing.T) {
	u := &Uploader{mentionUserNames: map[string]string{
		"on_known": "张三",
		"on_empty": "", // 空显示名视为缺失
	}}
	assert.Equal(t, "张三", u.mentionFallbackText("on_known"))
	assert.Equal(t, "on_empty", u.mentionFallbackText("on_empty"))
	assert.Equal(t, "on_missing", u.mentionFallbackText("on_missing"))
}

// downgradeElements：mention_user 原地变纯文本，非 mention 不动，nil 跳过。
func TestDowngradeElements(t *testing.T) {
	u := &Uploader{mentionUserNames: map[string]string{"on_a": "张三"}}
	elems := []*lark.DocxTextElement{
		{TextRun: &lark.DocxTextElementTextRun{Content: "前 "}},
		{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_a"}}, // → 显示名
		{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_b"}}, // 无显示名 → user-id
		nil,
	}

	names := u.downgradeElements(elems)

	assert.Equal(t, []string{"张三", "on_b"}, names)
	require.Nil(t, elems[1].MentionUser)
	assert.Equal(t, "张三", elems[1].TextRun.Content)
	require.Nil(t, elems[2].MentionUser)
	assert.Equal(t, "on_b", elems[2].TextRun.Content)
	// 普通文本不受影响
	assert.Equal(t, "前 ", elems[0].TextRun.Content)
}

// downgradeMentionUsersInBlocks：遍历各类文本块；getBlockText 不支持的块类型与 nil 安全跳过。
func TestDowngradeMentionUsersInBlocks(t *testing.T) {
	u := &Uploader{mentionUserNames: map[string]string{"on_a": "李四"}}
	blocks := []*lark.DocxBlock{
		{
			BlockType: lark.DocxBlockTypeText,
			Text: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{
				{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_a"}},
			}},
		},
		{
			BlockType: lark.DocxBlockTypeHeading1,
			Heading1: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{
				{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_b"}},
			}},
		},
		nil,
		{BlockType: lark.DocxBlockType(999)}, // getBlockText 返回 nil → 跳过不崩
	}

	names := u.downgradeMentionUsersInBlocks(blocks)

	assert.ElementsMatch(t, []string{"李四", "on_b"}, names)
	assert.Nil(t, blocks[0].Text.Elements[0].MentionUser)
	assert.Equal(t, "李四", blocks[0].Text.Elements[0].TextRun.Content)
	assert.Equal(t, "on_b", blocks[1].Heading1.Elements[0].TextRun.Content)
}

// downgradeMentionUsersInRequests：UpdateTextElements / UpdateText 两种字段都降级，nil 与空请求跳过。
func TestDowngradeMentionUsersInRequests(t *testing.T) {
	u := &Uploader{mentionUserNames: map[string]string{"on_a": "王五"}}
	requests := []*lark.BatchUpdateDocxDocumentBlockReqRequest{
		{UpdateTextElements: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateTextElements{
			Elements: []*lark.DocxTextElement{
				{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_a"}},
			},
		}},
		{UpdateText: &lark.BatchUpdateDocxDocumentBlockReqRequestUpdateText{
			Elements: []*lark.DocxTextElement{
				{MentionUser: &lark.DocxTextElementMentionUser{UserID: "on_b"}},
			},
		}},
		nil,
		{}, // 无文本更新字段 → 跳过
	}

	names := u.downgradeMentionUsersInRequests(requests)

	assert.ElementsMatch(t, []string{"王五", "on_b"}, names)
	assert.Equal(t, "王五", requests[0].UpdateTextElements.Elements[0].TextRun.Content)
	assert.Equal(t, "on_b", requests[1].UpdateText.Elements[0].TextRun.Content)
}

// withMentionFallback：失败→降级→重试一次的编排逻辑（不依赖飞书 client）。
func TestWithMentionFallback(t *testing.T) {
	t.Run("首次成功-不触发降级", func(t *testing.T) {
		writes, downgrades := 0, 0
		err := withMentionFallback(
			func() error { writes++; return nil },
			func() []string { downgrades++; return []string{"x"} },
		)
		require.NoError(t, err)
		assert.Equal(t, 1, writes)
		assert.Equal(t, 0, downgrades)
	})

	t.Run("首次失败-降级后重试成功", func(t *testing.T) {
		writes := 0
		err := withMentionFallback(
			func() error {
				writes++
				if writes == 1 {
					return errors.New("boom")
				}
				return nil
			},
			func() []string { return []string{"张三"} },
		)
		require.NoError(t, err)
		assert.Equal(t, 2, writes)
	})

	t.Run("无可降级项-返回原错误不重试", func(t *testing.T) {
		boom := errors.New("boom")
		writes := 0
		err := withMentionFallback(
			func() error { writes++; return boom },
			func() []string { return nil },
		)
		assert.Equal(t, boom, err)
		assert.Equal(t, 1, writes)
	})

	t.Run("重试仍失败-返回首次原错误", func(t *testing.T) {
		first, second := errors.New("first"), errors.New("second")
		writes := 0
		err := withMentionFallback(
			func() error {
				writes++
				if writes == 1 {
					return first
				}
				return second
			},
			func() []string { return []string{"张三"} },
		)
		assert.Equal(t, first, err) // 返回首次错误，而非重试错误
		assert.Equal(t, 2, writes)
	})
}
