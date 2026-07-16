package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amzyang/larkdown/core"
	"github.com/chyroc/lark"
)

type searchOptions struct {
	docTypes  string
	folder    string
	space     string
	sort      string
	pageToken string
	onlyTitle bool
	asJSON    bool
	pageSize  int64
	limit     int64 // >0 时自动翻页聚合至多 limit 条（0 = 单页行为）
}

var searchOpts = searchOptions{}

// searchDocTypes 是 --doc-types 允许的取值（API 侧为对应大写）。
var searchDocTypes = []string{"doc", "sheet", "bitable", "mindnote", "file", "wiki", "docx", "folder", "catalog", "slides", "shortcut"}

// searchSortTypes 是 --sort 允许的取值；服务端枚举为对应大写，唯 default 对应 DEFAULT_TYPE。
var searchSortTypes = []string{"default", "edit_time", "edit_time_asc", "open_time", "create_time"}

func handleSearchCommand(ctx context.Context, query string) error {
	config, configPath, err := loadConfig()
	if err != nil {
		return err
	}
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}
	if !client.HasUserToken() {
		return exitWithMessage("search 仅支持用户身份（user_access_token），不支持 --as bot；请先执行 larkdown auth login", 1)
	}

	page, err := fetchSearchResults(ctx, client, query, searchOpts)
	if err != nil {
		return err
	}
	if searchOpts.asJSON {
		printJSON(os.Stdout, page)
		return nil
	}
	fmt.Print(formatSearchResultsText(page))
	return nil
}

// fetchSearchResults 执行搜索：--limit > 0 时按 --page-size 自动翻页聚合至多 limit 条，
// 否则保持单页行为。聚合页的 HasMore/PageToken 取自最后一页（供继续翻页）。
func fetchSearchResults(ctx context.Context, client *core.Client, query string, opts searchOptions) (searchResultPage, error) {
	resp, err := client.SearchDocWiki(ctx, buildSearchRequest(query, opts))
	if err != nil {
		return searchResultPage{}, fmt.Errorf("搜索失败: %w", err)
	}
	page := newSearchResultPage(resp)
	if opts.limit <= 0 {
		return page, nil
	}

	for int64(len(page.Results)) < opts.limit && page.HasMore && page.PageToken != "" {
		opts.pageToken = page.PageToken
		resp, err := client.SearchDocWiki(ctx, buildSearchRequest(query, opts))
		if err != nil {
			return searchResultPage{}, fmt.Errorf("搜索翻页失败（已获取 %d 条）: %w", len(page.Results), err)
		}
		next := newSearchResultPage(resp)
		page.Results = append(page.Results, next.Results...)
		page.HasMore, page.PageToken = next.HasMore, next.PageToken
	}
	if int64(len(page.Results)) > opts.limit {
		page.Results = page.Results[:opts.limit]
		page.HasMore = true // 截断意味着还有未展示结果，但截断处无 page_token 可续
		page.PageToken = ""
	}
	return page, nil
}

// validateSearchArgs 校验位置参数与 flag 组合；全部在触网前完成。
func validateSearchArgs(args []string, opts searchOptions) error {
	if len(args) == 0 {
		return exitWithMessage("请指定搜索关键词", 1)
	}
	if len(args) > 1 {
		return exitWithMessage("search 只接受一个关键词参数（多词查询请用引号包裹）", 1)
	}
	if utf8.RuneCountInString(args[0]) > 50 {
		return exitWithMessage("搜索关键词最多 50 个字符", 1)
	}
	if opts.folder != "" && opts.space != "" {
		return exitWithMessage("--folder 不能与 --space 同时使用", 1)
	}
	if opts.pageSize < 1 || opts.pageSize > 20 {
		return exitWithMessage("--page-size 必须在 1-20 之间", 1)
	}
	if opts.limit < 0 || opts.limit > 1000 {
		return exitWithMessage("--limit 必须在 1-1000 之间", 1)
	}
	for _, dt := range splitCSV(opts.docTypes) {
		if !containsFold(searchDocTypes, dt) {
			return exitWithMessage(fmt.Sprintf("--doc-types 含未知取值 %q（可选: %s）",
				dt, strings.Join(searchDocTypes, ", ")), 1)
		}
	}
	if opts.sort != "" && !containsFold(searchSortTypes, opts.sort) {
		return exitWithMessage(fmt.Sprintf("--sort 必须是以下之一: %s", strings.Join(searchSortTypes, ", ")), 1)
	}
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func containsFold(allowed []string, s string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, s) {
			return true
		}
	}
	return false
}

