package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripSearchHighlight(t *testing.T) {
	assert.Equal(t, "产品设计规范", stripSearchHighlight("产品<h>设计</h>规范"))
	assert.Equal(t, "bold text", stripSearchHighlight("<b>bold</b> <hb>text</hb>"))
	assert.Equal(t, "no tags", stripSearchHighlight("no tags"))
	assert.Equal(t, "", stripSearchHighlight(""))
}

func TestBuildSearchRequest(t *testing.T) {
	t.Run("默认双空 filter 一次搜两边", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{pageSize: 20})
		require.NotNil(t, req.DocFilter)
		require.NotNil(t, req.WikiFilter)
		assert.Equal(t, "q", req.Query)
		assert.Equal(t, int64(20), *req.PageSize)
		assert.Nil(t, req.PageToken)
		// 空 filter 序列化为 {}
		docJSON, _ := json.Marshal(req.DocFilter)
		assert.Equal(t, "{}", string(docJSON))
	})

	t.Run("folder 只发 doc_filter", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{folder: "fld1, fld2", pageSize: 20})
		require.NotNil(t, req.DocFilter)
		assert.Nil(t, req.WikiFilter)
		assert.Equal(t, []string{"fld1", "fld2"}, req.DocFilter.FolderTokens)
	})

	t.Run("space 只发 wiki_filter", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{space: "sp1", pageSize: 20})
		assert.Nil(t, req.DocFilter)
		require.NotNil(t, req.WikiFilter)
		assert.Equal(t, []string{"sp1"}, req.WikiFilter.SpaceIDs)
	})

	t.Run("doc-types 大写化注入两侧", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{docTypes: "docx,Wiki", pageSize: 20})
		assert.Equal(t, []string{"DOCX", "WIKI"}, req.DocFilter.DocTypes)
		assert.Equal(t, []string{"DOCX", "WIKI"}, req.WikiFilter.DocTypes)
	})

	t.Run("sort 映射与 only-title", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{sort: "default", onlyTitle: true, pageSize: 20})
		assert.Equal(t, "DEFAULT_TYPE", *req.DocFilter.SortType)
		assert.True(t, *req.WikiFilter.OnlyTitle)

		req = buildSearchRequest("q", searchOptions{sort: "edit_time", pageSize: 20})
		assert.Equal(t, "EDIT_TIME", *req.WikiFilter.SortType)
		assert.Nil(t, req.DocFilter.OnlyTitle)
	})

	t.Run("page-token 传递", func(t *testing.T) {
		req := buildSearchRequest("q", searchOptions{pageToken: "tok", pageSize: 5})
		assert.Equal(t, "tok", *req.PageToken)
	})
}

func TestNewSearchResultPage(t *testing.T) {
	t.Run("字段映射", func(t *testing.T) {
		page := newSearchResultPage(&lark.SearchDocWikiResp{
			Total:     42,
			HasMore:   true,
			PageToken: "next",
			ResUnits: []*lark.SearchDocWikiRespResUnit{{
				TitleHighlighted:   "<h>设计</h>规范",
				SummaryHighlighted: "关于<h>设计</h>",
				EntityType:         "wiki",
				ResultMeta: &lark.SearchDocWikiRespResUnitResultMeta{
					DocTypes:   "DOCX",
					UpdateTime: 1751344200,
					URL:        "https://x.feishu.cn/wiki/tok1",
					OwnerName:  "张三",
					Token:      "tok1",
				},
			}},
		})
		assert.Equal(t, int64(42), page.Total)
		assert.True(t, page.HasMore)
		assert.Equal(t, "next", page.PageToken)
		require.Len(t, page.Results, 1)
		item := page.Results[0]
		assert.Equal(t, "设计规范", item.Title)
		assert.Equal(t, "关于设计", item.Summary)
		assert.Equal(t, "wiki", item.EntityType)
		assert.Equal(t, "docx", item.DocType)
		assert.Equal(t, "tok1", item.Token)
		assert.Equal(t, time.Unix(1751344200, 0).Format(time.RFC3339), item.UpdateTime)
	})

	t.Run("doc_type 空回退 entity_type 且 nil ResultMeta 不崩", func(t *testing.T) {
		page := newSearchResultPage(&lark.SearchDocWikiResp{
			ResUnits: []*lark.SearchDocWikiRespResUnit{
				// 真实 API 的 entity_type 为大写（如 WIKI），回退时应统一小写
				{TitleHighlighted: "t", EntityType: "WIKI"},
				nil,
			},
		})
		require.Len(t, page.Results, 1)
		assert.Equal(t, "wiki", page.Results[0].DocType)
		assert.Empty(t, page.Results[0].UpdateTime)
	})

	t.Run("空结果 Results 为空数组而非 null", func(t *testing.T) {
		page := newSearchResultPage(&lark.SearchDocWikiResp{})
		data, err := json.Marshal(page)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"results":[]`)
	})
}

func TestFormatSearchResultsText(t *testing.T) {
	t.Run("正常两行块与翻页提示", func(t *testing.T) {
		out := formatSearchResultsText(searchResultPage{
			Total:     42,
			HasMore:   true,
			PageToken: "tok_next",
			Results: []searchResultItem{{
				Title: "设计规范", DocType: "docx", OwnerName: "张三",
				URL: "https://x.feishu.cn/wiki/a", UpdateTime: "2026-07-01T12:30:00+08:00",
			}},
		})
		assert.Contains(t, out, "[docx] 设计规范 — 张三 · 2026-07-01 12:30")
		assert.Contains(t, out, "    https://x.feishu.cn/wiki/a")
		assert.Contains(t, out, "共 1 条（匹配总数 42）")
		assert.Contains(t, out, "--page-token 'tok_next'")
	})

	t.Run("URL 缺失回退 token", func(t *testing.T) {
		out := formatSearchResultsText(searchResultPage{
			Results: []searchResultItem{{Title: "t", DocType: "folder", Token: "fld1"}},
		})
		assert.Contains(t, out, "    token: fld1")
		assert.NotContains(t, out, "--page-token")
	})

	t.Run("空结果", func(t *testing.T) {
		assert.Equal(t, "未找到匹配结果。\n", formatSearchResultsText(searchResultPage{Results: []searchResultItem{}}))
	})
}

func TestValidateSearchArgs(t *testing.T) {
	valid := searchOptions{pageSize: 20}
	assert.NoError(t, validateSearchArgs([]string{"q"}, valid))
	assert.NoError(t, validateSearchArgs([]string{"q"}, searchOptions{docTypes: "Docx, WIKI", sort: "edit_time", pageSize: 1}))

	cases := []struct {
		name string
		args []string
		opts searchOptions
		msg  string
	}{
		{"缺 query", nil, valid, "Please specify the search query"},
		{"query 超长", []string{string(make([]rune, 51))}, valid, "at most 50 characters"},
		{"folder+space 互斥", []string{"q"}, searchOptions{folder: "f", space: "s", pageSize: 20}, "--folder cannot be used with --space"},
		{"page-size 越界", []string{"q"}, searchOptions{pageSize: 0}, "between 1 and 20"},
		{"doc-types 非法", []string{"q"}, searchOptions{docTypes: "bogus", pageSize: 20}, "unknown value"},
		{"sort 非法", []string{"q"}, searchOptions{sort: "bogus", pageSize: 20}, "--sort must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSearchArgs(tc.args, tc.opts)
			var ee *exitError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, 1, ee.code)
			assert.Contains(t, ee.msg, tc.msg)
		})
	}
}
