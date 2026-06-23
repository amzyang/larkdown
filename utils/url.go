package utils

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// UrlType 表示飞书 URL 的类型
type UrlType int

const (
	UrlTypeUnknown      UrlType = iota
	UrlTypeDocx                 // /docx/[token]
	UrlTypeWikiNode             // /wiki/[token] (需 API 判断是 space 还是 node)
	UrlTypeWikiSettings         // /wiki/settings/[space_id]
	UrlTypeFolder               // /drive/folder/[token]
	UrlTypeFile                 // /file/[token]
)

// ParsedUrl 包含解析后的 URL 信息
type ParsedUrl struct {
	Type      UrlType
	Token     string
	PrefixURL string
}

// 预编译正则表达式（避免每次调用重新编译）
var (
	rePrefixURL     = regexp.MustCompile(`^(https://[\w-.]+)/`)
	reWikiSettings  = regexp.MustCompile(`/wiki/settings/([a-zA-Z0-9]+)(?:\?|$)`)
	reWikiSpace     = regexp.MustCompile(`/wiki/space/([a-zA-Z0-9]+)(?:\?|$)`)
	reDriveFolder   = regexp.MustCompile(`/drive/folder/([a-zA-Z0-9]+)`)
	reDocx          = regexp.MustCompile(`/docx/([a-zA-Z0-9]+)`)
	reFile          = regexp.MustCompile(`/file/([a-zA-Z0-9]+)`)
	reWikiNode      = regexp.MustCompile(`/wiki/([a-zA-Z0-9]+)(?:\?|$)`)
	reDocumentURL   = regexp.MustCompile(`^https://[\w-.]+/(docs|docx|wiki)/([a-zA-Z0-9]+)`)
	reFolderURL     = regexp.MustCompile(`^https://[\w-.]+/drive/folder/([a-zA-Z0-9]+)`)
	reWikiURL1      = regexp.MustCompile(`^(https://[\w-.]+)/wiki/settings/([a-zA-Z0-9]+)(?:\?.*)?$`)
	reWikiURL2      = regexp.MustCompile(`^(https://[\w-.]+)/wiki/([a-zA-Z0-9]+)(?:\?.*)?$`)
	reWikiURL3      = regexp.MustCompile(`^(https://[\w-.]+)/wiki/space/([a-zA-Z0-9]+)(?:\?.*)?$`)
	reWikiPath      = regexp.MustCompile(`/wiki/([a-zA-Z0-9]+)`)
	reWikiSettingsP = regexp.MustCompile(`/wiki/settings/([a-zA-Z0-9]+)`)
	reWikiSpaceP    = regexp.MustCompile(`/wiki/space/([a-zA-Z0-9]+)`)
)

// ParseFeishuUrl 统一解析飞书 URL，自动识别类型
func ParseFeishuUrl(rawUrl string) (*ParsedUrl, error) {
	prefixMatch := rePrefixURL.FindStringSubmatch(rawUrl)
	if prefixMatch == nil {
		return nil, errors.Errorf("invalid feishu URL format: %s", rawUrl)
	}
	prefixURL := prefixMatch[1]

	if match := reWikiSettings.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeWikiSettings, Token: match[1], PrefixURL: prefixURL}, nil
	}
	if match := reWikiSpace.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeWikiSettings, Token: match[1], PrefixURL: prefixURL}, nil
	}
	if match := reDriveFolder.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeFolder, Token: match[1], PrefixURL: prefixURL}, nil
	}
	if match := reDocx.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeDocx, Token: match[1], PrefixURL: prefixURL}, nil
	}
	if match := reFile.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeFile, Token: match[1], PrefixURL: prefixURL}, nil
	}
	if match := reWikiNode.FindStringSubmatch(rawUrl); match != nil {
		return &ParsedUrl{Type: UrlTypeWikiNode, Token: match[1], PrefixURL: prefixURL}, nil
	}

	return nil, errors.Errorf("unsupported feishu URL format: %s", rawUrl)
}

