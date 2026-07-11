package core

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chyroc/lark"
)

// Comment 表示一条评论及其回复
type Comment struct {
	ID         string
	UserID     string
	CreateTime time.Time
	IsSolved   bool
	IsWhole    bool   // true=全文评论, false=局部评论
	Quote      string // 局部评论引用的原文
	Replies    []Reply
}

// Reply 表示评论的一条回复
type Reply struct {
	ID         string
	UserID     string
	CreateTime time.Time
	Content    string // 渲染后的文本内容
}

// CommentData 包含全文评论和局部评论
type CommentData struct {
	GlobalComments []Comment         // IsWhole=true
	LocalComments  []Comment         // IsWhole=false
	UserNames      map[string]string // UserID → Name 映射
}

// rawComment 是 Phase 1 累积的原始评论：渲染推迟到 Phase 3（姓名解析后），
// 这样评论内 person 元素才能填充显示名，而非渲染成原始 @<id>。
type rawComment struct {
	item        *lark.GetDriveCommentListRespItem
	moreReplies []*lark.GetDriveCommentReplyListRespItem // HasMore 时补抓的原始回复
}

// GetDocumentComments 获取文档所有评论（支持分页）。
//
// 两段式：先抓取累积原始 item 并收集全部 user id（作者 + person 元素），一次性解析
// 姓名后再渲染。person 元素必须经 basic_batch 取名，故渲染不能早于姓名解析。
func (c *Client) GetDocumentComments(ctx context.Context, fileToken string, fileType lark.FileType) (*CommentData, error) {
	// Phase 1：分页抓取 + 累积原始 item（暂不渲染）
	var raws []rawComment
	var pageToken *string
	for {
		resp, _, err := c.larkClient.Drive.GetDriveCommentList(ctx, &lark.GetDriveCommentListReq{
			FileToken:  fileToken,
			FileType:   fileType,
			PageToken:  pageToken,
			UserIDType: userIDTypePtr(), // 全局用户身份策略：统一 union_id
		}, c.methodOptions()...)
		if err != nil {
			return nil, fmt.Errorf("获取评论列表失败: %w", err)
		}

		for _, item := range resp.Items {
			raw := rawComment{item: item}
			// 如果还有更多回复，获取完整回复列表
			if item.HasMore {
				moreReplies, err := c.fetchMoreReplies(ctx, fileToken, fileType, item.CommentID, item.PageToken)
				if err != nil {
					log.Printf("警告: 获取评论 %s 的更多回复失败: %v", item.CommentID, err)
				} else {
					raw.moreReplies = moreReplies
				}
			}
			raws = append(raws, raw)
		}

		if !resp.HasMore {
			break
		}
		pageToken = &resp.PageToken
	}

	// Phase 2：收集全部 user id（作者 + person 元素）并一次性解析姓名
	userIDs := collectRawUserIDs(raws)
	userNames := c.ResolveUserNames(ctx, userIDs, userIDTypePtr())

	// Phase 3：用姓名 map 把原始 item 转为 Comment / Reply，渲染 person → @显示名
	data := &CommentData{UserNames: userNames}
	for _, raw := range raws {
		comment := convertComment(raw.item, userNames)
		for _, r := range raw.moreReplies {
			comment.Replies = append(comment.Replies, Reply{
				ID:         r.ReplyID,
				UserID:     r.UserID,
				CreateTime: time.Unix(r.CreateTime, 0),
				Content:    renderReplyContent(r.Content, userNames),
			})
		}
		if comment.IsWhole {
			data.GlobalComments = append(data.GlobalComments, comment)
		} else {
			data.LocalComments = append(data.LocalComments, comment)
		}
	}

	return data, nil
}

