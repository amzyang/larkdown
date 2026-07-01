package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amzyang/larkdown/utils"
	"gopkg.in/yaml.v3"
)

// Heading 标题信息
type Heading struct {
	Level    int       // 1-9
	Text     string    // 标题文本
	Children []Heading // 子标题
}

// DocMeta 单个文档的元信息
type DocMeta struct {
	Title    string    // 文档标题
	RelPath  string    // 相对路径 (如 "子目录/文档.md")
	Headings []Heading // 标题层级结构
}

// FolderNode 目录树节点
type FolderNode struct {
	Name     string
	RelPath  string
	Docs     []DocMeta     // 该目录下的文档
	Children []*FolderNode // 子目录
}

// DocsIndex 整个下载任务的索引
type DocsIndex struct {
	RootName    string      // 根目录/Wiki 名称
	OutputDir   string      // 输出目录
	Docs        []DocMeta   // 扁平文档列表（用于 llms.txt）
	Tree        *FolderNode // 目录树结构（用于 docs_map.md）
	GeneratedAt time.Time
	mu          sync.Mutex
}

// NewDocsIndex 创建新的索引
func NewDocsIndex(rootName, outputDir string) *DocsIndex {
	return &DocsIndex{
		RootName:    rootName,
		OutputDir:   outputDir,
		Docs:        make([]DocMeta, 0),
		Tree:        &FolderNode{Name: rootName, RelPath: ""},
		GeneratedAt: time.Now(),
	}
}

// AddDoc 添加文档到索引（线程安全）
func (idx *DocsIndex) AddDoc(meta DocMeta, folderPath string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// 计算相对于 outputDir 的路径
	relFolderPath, _ := filepath.Rel(idx.OutputDir, folderPath)
	if relFolderPath == "." {
		relFolderPath = ""
	}

	// 更新 meta.RelPath 为完整相对路径
	if relFolderPath != "" {
		meta.RelPath = filepath.Join(relFolderPath, meta.RelPath)
	}

	// 添加到扁平列表
	idx.Docs = append(idx.Docs, meta)

	// 添加到目录树
	idx.addToTree(meta, relFolderPath)
}

// addToTree 将文档添加到目录树
func (idx *DocsIndex) addToTree(meta DocMeta, relFolderPath string) {
	node := idx.Tree

	if relFolderPath != "" {
		parts := strings.Split(relFolderPath, string(filepath.Separator))
		for _, part := range parts {
			found := false
			for _, child := range node.Children {
				if child.Name == part {
					node = child
					found = true
					break
				}
			}
			if !found {
				newNode := &FolderNode{
					Name:    part,
					RelPath: filepath.Join(node.RelPath, part),
				}
				node.Children = append(node.Children, newNode)
				node = newNode
			}
		}
	}

	node.Docs = append(node.Docs, meta)
}

// ComputeMdFilename 计算 Markdown 文件名
func ComputeMdFilename(title, urlToken string, config OutputConfig) string {
	if config.TitleAsFilename {
		name := utils.SanitizeFileName(title)
		if name == "" {
			name = urlToken
		}
		return fmt.Sprintf("%s.md", name)
	}
	return fmt.Sprintf("%s.md", urlToken)
}

// BuildHeadingTree 将扁平标题列表构建为树形结构
func BuildHeadingTree(flat []Heading) []Heading {
	if len(flat) == 0 {
		return nil
	}

	var result []Heading
	var stack []*Heading

	for _, h := range flat {
		newHeading := Heading{
			Level: h.Level,
			Text:  h.Text,
		}

		// 找到正确的父节点
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			result = append(result, newHeading)
			stack = append(stack, &result[len(result)-1])
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, newHeading)
			stack = append(stack, &parent.Children[len(parent.Children)-1])
		}
	}

	return result
}

// GenerateLlmsTxt 生成 llms.txt 内容
func GenerateLlmsTxt(idx *DocsIndex) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# %s Docs\n\n## Docs\n", idx.RootName))

	for _, doc := range idx.Docs {
		// 转义标题中的特殊字符
		title := escapeMarkdownLink(doc.Title)
		buf.WriteString(fmt.Sprintf("- [%s](%s)\n", title, doc.RelPath))
	}
	return buf.String()
}

// GenerateDocsMap 生成 docs_map.md 内容
func GenerateDocsMap(idx *DocsIndex) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# %s Documentation Map\n\n", idx.RootName))
	buf.WriteString(fmt.Sprintf("> Auto-generated at: %s\n\n",
		idx.GeneratedAt.Format(time.RFC3339)))

	writeFolder(&buf, idx.Tree, 0)
	return buf.String()
}

func writeFolder(buf *strings.Builder, node *FolderNode, depth int) {
	// 写入文件夹标题（深度 > 0 时）
	if node.Name != "" && depth > 0 {
		buf.WriteString(fmt.Sprintf("## %s\n\n", node.Name))
	}

	// 写入该目录下的文档
	for _, doc := range node.Docs {
		title := escapeMarkdownLink(doc.Title)
		buf.WriteString(fmt.Sprintf("### [%s](%s)\n", title, doc.RelPath))
		if len(doc.Headings) == 0 {
			buf.WriteString("* (No headings found)\n")
		} else {
			writeHeadings(buf, doc.Headings, 0)
		}
		buf.WriteString("\n")
	}

	// 递归处理子目录
	for _, child := range node.Children {
		writeFolder(buf, child, depth+1)
	}
}