func UnescapeURL(rawURL string) string {
	if u, err := url.QueryUnescape(rawURL); err == nil {
		return u
	}
	return rawURL
}

// EscapeURL 对 URL 做 url_encode（飞书 mention_doc.URL 写入要求：整串 percent-encode，
// 官方示例形如 https%3A%2F%2F...）。与 UnescapeURL 互逆，把下载侧解码后的可读 href 还原为编码形式。
//
// url.QueryEscape 把空格编码为 '+'（form-urlencoded 风格），而飞书/RFC3986 期望 '%20'，
// 故统一替换为 '%20'，使「飞书 %20 编码 → 下载解码 → 上传编码」字节级稳定。
// 这里 QueryEscape 产出的 '+' 只来自空格（字面 '+' 已编码为 '%2B'），全量替换安全。
func EscapeURL(rawURL string) string {
	return strings.ReplaceAll(url.QueryEscape(rawURL), "+", "%20")
}

func ValidateDocumentURL(url string) (string, string, error) {
	matchResult := reDocumentURL.FindStringSubmatch(url)
	if matchResult == nil || len(matchResult) != 3 {
		return "", "", errors.Errorf("Invalid feishu/larksuite document URL pattern")
	}
	return matchResult[1], matchResult[2], nil
}

func ValidateFolderURL(url string) (string, error) {
	matchResult := reFolderURL.FindStringSubmatch(url)
	if matchResult == nil || len(matchResult) != 2 {
		return "", errors.Errorf("Invalid feishu/larksuite folder URL pattern")
	}
	return matchResult[1], nil
}

func ValidateWikiURL(url string) (string, string, error) {
	if matchResult := reWikiURL1.FindStringSubmatch(url); matchResult != nil && len(matchResult) == 3 {
		return matchResult[1], matchResult[2], nil
	}
	if matchResult := reWikiURL3.FindStringSubmatch(url); matchResult != nil && len(matchResult) == 3 {
		return matchResult[1], matchResult[2], nil
	}
	if matchResult := reWikiURL2.FindStringSubmatch(url); matchResult != nil && len(matchResult) == 3 {
		return matchResult[1], matchResult[2], nil
	}
	return "", "", errors.Errorf("Invalid feishu/larksuite folder URL pattern")
}

func ExtractPrefixURL(rawURL string) string {
	matchResult := rePrefixURL.FindStringSubmatch(rawURL)
	if matchResult == nil || len(matchResult) != 2 {
		return ""
	}
	return matchResult[1]
}

// ParseWikiURLResult Wiki URL 解析结果
type ParseWikiURLResult struct {
	PrefixURL string // https://xxx.feishu.cn
	SpaceID   string // 知识库空间 ID (仅从 /wiki/settings/xxx 提取)
	NodeToken string // Wiki 节点 token (从 /wiki/xxx 提取)
}

// ParseWikiURL 解析 Wiki URL，提取 space_id 和 node_token
// 支持两种格式:
//   - https://xxx.feishu.cn/wiki/settings/xxxx -> 返回 spaceID
//   - https://xxx.feishu.cn/wiki/xxxx -> 返回 nodeToken（可能是 node_token 或 space_id，需 API 判断）
func ParseWikiURL(rawURL string) (*ParseWikiURLResult, error) {
	prefixMatch := rePrefixURL.FindStringSubmatch(rawURL)
	if prefixMatch == nil {
		return nil, errors.Errorf("invalid feishu URL format: %s", rawURL)
	}

	result := &ParseWikiURLResult{PrefixURL: prefixMatch[1]}

	if match := reWikiSettingsP.FindStringSubmatch(rawURL); match != nil {
		result.SpaceID = match[1]
		return result, nil
	}
	if match := reWikiSpaceP.FindStringSubmatch(rawURL); match != nil {
		result.SpaceID = match[1]
		return result, nil
	}
	if match := reWikiPath.FindStringSubmatch(rawURL); match != nil {
		result.NodeToken = match[1]
		return result, nil
	}

	return nil, errors.Errorf("无法解析 Wiki URL: %s", rawURL)
}