// collectRawUserIDs 收集原始评论中所有 user id：评论 / 回复作者 + 评论内 person 元素。
func collectRawUserIDs(raws []rawComment) []string {
	seen := make(map[string]bool)
	var userIDs []string

	addUserID := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			userIDs = append(userIDs, id)
		}
	}

	addPersons := func(elems []commentElement) {
		for _, e := range elems {
			if e.GetType() == "person" {
				addUserID(e.GetPersonUserID())
			}
		}
	}

	for _, raw := range raws {
		addUserID(raw.item.UserID)
		if raw.item.ReplyList != nil {
			for _, r := range raw.item.ReplyList.Replies {
				addUserID(r.UserID)
				addPersons(listReplyContentElements(r.Content))
			}
		}
		for _, r := range raw.moreReplies {
			addUserID(r.UserID)
			addPersons(replyContentElements(r.Content))
		}
	}

	return userIDs
}

// basicUserFetcher 拉取一批（≤basicBatchSize）用户基本信息，idType 须与 batch 中 id 类型一致。
// 生产实现为 Client.BasicBatchGetUsers，测试注入 fake。
type basicUserFetcher func(ctx context.Context, batch []string, idType lark.IDType) ([]BasicUser, error)

// basicBatchSize 是 contact/v3 basic_batch 单次请求的 user_ids 上限（接口硬限制）。
const basicBatchSize = 10

// ResolveUserNames 批量获取用户名，返回「输入 id → 姓名」映射。
// 底层走 contact/v3 basic_batch：该接口不校验应用通讯录授权范围，user/bot 身份都能
// 解析任意同租户用户；users/batch 会被应用可见范围静默过滤（code=0 但 items 缺人），
// 曾导致 @mention 与评论作者全部回退为 union_id 原文。
// userIDType 用于无法从前缀识别类型的 id（全局策略为 union_id，见 identity.go）。
func (c *Client) ResolveUserNames(ctx context.Context, userIDs []string, userIDType *lark.IDType) map[string]string {
	idType := lark.IDTypeUnionID
	if userIDType != nil {
		idType = *userIDType
	}
	return resolveUserNames(ctx, c.BasicBatchGetUsers, userIDs, idType)
}

// resolveUserNames 是 ResolveUserNames 的纯逻辑核心：按前缀分组 → 分批 → 合并。
// basic_batch 返回的 user_id 类型与请求的 user_id_type 一致（不像 users/batch 一次
// 返回三种 id），而评论 person 元素可能恒返 open_id，与全局 union_id 策略混批会查不到，
// 故按 on_/ou_ 前缀分别以 union_id/open_id 成批。单批失败仅告警并继续（部分成功）。
func resolveUserNames(ctx context.Context, fetch basicUserFetcher, userIDs []string, defaultIDType lark.IDType) map[string]string {
	result := make(map[string]string)
	if len(userIDs) == 0 {
		return result
	}

	groups := make(map[lark.IDType][]string)
	for _, id := range userIDs {
		t := idTypeForUserID(id, defaultIDType)
		groups[t] = append(groups[t], id)
	}

	for _, idType := range []lark.IDType{lark.IDTypeUnionID, lark.IDTypeOpenID, lark.IDTypeUserID} {
		ids := groups[idType]
		for i := 0; i < len(ids); i += basicBatchSize {
			batch := ids[i:min(i+basicBatchSize, len(ids))]
			users, err := fetch(ctx, batch, idType)
			if err != nil {
				log.Printf("警告: 批量获取用户信息失败: %v", err)
				continue
			}
			for _, u := range users {
				if u.UserID != "" && u.Name != "" {
					result[u.UserID] = u.Name
				}
			}
		}
	}

	if missing := len(userIDs) - len(result); missing > 0 {
		log.Printf("警告: %d 个用户 ID 中 %d 个未解析到姓名（可能已离职或 ID 无效），将以原始 ID 显示", len(userIDs), missing)
	}
	return result
}

// idTypeForUserID 按前缀识别 id 类型：on_ 为 union_id、ou_ 为 open_id，其余用 defaultIDType。
func idTypeForUserID(id string, defaultIDType lark.IDType) lark.IDType {
	switch {
	case strings.HasPrefix(id, "on_"):
		return lark.IDTypeUnionID
	case strings.HasPrefix(id, "ou_"):
		return lark.IDTypeOpenID
	default:
		return defaultIDType
	}
}

