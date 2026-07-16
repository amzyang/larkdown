package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all"
	"github.com/chyroc/lark"
)

const (
	cookieDomain      = "feishu.cn"
	fallbackURLFormat = "https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/%s/?preview_type=16"
	// previewTypeSourceFile 是官方 drive/v1 preview_download 的 preview_type 取值：源文件
	// （返回原始字节而非缩略图/渲染预览，故支持任意文件类型）。与上面内部接口 URL 里的
	// preview_type=16 同值，对齐 lark-cli 的 PreviewType_SOURCE_FILE。
	previewTypeSourceFile = "16"
)

// browserUserAgents 按浏览器名称映射 User-Agent（最新版本）
var browserUserAgents = map[string]string{
	"chrome":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
	"chromium": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
	"edge":     "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36 Edg/137.0.0.0",
	"brave":    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
	"firefox":  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:139.0) Gecko/20100101 Firefox/139.0",
	"safari":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Safari/605.1.15",
}

// defaultUserAgent 未匹配浏览器时的 fallback UA
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"

// browserGroup 代表一个浏览器 profile 的 cookie 集合
type browserGroup struct {
	browser string // kooky Browser() 返回值：chrome/firefox/safari/edge/brave...
	profile string // kooky Profile() 返回值：Person 1/default/...
	cookies []*kooky.Cookie
}

// cookieHeader 构建该组的 Cookie header 字符串
func (g *browserGroup) cookieHeader() string {
	var parts []string
	for _, ck := range g.cookies {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

// userAgent 返回与该浏览器匹配的 User-Agent
func (g *browserGroup) userAgent() string {
	if ua, ok := browserUserAgents[strings.ToLower(g.browser)]; ok {
		return ua
	}
	return defaultUserAgent
}

// cookieState 懒加载的 cookie 状态（按浏览器 profile 隔离）
type cookieState struct {
	once           sync.Once
	groups         []browserGroup               // 按 cookie 数量降序排序
	preferBrowser  atomic.Pointer[browserGroup] // 全局记住成功的浏览器组
	preferFallback sync.Map                     // docToken → struct{}, 记录哪些文档已触发 cookie 回退
}

type contextKey string

const (
	docTokenKey  contextKey = "docToken"
	prefixURLKey contextKey = "prefixURL"
)

// WithDocToken 将文档 token 注入 context，用于 cookie 回退的按文档隔离
func WithDocToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, docTokenKey, token)
}

// WithPrefixURL 将飞书域名前缀注入 context，用于构建 Referer header
func WithPrefixURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, prefixURLKey, url)
}

func docTokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(docTokenKey).(string); ok {
		return v
	}
	return ""
}

func prefixURLFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(prefixURLKey).(string); ok {
		return v
	}
	return ""
}

// shouldPreferCookie 判断当前文档是否已标记为优先走受限通道回退（跳过必然失败的 API 直连）。
func (c *Client) shouldPreferCookie(ctx context.Context) bool {
	if dt := docTokenFromContext(ctx); dt != "" {
		_, ok := c.cookie.preferFallback.Load(dt)
		return ok
	}
	return false
}

// markCookieFallbackSuccess 标记当前文档的受限通道回退成功，后续同文档资源直接走级联
// （官方 preview_download 优先，失败再 cookie），不再先试注定被权限拦截的 API 直连。
func (c *Client) markCookieFallbackSuccess(ctx context.Context) {
	if dt := docTokenFromContext(ctx); dt != "" {
		if _, loaded := c.cookie.preferFallback.LoadOrStore(dt, struct{}{}); !loaded {
			log.Printf("文档 %s 后续下载将优先走受限通道回退", dt)
		}
	}
}

// IsPermissionError 检查错误是否为权限错误（403、PermissionViolations 或下载导出禁用 1069902）。
// 使用 errors.As 解包 fmt.Errorf("...%w", err) 后的 *lark.Error。
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	var le *lark.Error
	if errors.As(err, &le) {
		// 1069902: owner 在文档安全设置里禁用了下载/导出
		if le.Code == 1069902 {
			return true
		}
		if le.ErrorDetail != nil && len(le.ErrorDetail.PermissionViolations) > 0 {
			return true
		}
	}
	return strings.Contains(err.Error(), "403")
}

