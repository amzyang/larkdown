package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildHeadingTree(t *testing.T) {
	tests := []struct {
		name     string
		flat     []Heading
		expected []Heading
	}{
		{
			name:     "空列表",
			flat:     nil,
			expected: nil,
		},
		{
			name: "单层标题",
			flat: []Heading{
				{Level: 2, Text: "标题一"},
				{Level: 2, Text: "标题二"},
			},
			expected: []Heading{
				{Level: 2, Text: "标题一"},
				{Level: 2, Text: "标题二"},
			},
		},
		{
			name: "嵌套标题",
			flat: []Heading{
				{Level: 2, Text: "一级"},
				{Level: 3, Text: "二级-1"},
				{Level: 3, Text: "二级-2"},
				{Level: 2, Text: "另一个一级"},
			},
			expected: []Heading{
				{
					Level: 2, Text: "一级",
					Children: []Heading{
						{Level: 3, Text: "二级-1"},
						{Level: 3, Text: "二级-2"},
					},
				},
				{Level: 2, Text: "另一个一级"},
			},
		},
		{
			name: "深层嵌套",
			flat: []Heading{
				{Level: 1, Text: "H1"},
				{Level: 2, Text: "H2"},
				{Level: 3, Text: "H3"},
				{Level: 4, Text: "H4"},
			},
			expected: []Heading{
				{
					Level: 1, Text: "H1",
					Children: []Heading{
						{
							Level: 2, Text: "H2",
							Children: []Heading{
								{
									Level: 3, Text: "H3",
									Children: []Heading{
										{Level: 4, Text: "H4"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildHeadingTree(tt.flat)
			if !headingsEqual(result, tt.expected) {
				t.Errorf("BuildHeadingTree() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func headingsEqual(a, b []Heading) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Level != b[i].Level || a[i].Text != b[i].Text {
			return false
		}
		if !headingsEqual(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

func TestGenerateLlmsTxt(t *testing.T) {
	idx := NewDocsIndex("TestWiki", "/tmp/test")
	idx.AddDoc(DocMeta{
		Title:   "文档一",
		RelPath: "doc1.md",
	}, "/tmp/test")
	// 注意：RelPath 在 AddDoc 内部会根据 folderPath 计算相对路径
	idx.AddDoc(DocMeta{
		Title:   "子目录文档",
		RelPath: "doc2.md", // 只传文件名，AddDoc 会处理相对路径
	}, "/tmp/test/subdir")

	result := GenerateLlmsTxt(idx)

	// 检查基本结构
	if !strings.Contains(result, "# TestWiki Docs") {
		t.Errorf("llms.txt 缺少根标题")
	}
	if !strings.Contains(result, "## Docs") {
		t.Errorf("llms.txt 缺少 Docs 节")
	}
	if !strings.Contains(result, "[文档一](doc1.md)") {
		t.Errorf("llms.txt 缺少文档一链接")
	}
	if !strings.Contains(result, "[子目录文档](subdir/doc2.md)") {
		t.Errorf("llms.txt 缺少子目录文档链接")
	}
}

func TestGenerateDocsMap(t *testing.T) {
	idx := NewDocsIndex("TestWiki", "/tmp/test")
	idx.AddDoc(DocMeta{
		Title:   "文档一",
		RelPath: "doc1.md",
		Headings: []Heading{
			{Level: 2, Text: "概述"},
			{Level: 2, Text: "详情"},
		},
	}, "/tmp/test")
	idx.AddDoc(DocMeta{
		Title:    "无标题文档",
		RelPath:  "doc2.md",
		Headings: nil,
	}, "/tmp/test")

	result := GenerateDocsMap(idx)

	// 检查基本结构
	if !strings.Contains(result, "# TestWiki Documentation Map") {
		t.Errorf("docs_map.md 缺少根标题")
	}
	if !strings.Contains(result, "Auto-generated at:") {
		t.Errorf("docs_map.md 缺少生成时间")
	}
	if !strings.Contains(result, "### [文档一](doc1.md)") {
		t.Errorf("docs_map.md 缺少文档一标题")
	}
	if !strings.Contains(result, "* 概述") {
		t.Errorf("docs_map.md 缺少标题内容")
	}
	if !strings.Contains(result, "(No headings found)") {
		t.Errorf("docs_map.md 无标题文档处理错误")
	}
}

func TestEscapeMarkdownLink(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"普通文本", "普通文本"},
		{"带[括号]的文本", "带\\[括号\\]的文本"},
		{"[多个][括号]", "\\[多个\\]\\[括号\\]"},
	}

	for _, tt := range tests {
		result := escapeMarkdownLink(tt.input)
		if result != tt.expected {
			t.Errorf("escapeMarkdownLink(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestComputeMdFilename(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		urlToken string
		config   OutputConfig
		expected string
	}{
		{
			name:     "仅使用 token",
			title:    "测试文档",
			urlToken: "abc123",
			config:   OutputConfig{TitleAsFilename: false},
			expected: "abc123.md",
		},
		{
			name:     "使用标题",
			title:    "测试文档",
			urlToken: "abc123",
			config:   OutputConfig{TitleAsFilename: true},
			expected: "测试文档.md",
		},
		{
			name:     "特殊字符清理",
			title:    "文档/路径:名称",
			urlToken: "abc123",
			config:   OutputConfig{TitleAsFilename: true},
			expected: "文档_路径_名称.md",
		},
		{
			name:     "空标题回退到 token",
			title:    "",
			urlToken: "abc123",
			config:   OutputConfig{TitleAsFilename: true},
			expected: "abc123.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeMdFilename(tt.title, tt.urlToken, tt.config)
			if result != tt.expected {
				t.Errorf("ComputeMdFilename() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGenerateFrontMatter(t *testing.T) {
	t.Run("空 Source 返回空字符串", func(t *testing.T) {
		fm := FrontMatter{}
		result := GenerateFrontMatter(fm)
		if result != "" {
			t.Errorf("空 Source 应返回空字符串，实际: %q", result)
		}
	})

	t.Run("正常 frontmatter", func(t *testing.T) {
		fm := FrontMatter{
			Source: "https://feishu.cn/docx/abc123",
		}
		result := GenerateFrontMatter(fm)
		if !strings.HasPrefix(result, "<!--\n") {
			t.Errorf("应以 <!--\\n 开头，实际: %q", result)
		}
		if !strings.HasSuffix(result, "-->\n") {
			t.Errorf("应以 -->\\n 结尾，实际: %q", result)
		}
		if !strings.Contains(result, "source: https://feishu.cn/docx/abc123\n") {
			t.Errorf("应包含 source 字段，实际: %s", result)
		}
		if strings.Contains(result, "version:") {
			t.Errorf("空 Version 不应输出 version 字段（向后兼容），实际: %s", result)
		}
	})

	t.Run("带 version 的 frontmatter 可 round-trip", func(t *testing.T) {
		fm := FrontMatter{
			Source:  "https://feishu.cn/wiki/abc123",
			Version: "1700000000.42",
		}
		result := GenerateFrontMatter(fm)
		// yaml 对类数字字符串会加引号，只断言字段存在
		if !strings.Contains(result, "version: \"1700000000.42\"\n") {
			t.Errorf("应包含 version 字段，实际: %s", result)
		}

		content := "# 标题\n\n正文\n\n" + result
		parsed, body, err := ParseFrontMatter(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed == nil || parsed.Source != fm.Source || parsed.Version != fm.Version {
			t.Errorf("round-trip 失败: %+v", parsed)
		}
		if strings.Contains(body, "version:") {
			t.Errorf("正文不应包含 frontmatter 内容: %q", body)
		}
	})
}

func TestDownloadVersion(t *testing.T) {
	if got := DownloadVersion("", 42); got != "42" {
		t.Errorf("非 Wiki 文档应仅用 revision_id，实际: %q", got)
	}
	if got := DownloadVersion("1700000000", 42); got != "1700000000.42" {
		t.Errorf("Wiki 文档应拼接 obj_edit_time 与 revision_id，实际: %q", got)
	}
}

func TestExtractHeadingsFromMarkdown(t *testing.T) {
	body := "## 二级\n\n```go\n# 注释不是标题\n```\n\n### 三级 **加粗**\n\n####### 七个井号不是标题\n\n#无空格不是标题\n"
	got := ExtractHeadingsFromMarkdown(body)
	if len(got) != 2 {
		t.Fatalf("预期 2 个标题，实际 %d: %+v", len(got), got)
	}
	if got[0].Level != 2 || got[0].Text != "二级" {
		t.Errorf("标题[0] 错误: %+v", got[0])
	}
	if got[1].Level != 3 || got[1].Text != "三级 **加粗**" {
		t.Errorf("标题[1] 错误: %+v", got[1])
	}
}

func TestLocalDocMeta(t *testing.T) {
	body := "# 文档标题\n\n## 章节一\n\n内容\n\n---\n\n## 评论\n\n> 某人: 评论内容\n"
	meta := LocalDocMeta("/tmp/out/文档标题.md", body)
	if meta.Title != "文档标题" {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.RelPath != "文档标题.md" {
		t.Errorf("RelPath = %q", meta.RelPath)
	}
	// 标题行与评论附录都不应进入标题树
	if len(meta.Headings) != 1 || meta.Headings[0].Text != "章节一" {
		t.Errorf("Headings = %+v", meta.Headings)
	}
}

func TestFindLocalFileByToken(t *testing.T) {
	writeDoc := func(t *testing.T, dir, name, token, version string) string {
		t.Helper()
		fm := FrontMatter{Source: "https://feishu.cn/wiki/" + token, Version: version}
		content := "# 标题\n\n正文\n\n" + GenerateFrontMatter(fm)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("快路径: token 命名", func(t *testing.T) {
		dir := t.TempDir()
		want := writeDoc(t, dir, "Token123.md", "Token123", "42")
		path, fm, body := FindLocalFileByToken(dir, "Token123", "", OutputConfig{})
		if path != want || fm == nil || fm.Version != "42" || !strings.Contains(body, "正文") {
			t.Errorf("path=%q fm=%+v", path, fm)
		}
	})

	t.Run("快路径: 标题命名", func(t *testing.T) {
		dir := t.TempDir()
		want := writeDoc(t, dir, "我的文档.md", "Token123", "42")
		path, fm, _ := FindLocalFileByToken(dir, "Token123", "我的文档", OutputConfig{TitleAsFilename: true})
		if path != want || fm == nil {
			t.Errorf("path=%q fm=%+v", path, fm)
		}
	})

	t.Run("慢路径: 文件名与推算不符时扫描目录", func(t *testing.T) {
		dir := t.TempDir()
		want := writeDoc(t, dir, "改过名的文件.md", "Token123", "42")
		writeDoc(t, dir, "其它文档.md", "OtherToken", "1")
		path, fm, _ := FindLocalFileByToken(dir, "Token123", "", OutputConfig{TitleAsFilename: true})
		if path != want || fm == nil {
			t.Errorf("path=%q fm=%+v", path, fm)
		}
	})

	t.Run("未找到", func(t *testing.T) {
		dir := t.TempDir()
		path, fm, body := FindLocalFileByToken(dir, "Nope", "", OutputConfig{})
		if path != "" || fm != nil || body != "" {
			t.Errorf("应返回零值: path=%q fm=%+v", path, fm)
		}
	})
}

func TestDocsIndexAddDoc(t *testing.T) {
	idx := NewDocsIndex("TestRoot", "/output")

	// 添加根目录文档
	idx.AddDoc(DocMeta{Title: "根文档", RelPath: "root.md"}, "/output")

	// 添加子目录文档
	idx.AddDoc(DocMeta{Title: "子文档", RelPath: "sub.md"}, "/output/subdir")

	// 检查扁平列表
	if len(idx.Docs) != 2 {
		t.Errorf("预期 2 个文档，实际 %d", len(idx.Docs))
	}

	// 检查第二个文档的相对路径
	if idx.Docs[1].RelPath != "subdir/sub.md" {
		t.Errorf("相对路径计算错误: %s", idx.Docs[1].RelPath)
	}

	// 检查目录树
	if len(idx.Tree.Children) != 1 {
		t.Errorf("预期 1 个子目录，实际 %d", len(idx.Tree.Children))
	}
}

func TestExtractTokenFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://feishu.cn/wiki/NodeToken123", "NodeToken123"},
		{"https://feishu.cn/docx/DocToken456", "DocToken456"},
		{"https://feishu.cn/docs/OldToken789", "OldToken789"},
		{"https://feishu.cn/drive/folder/abc", ""},
		{"not a url", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := ExtractTokenFromURL(tt.url)
		if result != tt.expected {
			t.Errorf("ExtractTokenFromURL(%q) = %q, want %q", tt.url, result, tt.expected)
		}
	}
}

func TestFindStaleFile(t *testing.T) {
	t.Run("frontmatter source URL 匹配", func(t *testing.T) {
		dir := t.TempDir()
		content := "# 旧文档\n\n内容\n\n<!--\nsource: https://feishu.cn/wiki/NodeToken789\n-->\n"
		oldFile := filepath.Join(dir, "旧标题.md")
		os.WriteFile(oldFile, []byte(content), 0o644)

		stale, err := FindStaleFile(dir, "新标题.md", "NodeToken789")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stale != oldFile {
			t.Errorf("expected %q, got %q", oldFile, stale)
		}
	})

	t.Run("同名文件不误报", func(t *testing.T) {
		dir := t.TempDir()
		sameFile := filepath.Join(dir, "标题.md")
		os.WriteFile(sameFile, []byte("content"), 0o644)

		stale, err := FindStaleFile(dir, "标题.md", "Token123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stale != "" {
			t.Errorf("expected empty, got %q", stale)
		}
	})

	t.Run("无旧文件返回空", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "其他文档.md"), []byte("content"), 0o644)

		stale, err := FindStaleFile(dir, "新标题.md", "Token123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stale != "" {
			t.Errorf("expected empty, got %q", stale)
		}
	})
}
