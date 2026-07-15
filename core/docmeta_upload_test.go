package core

import (
	"testing"
)

func TestParseFrontMatter(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantFM      *FrontMatter
		wantBody    string
		wantErr     bool
		description string
	}{
		{
			name:    "新格式：完整 frontmatter",
			content: "# 正文标题\n\n这是正文内容。\n\n<!--\nsource: https://feishu.cn/wiki/abc123\n-->\n",
			wantFM: &FrontMatter{
				Source: "https://feishu.cn/wiki/abc123",
			},
			wantBody:    "# 正文标题\n\n这是正文内容。\n",
			wantErr:     false,
			description: "应正确解析末尾 HTML 注释格式的 frontmatter",
		},
		{
			name:    "新格式：最小 frontmatter",
			content: "正文内容\n\n<!--\nsource: https://example.com/wiki/test\n-->\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/test",
			},
			wantBody:    "正文内容\n",
			wantErr:     false,
			description: "应正确解析最小的 HTML 注释格式 frontmatter",
		},
		{
			name:        "无 frontmatter",
			content:     "# 标题\n\n正文内容\n",
			wantFM:      nil,
			wantBody:    "# 标题\n\n正文内容\n",
			wantErr:     false,
			description: "无 frontmatter 时应返回原内容",
		},
		{
			name:        "空内容",
			content:     "",
			wantFM:      nil,
			wantBody:    "",
			wantErr:     false,
			description: "空内容应返回空",
		},
		{
			name: "旧格式向后兼容",
			content: `---
source: https://feishu.cn/wiki/abc123
---
# 正文标题

这是正文内容。
`,
			wantFM: &FrontMatter{
				Source: "https://feishu.cn/wiki/abc123",
			},
			wantBody:    "# 正文标题\n\n这是正文内容。\n",
			wantErr:     false,
			description: "应正确解析旧格式 --- 的 frontmatter",
		},
		{
			name:        "旧格式只有开始标记",
			content:     "---\n内容没有结束标记",
			wantFM:      nil,
			wantBody:    "---\n内容没有结束标记",
			wantErr:     false,
			description: "只有开始标记时应返回原内容",
		},
		{
			name:    "HTML 注释非末尾仍视为 frontmatter",
			content: "# 标题\n\n<!--\nsource: https://example.com/wiki/test\n-->\n\n正文继续\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/test",
			},
			wantBody:    "# 标题\n\n正文继续\n",
			wantErr:     false,
			description: "frontmatter 之后被追加内容时仍应识别，注释块从正文剔除",
		},
		{
			name:    "frontmatter 后追加正文",
			content: "正文内容\n\n<!--\nsource: https://example.com/wiki/test\n-->\n新增的一段\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/test",
			},
			wantBody:    "正文内容\n\n新增的一段\n",
			wantErr:     false,
			description: "紧跟注释块追加的内容应并入正文",
		},
		{
			name:    "frontmatter 后追加正文且无末尾换行",
			content: "正文\n\n<!--\nsource: https://example.com/wiki/test\n-->\n追加",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/test",
			},
			wantBody:    "正文\n\n追加\n",
			wantErr:     false,
			description: "正文应归一化为单个换行收尾",
		},
		{
			name:    "frontmatter 后追加普通注释",
			content: "正文\n\n<!--\nsource: https://example.com/wiki/test\n-->\n\n<!--\nTODO\n-->\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/test",
			},
			wantBody:    "正文\n\n<!--\nTODO\n-->\n",
			wantErr:     false,
			description: "YAML 非法的注释块视为普通正文注释，不影响 source 块识别",
		},
		{
			name:        "代码围栏内的 source 注释不误判",
			content:     "# 说明\n\n```\n<!--\nsource: https://example.com/wiki/fake\n-->\n```\n",
			wantFM:      nil,
			wantBody:    "# 说明\n\n```\n<!--\nsource: https://example.com/wiki/fake\n-->\n```\n",
			wantErr:     false,
			description: "围栏内引用的 frontmatter 示例不应被解析",
		},
		{
			name:    "围栏内示例与末尾真 frontmatter 共存",
			content: "# 说明\n\n```\n<!--\nsource: https://example.com/wiki/fake\n-->\n```\n\n<!--\nsource: https://example.com/wiki/real\n-->\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/real",
			},
			wantBody:    "# 说明\n\n```\n<!--\nsource: https://example.com/wiki/fake\n-->\n```\n",
			wantErr:     false,
			description: "应取围栏外的真 frontmatter，围栏内示例原样保留",
		},
		{
			name:    "多个围栏外 source 块取最后一个",
			content: "开头\n\n<!--\nsource: https://example.com/wiki/old\n-->\n\n中间\n\n<!--\nsource: https://example.com/wiki/new\n-->\n",
			wantFM: &FrontMatter{
				Source: "https://example.com/wiki/new",
			},
			wantBody:    "开头\n\n<!--\nsource: https://example.com/wiki/old\n-->\n\n中间\n",
			wantErr:     false,
			description: "多个候选块应取最靠后的，靠前的保留在正文",
		},
		{
			name:        "HTML 注释无 source 不视为 frontmatter",
			content:     "正文\n\n<!--\nsome_key: value\n-->\n",
			wantFM:      nil,
			wantBody:    "正文\n\n<!--\nsome_key: value\n-->\n",
			wantErr:     false,
			description: "HTML 注释中无 source 字段不应被视为 frontmatter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := ParseFrontMatter(tt.content)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFrontMatter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantFM == nil {
				if fm != nil {
					t.Errorf("ParseFrontMatter() fm = %v, want nil", fm)
				}
			} else {
				if fm == nil {
					t.Errorf("ParseFrontMatter() fm = nil, want %v", tt.wantFM)
					return
				}
				if fm.Source != tt.wantFM.Source {
					t.Errorf("Source = %q, want %q", fm.Source, tt.wantFM.Source)
				}
			}

			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "标准一级标题",
			body: "# 这是标题\n\n正文内容",
			want: "这是标题",
		},
		{
			name: "标题前有空行",
			body: "\n\n# 这是标题\n\n正文内容",
			want: "这是标题",
		},
		{
			name: "无 H1 但有 H2，取 H2",
			body: "## 二级标题\n\n正文内容",
			want: "二级标题",
		},
		{
			name: "无 H1 但有 H3，取 H3",
			body: "### 三级标题\n\n正文内容",
			want: "三级标题",
		},
		{
			name: "有 H1 时不取 H2",
			body: "## 先出现的二级\n\n# 一级标题\n\n正文",
			want: "一级标题",
		},
		{
			name: "无标题",
			body: "这是一段普通文本\n\n没有任何标题",
			want: "",
		},
		{
			name: "标题有多余空格",
			body: "#   带空格的标题  \n\n正文",
			want: "带空格的标题",
		},
		{
			name: "空内容",
			body: "",
			want: "",
		},
		{
			name: "多个一级标题取第一个",
			body: "# 第一个标题\n\n# 第二个标题",
			want: "第一个标题",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTitle(tt.body)
			if got != tt.want {
				t.Errorf("ExtractTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoveFirstHeading(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "移除一级标题",
			body: "# 标题\n\n正文内容",
			want: "正文内容",
		},
		{
			name: "保留二级标题",
			body: "## 二级标题\n\n正文内容",
			want: "## 二级标题\n\n正文内容",
		},
		{
			name: "无标题时不变",
			body: "正文内容\n\n更多内容",
			want: "正文内容\n\n更多内容",
		},
		{
			name: "只移除第一个一级标题",
			body: "# 第一个\n\n# 第二个\n\n正文",
			want: "# 第二个\n\n正文",
		},
		{
			name: "空内容",
			body: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveFirstHeading(tt.body)
			if got != tt.want {
				t.Errorf("RemoveFirstHeading() = %q, want %q", got, tt.want)
			}
		})
	}
}