// initCookies 按 (browser, profile) 分组收集 feishu.cn 域名 cookie
func (c *Client) initCookies() {
	c.cookie.once.Do(func() {
		ctx := context.Background()

		// 按 (browser, profile) 分组
		groupMap := make(map[string]*browserGroup) // key: "browser\x00profile"
		for store, err := range kooky.TraverseCookieStores(ctx) {
			if err != nil || store == nil {
				continue
			}
			browser := store.Browser()
			profile := store.Profile()
			cookies := store.TraverseCookies(
				kooky.DomainHasSuffix(cookieDomain), kooky.Valid,
			).Collect(ctx)
			store.Close()
			if len(cookies) == 0 {
				continue
			}
			key := browser + "\x00" + profile
			if g, ok := groupMap[key]; ok {
				g.cookies = append(g.cookies, cookies...)
			} else {
				groupMap[key] = &browserGroup{
					browser: browser,
					profile: profile,
					cookies: cookies,
				}
			}
		}

		if len(groupMap) == 0 {
			log.Printf("未找到 %s 域名的浏览器 cookie", cookieDomain)
			return
		}

		// 收集并按 cookie 数量降序排序
		groups := make([]browserGroup, 0, len(groupMap))
		for _, g := range groupMap {
			groups = append(groups, *g)
		}
		sort.Slice(groups, func(i, j int) bool {
			return len(groups[i].cookies) > len(groups[j].cookies)
		})
		c.cookie.groups = groups

		// 日志输出
		var descs []string
		for _, g := range groups {
			descs = append(descs, fmt.Sprintf("%s/%s (%d cookies)", g.browser, g.profile, len(g.cookies)))
		}
		log.Printf("已加载 %d 个浏览器组: %s", len(groups), strings.Join(descs, ", "))
	})
}

// buildReferer 根据 context 中的 prefixURL 和 docToken 构造 Referer
func buildReferer(ctx context.Context) string {
	docToken := docTokenFromContext(ctx)
	prefixURL := prefixURLFromContext(ctx)
	switch {
	case prefixURL != "" && docToken != "":
		return prefixURL + "/docx/" + docToken
	case docToken != "":
		return "https://feishu.cn/docx/" + docToken
	default:
		return ""
	}
}

// cookieFallbackDownload 级联尝试各浏览器组下载，返回 (data, filename, error)
func (c *Client) cookieFallbackDownload(ctx context.Context, fileToken string) ([]byte, string, error) {
	c.initCookies()
	if len(c.cookie.groups) == 0 {
		return nil, "", fmt.Errorf("无可用的浏览器 cookie")
	}

	url := fmt.Sprintf(fallbackURLFormat, fileToken)
	referer := buildReferer(ctx)

	// 构建尝试序列：preferBrowser 优先，然后 groups（去重）
	preferred := c.cookie.preferBrowser.Load()
	var tryList []*browserGroup
	if preferred != nil {
		tryList = append(tryList, preferred)
	}
	for i := range c.cookie.groups {
		g := &c.cookie.groups[i]
		if preferred != nil && g.browser == preferred.browser && g.profile == preferred.profile {
			continue // 去重
		}
		tryList = append(tryList, g)
	}

	var lastErr error
	for _, g := range tryList {
		data, filename, err := c.doDownloadWithGroup(ctx, url, referer, g)
		if err == nil {
			// 记住成功的浏览器组
			c.cookie.preferBrowser.Store(g)
			if filename == "" {
				filename = fileToken
			}
			return data, filename, nil
		}
		lastErr = err
		log.Printf("浏览器组 %s/%s 下载失败: %v", g.browser, g.profile, err)
	}
	return nil, "", fmt.Errorf("所有浏览器组均失败: %w", lastErr)
}

// doDownloadWithGroup 用指定浏览器组的 cookie 和 headers 执行一次下载
func (c *Client) doDownloadWithGroup(ctx context.Context, url, referer string, g *browserGroup) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Cookie", g.cookieHeader())
	req.Header.Set("User-Agent", g.userAgent())
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("cookie 备用下载请求失败 (%s/%s): %w", g.browser, g.profile, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("cookie 备用下载失败 HTTP %d (%s/%s): %s", resp.StatusCode, g.browser, g.profile, body)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("读取响应失败: %w", err)
	}

	filename := filenameFromResponse(resp, "")
	return data, filename, nil
}

// filenameFromResponse 从 HTTP 响应中提取文件名
func filenameFromResponse(resp *http.Response, fallback string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := params["filename"]; name != "" {
				return name
			}
		}
	}
	return fallback
}

// cookieFallbackDownloadImage 图片 fallback：下载 + 校验 Content-Type + 写入缓存
func (c *Client) cookieFallbackDownloadImage(ctx context.Context, token string) ([]byte, string, error) {
	data, filename, err := c.cookieFallbackDownload(ctx, token)
	if err != nil {
		return nil, "", err
	}
	// 校验是否为图片
	ct := http.DetectContentType(data)
	if !isImageContent(ct, data) {
		return nil, "", fmt.Errorf("cookie 下载的内容不是图片: %s", ct)
	}
	// 写入缓存
	_ = c.imageCache.Put(token, data, filename)
	return data, filename, nil
}

// cookieFallbackDownloadMedia 附件 fallback：下载 + 写入缓存
func (c *Client) cookieFallbackDownloadMedia(ctx context.Context, token string) ([]byte, string, error) {
	data, filename, err := c.cookieFallbackDownload(ctx, token)
	if err != nil {
		return nil, "", err
	}
	_ = c.imageCache.PutFile(token, data, filename)
	return data, filename, nil
}