func writeHeadings(buf *strings.Builder, headings []Heading, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, h := range headings {
		buf.WriteString(fmt.Sprintf("%s* %s\n", prefix, h.Text))
		if len(h.Children) > 0 {
			writeHeadings(buf, h.Children, indent+1)
		}
	}
}

// escapeMarkdownLink 转义 Markdown 链接中的特殊字符
func escapeMarkdownLink(text string) string {
	text = strings.ReplaceAll(text, "[", "\\[")
	text = strings.ReplaceAll(text, "]", "\\]")
	return text
}

// WriteDocsIndex 写入索引文件到磁盘
func WriteDocsIndex(idx *DocsIndex, outputDir string) error {
	// 确保输出目录存在
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}

	// 写入 llms.txt
	llmsTxt := GenerateLlmsTxt(idx)
	llmsPath := filepath.Join(outputDir, "llms.txt")
	if err := os.WriteFile(llmsPath, []byte(llmsTxt), 0o644); err != nil {
		return fmt.Errorf("写入 llms.txt 失败: %w", err)
	}
	fmt.Printf("Generated %s\n", llmsPath)

	// 写入 docs_map.md
	docsMap := GenerateDocsMap(idx)
	docsMapPath := filepath.Join(outputDir, "docs_map.md")
	if err := os.WriteFile(docsMapPath, []byte(docsMap), 0o644); err != nil {
		return fmt.Errorf("写入 docs_map.md 失败: %w", err)
	}
	fmt.Printf("Generated %s\n", docsMapPath)

	return nil
}

// FrontMatter 文档的 YAML frontmatter 元数据
type FrontMatter struct {
	Source string `yaml:"source"`
	// Version 下载时的远程版本标记（见 DownloadVersion），用于重复下载时跳过未变化文档。
	// 上传写回时原样保留：上传会推高远程版本，旧标记自然失配、触发下次重新下载。
	Version string `yaml:"version,omitempty"`
}

// DownloadVersion 组合文档下载版本标记。revision_id 随内容编辑与评论变化，
// 但白板编辑不更新它；Wiki 节点的 obj_edit_time 随白板编辑变化，故两者拼接，
// 任一变化都触发重新下载。非 Wiki 文档无 obj_edit_time，仅用 revision_id
// （已知限制：纯白板编辑不会被感知，可用 download --force 强制刷新）。
func DownloadVersion(objEditTime string, revisionID int64) string {
	rev := strconv.FormatInt(revisionID, 10)
	if objEditTime == "" {
		return rev
	}
	return objEditTime + "." + rev
}

// GenerateFrontMatter 生成 HTML 注释格式的 frontmatter（放在文件末尾）
func GenerateFrontMatter(fm FrontMatter) string {
	if fm.Source == "" {
		return ""
	}
	data, err := yaml.Marshal(&fm)
	if err != nil {
		return ""
	}
	return "<!--\n" + string(data) + "-->\n"
}

// ParseFrontMatter 解析 md 文件的 frontmatter
// 返回: frontmatter 数据、正文内容（不含 frontmatter）、错误
// 优先匹配新格式（文件末尾 HTML 注释），向后兼容旧格式（文件顶部 ---）
func ParseFrontMatter(content string) (*FrontMatter, string, error) {
	if fm, body, err := parseCommentFrontMatter(content); fm != nil || err != nil {
		return fm, body, err
	}
	return parseLegacyFrontMatter(content)
}

// parseCommentFrontMatter 解析文件末尾 <!-- --> 格式的 frontmatter
func parseCommentFrontMatter(content string) (*FrontMatter, string, error) {
	lastOpen := strings.LastIndex(content, "<!--\n")
	if lastOpen == -1 {
		return nil, content, nil
	}
	closeIdx := strings.Index(content[lastOpen:], "\n-->")
	if closeIdx == -1 {
		return nil, content, nil
	}
	closeIdx += lastOpen
	// 注释块后面必须没有非空内容（确保是文件末尾）
	after := content[closeIdx+4:] // skip "\n-->"
	if strings.TrimSpace(after) != "" {
		return nil, content, nil
	}

	yamlContent := content[lastOpen+5 : closeIdx]
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
		return nil, content, fmt.Errorf("解析 frontmatter 失败: %w", err)
	}
	if fm.Source == "" {
		return nil, content, nil
	}

	body := strings.TrimRight(content[:lastOpen], "\n") + "\n"
	return &fm, body, nil
}

// parseLegacyFrontMatter 解析旧格式（文件顶部 ---）的 frontmatter，向后兼容
func parseLegacyFrontMatter(content string) (*FrontMatter, string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, content, nil
	}

	endIndex := strings.Index(content[4:], "\n---")
	if endIndex == -1 {
		return nil, content, nil
	}

	yamlContent := content[4 : 4+endIndex]
	body := content[4+endIndex+4:]
	body = strings.TrimLeft(body, "\n")

	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
		return nil, content, fmt.Errorf("解析 frontmatter 失败: %w", err)
	}

	return &fm, body, nil
}

