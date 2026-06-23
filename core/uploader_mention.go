package core

import (
	"context"
	"log"

	"github.com/chyroc/lark"
)

// @人（mention_user）写入降级。
//
// MentionUser 写入 API 无 fallback 字段（不同于 @文档的 FallbackToLink）。
// 策略：不做上传前预检，直接写入；若失败且 payload 含 mention_user，
// 则把所有 mention_user 降级为纯文本（用内嵌显示名）后重试一次。
// 重试成功 → 打印 warning 并继续，不因单个坏 mention 中断整篇上传；
// 重试仍失败 → 返回原始错误（说明问题不在 mention）。

// mentionFallbackText 返回 user-id 对应的降级文本：优先显示名，缺失则用 user-id 本身。
func (u *Uploader) mentionFallbackText(userID string) string {
	if name, ok := u.mentionUserNames[userID]; ok && name != "" {
		return name
	}
	return userID
}

// downgradeElements 把元素列表中的 mention_user 原地替换为纯文本，返回被降级的显示名。
func (u *Uploader) downgradeElements(elements []*lark.DocxTextElement) []string {
	var names []string
	for _, e := range elements {
		if e == nil || e.MentionUser == nil {
			continue
		}
		name := u.mentionFallbackText(e.MentionUser.UserID)
		names = append(names, name)
		e.MentionUser = nil
		e.TextRun = &lark.DocxTextElementTextRun{Content: name}
	}
	return names
}

// downgradeMentionUsersInBlocks 把块（含嵌套文本块）内所有 mention_user 降级为纯文本。
func (u *Uploader) downgradeMentionUsersInBlocks(blocks []*lark.DocxBlock) []string {
	var names []string
	for _, b := range blocks {
		if b == nil {
			continue
		}
		if text := getBlockText(b); text != nil {
			names = append(names, u.downgradeElements(text.Elements)...)
		}
	}
	return names
}

// downgradeMentionUsersInRequests 把批量更新请求里所有 mention_user 降级为纯文本。
func (u *Uploader) downgradeMentionUsersInRequests(requests []*lark.BatchUpdateDocxDocumentBlockReqRequest) []string {
	var names []string
	for _, r := range requests {
		if r == nil {
			continue
		}
		if r.UpdateTextElements != nil {
			names = append(names, u.downgradeElements(r.UpdateTextElements.Elements)...)
		}
		if r.UpdateText != nil {
			names = append(names, u.downgradeElements(r.UpdateText.Elements)...)
		}
	}
	return names
}

// warnMentionDowngrade 打印降级 warning。
func warnMentionDowngrade(names []string) {
	for _, n := range names {
		log.Printf("警告: @人 mention 写入失败，已降级为纯文本：%s", n)
	}
}

// withMentionFallback 执行一次写入；若失败且 payload 含可降级的 mention_user，
// 则降级为纯文本后重试一次：重试成功 → warning + nil；
// 无可降级项或重试仍失败 → 返回首次写入的原始错误。
//
// write 须可重复调用（第二次作用于已被 downgrade 原地改写的同一 payload）；
// downgrade 返回被降级的显示名，空切片表示 payload 不含 mention_user（即问题不在 mention）。
func withMentionFallback(write func() error, downgrade func() []string) error {
	err := write()
	if err == nil {
		return nil
	}
	names := downgrade()
	if len(names) == 0 {
		return err
	}
	if retryErr := write(); retryErr != nil {
		return err
	}
	warnMentionDowngrade(names)
	return nil
}

// createDescendantWithMentionFallback 包装 descendant 创建，附带 @人 写入降级。
func (u *Uploader) createDescendantWithMentionFallback(
	ctx context.Context,
	documentID, parentBlockID string,
	childrenIDs []string,
	descendants []*lark.DocxBlock,
	index int,
) (*lark.CreateDocxDocumentBlockDescendantResp, error) {
	var resp *lark.CreateDocxDocumentBlockDescendantResp
	err := withMentionFallback(
		func() error {
			var e error
			resp, e = u.client.CreateDocxDescendant(ctx, documentID, parentBlockID, childrenIDs, descendants, index)
			return e
		},
		func() []string { return u.downgradeMentionUsersInBlocks(descendants) },
	)
	return resp, err
}

// batchUpdateWithMentionFallback 包装批量更新，附带 @人 写入降级。
func (u *Uploader) batchUpdateWithMentionFallback(
	ctx context.Context,
	documentID string,
	requests []*lark.BatchUpdateDocxDocumentBlockReqRequest,
) error {
	return withMentionFallback(
		func() error { return u.client.BatchUpdateDocxBlocks(ctx, documentID, requests) },
		func() []string { return u.downgradeMentionUsersInRequests(requests) },
	)
}