// previewDownloadReq 是 drive/v1 medias/{token}/preview_download 的请求：file_token 走 path
// （:file_token 占位），preview_type 走 query。
type previewDownloadReq struct {
	FileToken   string `path:"file_token" json:"-"`
	PreviewType string `query:"preview_type" json:"-"`
}

// mediaStreamData 承载 preview_download 返回的文件流（镜像 SDK downloadDriveMediaResp.Data）。
type mediaStreamData struct {
	File     io.Reader
	Filename string
}

// previewDownloadResp 镜像 SDK 私有 downloadDriveMediaResp：Code/Msg 供 SDK 的 getCodeMsg 反射出
// 错误码（权限错误据此浮现为 *lark.Error，不会被静默吞掉），Data 承载文件流；SetReader/SetFilename
// 命中 SDK 的 readerSetter/filenameSetter 鸭子接口。
type previewDownloadResp struct {
	Code int64            `json:"code,omitempty"`
	Msg  string           `json:"msg,omitempty"`
	Data *mediaStreamData `json:"data,omitempty"`
}

func (r *previewDownloadResp) SetReader(file io.Reader) {
	if r.Data == nil {
		r.Data = &mediaStreamData{}
	}
	r.Data.File = file
}

func (r *previewDownloadResp) SetFilename(filename string) {
	if r.Data == nil {
		r.Data = &mediaStreamData{}
	}
	r.Data.Filename = filename
}

// previewDownloadMedia 走官方 open API GET drive/v1/medias/{token}/preview_download?preview_type=16
// （SOURCE_FILE，取原始文件而非缩略图/渲染预览，故支持任意文件类型），用 access token 鉴权，
// 对齐 lark-cli 的 `docs +media-preview`。SDK 无封装，用 RawRequest（自动复用 token 注入 + 限流 +
// 超时）；响应类型镜像 SDK downloadDriveMediaResp 以便 getCodeMsg 反射出权限错误码。
//
// 与 cookieFallbackDownload 的区别：官方端点用 access token 而非偷来的浏览器 cookie，不依赖本地
// 浏览器登录态。前提是文档「可预览」——飞书预览权限与下载权限分离时，禁下载的文档仍可经此取源文件。
func (c *Client) previewDownloadMedia(ctx context.Context, token string) ([]byte, string, error) {
	resp := new(previewDownloadResp)
	_, err := c.larkClient.RawRequest(ctx, &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "PreviewDownloadMedia",
		Method: "GET",
		URL:    "https://open.feishu.cn/open-apis/drive/v1/medias/:file_token/preview_download",
		Body: &previewDownloadReq{
			FileToken:   token,
			PreviewType: previewTypeSourceFile,
		},
		NeedUserAccessToken:   c.userAccessToken != "",
		NeedTenantAccessToken: c.userAccessToken == "",
		MethodOption:          c.userMethodOption(),
	}, resp)
	if err != nil {
		return nil, "", err
	}
	if resp.Data == nil || resp.Data.File == nil {
		return nil, "", fmt.Errorf("preview_download 未返回文件流 (token=%s)", token)
	}
	data, err := io.ReadAll(resp.Data.File)
	if err != nil {
		return nil, "", err
	}
	filename := resp.Data.Filename
	if filename == "" {
		filename = token
	}
	return data, filename, nil
}

// restrictedFallbackImage 受限图片下载级联：先官方 preview_download（access token），失败再降级
// cookie hack。命中官方端点时校验内容确为图片并写缓存；cookie 分支内部已自带校验与缓存。
func (c *Client) restrictedFallbackImage(ctx context.Context, token string) ([]byte, string, error) {
	data, filename, err := c.previewDownloadMedia(ctx, token)
	if err == nil {
		if ct := http.DetectContentType(data); !isImageContent(ct, data) {
			err = fmt.Errorf("preview_download 内容不是图片: %s", ct)
		} else {
			_ = c.imageCache.Put(token, data, filename)
			return data, filename, nil
		}
	}
	log.Printf("preview_download 官方回退失败，降级 cookie: %v", err)
	data, filename, cookieErr := c.cookieFallbackDownloadImage(ctx, token)
	if cookieErr != nil {
		return nil, "", fmt.Errorf("preview_download 失败(%v)且 cookie 回退失败: %w", err, cookieErr)
	}
	return data, filename, nil
}

// restrictedFallbackMedia 受限附件下载级联：先官方 preview_download（任意文件类型），失败再降级
// cookie hack。
func (c *Client) restrictedFallbackMedia(ctx context.Context, token string) ([]byte, string, error) {
	data, filename, err := c.previewDownloadMedia(ctx, token)
	if err == nil {
		_ = c.imageCache.PutFile(token, data, filename)
		return data, filename, nil
	}
	log.Printf("preview_download 官方回退失败，降级 cookie: %v", err)
	data, filename, cookieErr := c.cookieFallbackDownloadMedia(ctx, token)
	if cookieErr != nil {
		return nil, "", fmt.Errorf("preview_download 失败(%v)且 cookie 回退失败: %w", err, cookieErr)
	}
	return data, filename, nil
}