// fetchMoreReplies 获取评论的更多回复，返回原始 item（渲染推迟到 Phase 3）。
func (c *Client) fetchMoreReplies(ctx context.Context, fileToken string, fileType lark.FileType, commentID, pageToken string) ([]*lark.GetDriveCommentReplyListRespItem, error) {
	var replies []*lark.GetDriveCommentReplyListRespItem
	pt := &pageToken

	for {
		resp, _, err := c.larkClient.Drive.GetDriveCommentReplyList(ctx, &lark.GetDriveCommentReplyListReq{
			FileToken:  fileToken,
			CommentID:  commentID,
			FileType:   fileType,
			PageToken:  pt,
			UserIDType: userIDTypePtr(), // 全局用户身份策略：统一 union_id
		}, c.methodOptions()...)
		if err != nil {
			return nil, err
		}

		replies = append(replies, resp.Items...)

		if !resp.HasMore {
			break
		}
		pt = &resp.PageToken
	}

	return replies, nil
}

// convertComment 将 API 响应转换为 Comment 结构，渲染时用 userNames 解析 person 显示名。
func convertComment(item *lark.GetDriveCommentListRespItem, userNames map[string]string) Comment {
	comment := Comment{
		ID:         item.CommentID,
		UserID:     item.UserID,
		CreateTime: time.Unix(item.CreateTime, 0),
		IsSolved:   item.IsSolved,
		IsWhole:    item.IsWhole,
		Quote:      item.Quote,
	}

	if item.ReplyList != nil {
		for _, r := range item.ReplyList.Replies {
			comment.Replies = append(comment.Replies, Reply{
				ID:         r.ReplyID,
				UserID:     r.UserID,
				CreateTime: time.Unix(r.CreateTime, 0),
				Content:    renderReplyContentFromList(r.Content, userNames),
			})
		}
	}

	return comment
}

// commentElement 评论内容元素的统一接口
type commentElement interface {
	GetType() string
	GetTextRunText() string
	GetDocsLinkURL() string
	GetPersonUserID() string
}

// replyElement 适配 GetDriveCommentReplyListRespItemContent 的元素
type replyElement struct {
	e *lark.GetDriveCommentReplyListRespItemContentElement
}

func (r replyElement) GetType() string { return r.e.Type }
func (r replyElement) GetTextRunText() string {
	if r.e.TextRun != nil {
		return r.e.TextRun.Text
	}
	return ""
}
func (r replyElement) GetDocsLinkURL() string {
	if r.e.DocsLink != nil {
		return r.e.DocsLink.URL
	}
	return ""
}
func (r replyElement) GetPersonUserID() string {
	if r.e.Person != nil {
		return r.e.Person.UserID
	}
	return ""
}

// listReplyElement 适配 GetDriveCommentListRespItemReplyListReplyContent 的元素
type listReplyElement struct {
	e *lark.GetDriveCommentListRespItemReplyListReplyContentElement
}

func (r listReplyElement) GetType() string { return r.e.Type }
func (r listReplyElement) GetTextRunText() string {
	if r.e.TextRun != nil {
		return r.e.TextRun.Text
	}
	return ""
}
func (r listReplyElement) GetDocsLinkURL() string {
	if r.e.DocsLink != nil {
		return r.e.DocsLink.URL
	}
	return ""
}
func (r listReplyElement) GetPersonUserID() string {
	if r.e.Person != nil {
		return r.e.Person.UserID
	}
	return ""
}