// buildSearchRequest 组装请求（假设已通过 validateSearchArgs）。
// 默认 doc_filter 与 wiki_filter 都发非 nil 空结构（序列化为 {}），一次同时搜云文档和 Wiki；
// --folder 只发 doc_filter，--space 只发 wiki_filter（服务端不支持二者同时限定）。
func buildSearchRequest(query string, opts searchOptions) *lark.SearchDocWikiReq {
	var docTypes []string
	for _, dt := range splitCSV(opts.docTypes) {
		docTypes = append(docTypes, strings.ToUpper(dt))
	}
	var sortType *string
	if opts.sort != "" {
		st := strings.ToUpper(opts.sort)
		if st == "DEFAULT" {
			st = "DEFAULT_TYPE"
		}
		sortType = &st
	}
	var onlyTitle *bool
	if opts.onlyTitle {
		onlyTitle = &opts.onlyTitle
	}

	req := &lark.SearchDocWikiReq{
		Query:    query,
		PageSize: &opts.pageSize,
	}
	if opts.pageToken != "" {
		req.PageToken = &opts.pageToken
	}
	if opts.space == "" {
		req.DocFilter = &lark.SearchDocWikiReqDocFilter{
			DocTypes:     docTypes,
			FolderTokens: splitCSV(opts.folder),
			OnlyTitle:    onlyTitle,
			SortType:     sortType,
		}
	}
	if opts.folder == "" {
		req.WikiFilter = &lark.SearchDocWikiReqWikiFilter{
			DocTypes:  docTypes,
			SpaceIDs:  splitCSV(opts.space),
			OnlyTitle: onlyTitle,
			SortType:  sortType,
		}
	}
	return req
}

var searchHighlightRe = regexp.MustCompile(`</?hb?>|</?b>`)

// stripSearchHighlight 剥离搜索结果中的 <h>/<hb>/<b> 高亮标签。
func stripSearchHighlight(s string) string {
	return searchHighlightRe.ReplaceAllString(s, "")
}

type searchResultItem struct {
	Title      string `json:"title"`
	Summary    string `json:"summary,omitempty"`
	EntityType string `json:"entity_type"`
	DocType    string `json:"doc_type,omitempty"`
	Token      string `json:"token"`
	URL        string `json:"url"`
	OwnerName  string `json:"owner_name,omitempty"`
	UpdateTime string `json:"update_time,omitempty"` // RFC3339（本地时区）
}

type searchResultPage struct {
	Total     int64              `json:"total"`
	HasMore   bool               `json:"has_more"`
	PageToken string             `json:"page_token,omitempty"` // 仅 has_more 时有值
	Results   []searchResultItem `json:"results"`
}

// newSearchResultPage 把 API 响应转成输出模型：剥高亮标签、doc_type 空时回退 entity_type、
// 时间戳转 RFC3339。
func newSearchResultPage(resp *lark.SearchDocWikiResp) searchResultPage {
	page := searchResultPage{
		Total:     resp.Total,
		HasMore:   resp.HasMore,
		PageToken: resp.PageToken,
		Results:   []searchResultItem{},
	}
	for _, unit := range resp.ResUnits {
		if unit == nil {
			continue
		}
		item := searchResultItem{
			Title:      stripSearchHighlight(unit.TitleHighlighted),
			Summary:    stripSearchHighlight(unit.SummaryHighlighted),
			EntityType: unit.EntityType,
		}
		if meta := unit.ResultMeta; meta != nil {
			item.DocType = strings.ToLower(meta.DocTypes)
			item.Token = meta.Token
			item.URL = meta.URL
			item.OwnerName = meta.OwnerName
			if meta.UpdateTime > 0 {
				item.UpdateTime = time.Unix(meta.UpdateTime, 0).Format(time.RFC3339)
			}
		}
		if item.DocType == "" {
			item.DocType = strings.ToLower(unit.EntityType)
		}
		page.Results = append(page.Results, item)
	}
	return page
}

// formatSearchResultsText 人类可读文本渲染。每条结果两行：标题行 + URL 行（URL 缺失时回退 token）。
func formatSearchResultsText(page searchResultPage) string {
	if len(page.Results) == 0 {
		return "未找到匹配结果。\n"
	}
	var b strings.Builder
	for _, item := range page.Results {
		fmt.Fprintf(&b, "[%s] %s", item.DocType, item.Title)
		if item.OwnerName != "" {
			fmt.Fprintf(&b, " — %s", item.OwnerName)
		}
		if len(item.UpdateTime) >= 16 {
			fmt.Fprintf(&b, " · %s", strings.Replace(item.UpdateTime[:16], "T", " ", 1))
		}
		b.WriteString("\n")
		switch {
		case item.URL != "":
			fmt.Fprintf(&b, "    %s\n", item.URL)
		case item.Token != "":
			fmt.Fprintf(&b, "    token: %s\n", item.Token)
		}
	}
	fmt.Fprintf(&b, "\n共 %d 条（匹配总数 %d）\n", len(page.Results), page.Total)
	if page.HasMore && page.PageToken != "" {
		fmt.Fprintf(&b, "还有更多结果，下一页请附加: --page-token '%s'\n", page.PageToken)
	}
	return b.String()
}
