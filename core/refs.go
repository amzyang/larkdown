package core

import (
	"strings"
	"sync"

	"github.com/amzyang/larkdown/utils"
)

// DocRef 正文中对另一篇飞书文档的引用（@文档 mention 或行内链接）。
// 仅收录可转 Markdown 的 docx/wiki 两类；Token 取 URL 路径段，
// 与 frontmatter source 经 ExtractTokenFromURL 提取的口径一致（mirror prune 依赖）。
// yaml 标签供下载版本边车持久化（skip-unchanged 时回放，见 download_manifest.go）。
type DocRef struct {
	Token   string `yaml:"token"`           // URL token（/docx/ 或 /wiki/ 路径段）
	ObjType string `yaml:"obj_type"`        // "docx" | "wiki"
	URL     string `yaml:"url"`             // 归一化解码 URL：<prefix>/<docx|wiki>/<token>
	Title   string `yaml:"title,omitempty"` // 显示标题/链接文本，仅用于日志提示
}

// DocRefFromMention 从 @文档 mention 构造 DocRef。
// obj-type 非 docx/wiki（sheet/bitable/mindnote/file/slide/旧版 doc）返回 ok=false。
func DocRefFromMention(token, objType, rawURL, title string) (DocRef, bool) {
	if token == "" || (objType != "docx" && objType != "wiki") {
		return DocRef{}, false
	}
	prefix := utils.ExtractPrefixURL(rawURL)
	if prefix == "" {
		return DocRef{}, false
	}
	return DocRef{
		Token:   token,
		ObjType: objType,
		URL:     prefix + "/" + objType + "/" + token,
		Title:   title,
	}, true
}

// DocRefFromLink 从行内链接构造 DocRef；仅飞书 docx/wiki 文档链接返回 ok=true。
// 域名不做限制（与 URL 解析的整体策略一致），非文档路径/非 https 链接一律排除。
func DocRefFromLink(rawURL, text string) (DocRef, bool) {
	// 剥离 #fragment：锚点不参与类型识别（wiki 正则要求 token 后紧跟 ? 或行尾）
	if i := strings.IndexByte(rawURL, '#'); i >= 0 {
		rawURL = rawURL[:i]
	}
	parsed, err := utils.ParseFeishuUrl(rawURL)
	if err != nil {
		return DocRef{}, false
	}
	var objType string
	switch parsed.Type {
	case utils.UrlTypeDocx:
		objType = "docx"
	case utils.UrlTypeWikiNode:
		objType = "wiki"
	default:
		return DocRef{}, false
	}
	return DocRef{
		Token:   parsed.Token,
		ObjType: objType,
		URL:     parsed.PrefixURL + "/" + objType + "/" + parsed.Token,
		Title:   text,
	}, true
}

// RefCollector 并发安全的引用收集器（nil 接收者全部 no-op），按 Token 去重。
// 主下载阶段的各并发 goroutine 共享同一实例，follow 阶段 Drain 统一消费。
type RefCollector struct {
	mu   sync.Mutex
	seen map[string]bool
	refs []DocRef
}

func NewRefCollector() *RefCollector {
	return &RefCollector{seen: make(map[string]bool)}
}

func (c *RefCollector) Add(refs ...DocRef) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range refs {
		if r.Token == "" || c.seen[r.Token] {
			continue
		}
		c.seen[r.Token] = true
		c.refs = append(c.refs, r)
	}
}

// Drain 取出当前收集结果并清空待取列表（去重集保留，同一 token 不会二次产出）。
func (c *RefCollector) Drain() []DocRef {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.refs
	c.refs = nil
	return out
}