// ExtractTitle 从 markdown 正文提取标题
// fallback 链：ATX H1 → 首个任意级 ATX 标题 → 空字符串
func ExtractTitle(body string) string {
	lines := strings.Split(body, "\n")
	// 第一轮：找 ATX H1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(line[2:])
		}
	}
	// 第二轮：找任意级别 ATX 标题
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			i := 0
			for i < len(line) && line[i] == '#' {
				i++
			}
			if i < len(line) && line[i] == ' ' {
				title := strings.TrimSpace(line[i+1:])
				if title != "" {
					return title
				}
			}
		}
	}
	return ""
}

// FindStaleFile 在 outputDir 中查找与 urlToken 关联但文件名不同的旧 md 文件。
func FindStaleFile(outputDir, newFilename, urlToken string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(outputDir, "*.md"))
	if err != nil {
		return "", err
	}
	for _, path := range matches {
		base := filepath.Base(path)
		if base == newFilename {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fm, _, err := ParseFrontMatter(string(content))
		if err != nil || fm == nil {
			continue
		}
		if ExtractTokenFromURL(fm.Source) == urlToken {
			return path, nil
		}
	}
	return "", nil
}

// FindLocalFileByToken 在 outputDir 中查找 frontmatter source token 与 urlToken 匹配的 md 文件，
// 返回 (文件路径, frontmatter, 正文)；未找到返回零值。
// 优先直接探测候选文件名（token 命名 / 已知标题命名），避免大目录下逐一扫描；
// 候选未命中时回退全目录扫描（覆盖 TitleAsFilename 下标题未知的情况）。
func FindLocalFileByToken(outputDir, urlToken, knownTitle string, config OutputConfig) (string, *FrontMatter, string) {
	tryPath := func(path string) (*FrontMatter, string) {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, ""
		}
		fm, body, err := ParseFrontMatter(string(content))
		if err != nil || fm == nil || ExtractTokenFromURL(fm.Source) != urlToken {
			return nil, ""
		}
		return fm, body
	}

	// 快路径：按当前配置推算的文件名 + token 文件名
	candidates := []string{ComputeMdFilename(knownTitle, urlToken, config), urlToken + ".md"}
	for i, name := range candidates {
		if i == 1 && name == candidates[0] {
			continue
		}
		path := filepath.Join(outputDir, name)
		if fm, body := tryPath(path); fm != nil {
			return path, fm, body
		}
	}

	// 慢路径：全目录扫描（如配置变更导致文件名与推算不符）
	matches, err := filepath.Glob(filepath.Join(outputDir, "*.md"))
	if err != nil {
		return "", nil, ""
	}
	for _, path := range matches {
		if fm, body := tryPath(path); fm != nil {
			return path, fm, body
		}
	}
	return "", nil, ""
}

// ExtractHeadingsFromMarkdown 从 markdown 正文提取扁平 ATX 标题列表（跳过代码围栏内的 #）。
// 标题文本保留行内 markdown 格式（与 parser 的纯文本口径略有差异，仅用于索引展示）。
func ExtractHeadingsFromMarkdown(body string) []Heading {
	var headings []Heading
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		if text := strings.TrimSpace(trimmed[level+1:]); text != "" {
			headings = append(headings, Heading{Level: level, Text: text})
		}
	}
	return headings
}

// commentsAppendixMarker 下载时 RenderComments 追加的评论附录分隔符。
const commentsAppendixMarker = "\n---\n\n## 评论\n"

// LocalDocMeta 从本地已下载的 markdown 文件重建 DocMeta（跳过未变化文档时索引生成需要）。
// 标题取正文首个 H1（即文档标题行）；标题结构剔除标题行与评论附录，对齐新鲜下载的口径。
func LocalDocMeta(path, body string) DocMeta {
	if idx := strings.Index(body, commentsAppendixMarker); idx >= 0 {
		body = body[:idx]
	}
	return DocMeta{
		Title:    ExtractTitle(body),
		RelPath:  filepath.Base(path),
		Headings: BuildHeadingTree(ExtractHeadingsFromMarkdown(RemoveFirstHeading(body))),
	}
}

// ExtractTokenFromURL 从飞书 URL 中提取文档 token
func ExtractTokenFromURL(url string) string {
	re := regexp.MustCompile(`/(wiki|docx|docs)/([a-zA-Z0-9]+)`)
	if m := re.FindStringSubmatch(url); len(m) == 3 {
		return m[2]
	}
	return ""
}

// RemoveFirstHeading 从 markdown 正文移除第一个一级标题
// 用于上传时避免文档标题重复
func RemoveFirstHeading(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 匹配 ATX 风格标题: # Title
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			// 移除这一行，保留其余内容
			result := strings.Join(append(lines[:i], lines[i+1:]...), "\n")
			return strings.TrimPrefix(result, "\n")
		}
	}
	return body
}