// renderElements 渲染评论元素列表为文本，person 元素经 userNames 解析为 @显示名。
func renderElements(elems []commentElement, userNames map[string]string) string {
	var parts []string
	for _, elem := range elems {
		switch elem.GetType() {
		case "text_run":
			if text := elem.GetTextRunText(); text != "" {
				parts = append(parts, text)
			}
		case "docs_link":
			if url := elem.GetDocsLinkURL(); url != "" {
				parts = append(parts, fmt.Sprintf("[文档链接](%s)", url))
			}
		case "person":
			// 解析失败静默回退 @<原始 id>，与作者 / mention 解析失败一致
			if uid := elem.GetPersonUserID(); uid != "" {
				parts = append(parts, "@"+getUserDisplayName(userNames, uid))
			}
		}
	}
	return strings.Join(parts, "")
}

// replyContentElements 把回复内容（reply_list API）转为统一元素切片。
func replyContentElements(content *lark.GetDriveCommentReplyListRespItemContent) []commentElement {
	if content == nil {
		return nil
	}
	elems := make([]commentElement, len(content.Elements))
	for i, e := range content.Elements {
		elems[i] = replyElement{e}
	}
	return elems
}

// listReplyContentElements 把评论列表内联回复内容转为统一元素切片。
func listReplyContentElements(content *lark.GetDriveCommentListRespItemReplyListReplyContent) []commentElement {
	if content == nil {
		return nil
	}
	elems := make([]commentElement, len(content.Elements))
	for i, e := range content.Elements {
		elems[i] = listReplyElement{e}
	}
	return elems
}

// renderReplyContent 渲染回复内容元素为文本
func renderReplyContent(content *lark.GetDriveCommentReplyListRespItemContent, userNames map[string]string) string {
	return renderElements(replyContentElements(content), userNames)
}

// renderReplyContentFromList 渲染评论列表中的回复内容
func renderReplyContentFromList(content *lark.GetDriveCommentListRespItemReplyListReplyContent, userNames map[string]string) string {
	return renderElements(listReplyContentElements(content), userNames)
}

// RenderComments 将评论渲染为 Markdown 附录
func RenderComments(data *CommentData) string {
	if data == nil || (len(data.GlobalComments) == 0 && len(data.LocalComments) == 0) {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n---\n\n## 评论\n\n")

	if len(data.GlobalComments) > 0 {
		sb.WriteString("### 全文评论\n\n")
		for _, c := range data.GlobalComments {
			renderSingleComment(&sb, &c, data.UserNames)
		}
	}

	if len(data.LocalComments) > 0 {
		sb.WriteString("### 局部评论\n\n")
		for _, c := range data.LocalComments {
			renderSingleComment(&sb, &c, data.UserNames)
		}
	}

	return sb.String()
}

// getUserDisplayName 获取用户显示名称，若查询不到则降级显示 UserID
func getUserDisplayName(userNames map[string]string, userID string) string {
	if name, ok := userNames[userID]; ok && name != "" {
		return name
	}
	return userID
}

// renderSingleComment 渲染单条评论
func renderSingleComment(sb *strings.Builder, c *Comment, userNames map[string]string) {
	// 评论标题
	if c.IsWhole {
		sb.WriteString("**评论**")
	} else {
		// 局部评论显示引用的原文
		quote := c.Quote
		quoteRunes := []rune(quote)
		if len(quoteRunes) > 50 {
			quote = string(quoteRunes[:50]) + "..."
		}
		fmt.Fprintf(sb, "**关于:** `%s`", quote)
	}

	// 解决状态
	if c.IsSolved {
		sb.WriteString(" [已解决]")
	}

	// 创建时间
	fmt.Fprintf(sb, " (%s)\n\n", c.CreateTime.Format("2006-01-02 15:04:05"))

	// 回复列表
	for _, r := range c.Replies {
		displayName := getUserDisplayName(userNames, r.UserID)
		fmt.Fprintf(sb, "> **%s** (%s):\n", displayName, r.CreateTime.Format("2006-01-02 15:04:05"))
		// 处理多行内容，每行都加上引用前缀
		lines := strings.Split(r.Content, "\n")
		for _, line := range lines {
			fmt.Fprintf(sb, "> %s\n", line)
		}
		sb.WriteString(">\n")
	}
	sb.WriteString("\n")
}
