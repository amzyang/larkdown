package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chyroc/lark"
)

// miaodaErrAppNotFound 是妙搭发布接口在 app_id 不存在/无权访问时返回的业务码。
const miaodaErrAppNotFound = 90002

// ExtractMiaodaAppID 从原始 app_id 或妙搭应用链接中提取 app_id。
// 按 /app/ 路径段提取，与 host 无关，因此兼容不同部署域名：
//   - app_xxx
//   - https://miaoda.feishu.cn/app/app_xxx[/]
//   - https://<sub>.aiforce.cloud/app/app_xxx[/...]
func ExtractMiaodaAppID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "/app/"); i >= 0 {
		s = s[i+len("/app/"):]
	}
	s = strings.TrimRight(s, "/")
	if j := strings.IndexAny(s, "/?#"); j >= 0 {
		s = s[:j]
	}
	return s
}

// miaodaManageURLBase 是妙搭应用的编辑管理门户前缀。
// 发布接口返回的访问链接走 aiforce.cloud 运行域，仅用于打开运行中的应用；
// 而编辑应用、设置访问权限等管理操作在妙搭门户进行，路径同样以 /app/<app_id> 寻址。
const miaodaManageURLBase = "https://miaoda.feishu.cn"

// MiaodaManageURL 返回妙搭应用的编辑管理页链接（编辑应用、设置权限等）。
func MiaodaManageURL(appID string) string {
	return miaodaManageURLBase + "/app/" + appID
}

// IsMiaodaAppNotFound 判断 err 是否为"妙搭应用不存在/无权访问"（app_id 失效）。
func IsMiaodaAppNotFound(err error) bool {
	var le *lark.Error
	if errors.As(err, &le) {
		return le.Code == miaodaErrAppNotFound
	}
	return false
}

// miaodaAPIBase 是妙搭（Miaoda）开放接口前缀。
// 端点走标准 OAPI 网关，使用 user_access_token 鉴权。
const miaodaAPIBase = "https://open.feishu.cn/open-apis/spark/v1"

// userMethodOption 构造携带 user_access_token 的 MethodOption。
// MethodOption 字段不可导出，但 functional option 闭包在 lark 包内执行，
// 可借此在包外完成赋值。
func (c *Client) userMethodOption() *lark.MethodOption {
	mo := &lark.MethodOption{}
	for _, f := range c.methodOptions() {
		f(mo)
	}
	return mo
}

// miaodaCreateAppReq 建应用请求体。
// chyroc/lark 用反射遍历 body 的 struct 字段，故必须用 struct（不能用 map）。
type miaodaCreateAppReq struct {
	Name    string `json:"name"`
	AppType string `json:"app_type"`
}

type miaodaCreateAppResp struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		App struct {
			AppID string `json:"app_id"`
		} `json:"app"`
	} `json:"data"`
}

// CreateMiaodaApp 新建一个妙搭应用，返回 app_id。当前 app_type 仅支持 "HTML"。
func (c *Client) CreateMiaodaApp(ctx context.Context, name string) (string, error) {
	resp := new(miaodaCreateAppResp)
	_, err := c.larkClient.RawRequest(ctx, &lark.RawRequestReq{
		Scope:               "Miaoda",
		API:                 "CreateApp",
		Method:              "POST",
		URL:                 miaodaAPIBase + "/apps",
		Body:                &miaodaCreateAppReq{Name: name, AppType: "HTML"},
		NeedUserAccessToken: true,
		MethodOption:        c.userMethodOption(),
	}, resp)
	if err != nil {
		return "", fmt.Errorf("创建妙搭应用失败: %w", err)
	}
	if resp.Data.App.AppID == "" {
		return "", fmt.Errorf("创建妙搭应用成功但未返回 app_id")
	}
	return resp.Data.App.AppID, nil
}

// miaodaPublishReq IsFile=true 时，io.Reader 字段的 json tag 即 multipart 字段名。
type miaodaPublishReq struct {
	File io.Reader `json:"file"`
}

type miaodaPublishResp struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		URL string `json:"url"`
	} `json:"data"`
}

// PublishMiaodaHTML 上传 tar.gz 并发布到指定妙搭应用，返回访问 URL。
func (c *Client) PublishMiaodaHTML(ctx context.Context, appID string, tarball []byte) (string, error) {
	resp := new(miaodaPublishResp)
	_, err := c.larkClient.RawRequest(ctx, &lark.RawRequestReq{
		Scope:               "Miaoda",
		API:                 "PublishHTML",
		Method:              "POST",
		URL:                 fmt.Sprintf("%s/apps/%s/upload_and_release_html_code", miaodaAPIBase, appID),
		Body:                &miaodaPublishReq{File: bytes.NewReader(tarball)},
		IsFile:              true,
		NeedUserAccessToken: true,
		MethodOption:        c.userMethodOption(),
	}, resp)
	if err != nil {
		return "", fmt.Errorf("发布妙搭 HTML 失败: %w", err)
	}
	if resp.Data.URL == "" {
		return "", fmt.Errorf("发布成功但未返回访问 URL")
	}
	return resp.Data.URL, nil
}

// 妙搭 access-scope 后端 scope 枚举（对齐 lark-cli / 官方 SDK：All=互联网 / Tenant=机构内 / Range=指定人）。
// larkdown 主动设置 Tenant / All；Range（指定人）需具体 targets，见 resolveShareScope。
const (
	MiaodaScopeTenant = "Tenant" // 机构内可见
	MiaodaScopeAll    = "All"    // 互联网可访问
)

// --share flag 取值。ShareTenant 是新建发布的默认档位（「默认对机构开放」）。
const (
	ShareSelected = "selected" // 仅自己/指定人可见：不主动调用 access-scope
	ShareTenant   = "tenant"   // 机构内可见
	SharePublic   = "public"   // 互联网可访问
)

// ValidShareValues 是 --share 合法取值，供 cmd 层白名单校验与帮助文案复用。
var ValidShareValues = []string{ShareSelected, ShareTenant, SharePublic}

// resolveShareScope 决定本次发布是否设置访问权限、设为哪一档。
//
//	explicit: --share 的原值（"" 表示未指定）
//	isNew:    本次是否新建了应用
//
// 返回 (serverScope, apply)：apply=false 表示不调用 access-scope，保持服务端现状。
//
// 默认（未指定 --share）语义：仅新建应用时开放到机构可见；更新已有应用不覆盖权限。
//
// 注意：larkdown 调的是官方 open API spark/v1 access-scope（与 lark-cli / 官方 SDK 一致）。
// 实测该接口对妙搭 HTML 应用与网页「App availability」不同步：PUT/GET 自洽，但不改变网页
// 实际访问控制（后者走妙搭内部 API，open token 触达不到）。larkdown 忠实提交此官方接口，
// 是否真正生效属妙搭侧行为；cmd 层提示会如实告知可能需去门户手动核实。
func resolveShareScope(explicit string, isNew bool) (serverScope string, apply bool) {
	switch explicit {
	case ShareTenant:
		return MiaodaScopeTenant, true
	case SharePublic:
		return MiaodaScopeAll, true
	case ShareSelected:
		return "", false
	default: // 未指定
		if isNew {
			return MiaodaScopeTenant, true
		}
		return "", false
	}
}

// miaodaAccessScopeReq 设置访问权限请求体（对齐官方 spark/v1 UpdateAppVisibilityAppReqBody）。
// require_login 仅 All（互联网）档有意义。
type miaodaAccessScopeReq struct {
	Scope        string `json:"scope"`
	RequireLogin *bool  `json:"require_login,omitempty"`
}

// buildAccessScopeReq 构造 access-scope 请求体：仅 All 档带 require_login。
func buildAccessScopeReq(serverScope string, requireLogin bool) *miaodaAccessScopeReq {
	req := &miaodaAccessScopeReq{Scope: serverScope}
	if serverScope == MiaodaScopeAll {
		req.RequireLogin = &requireLogin
	}
	return req
}

// miaodaBaseResp 是仅关心 code/msg 的通用响应容器（access-scope 无需读 data）。
type miaodaBaseResp struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
}

// SetMiaodaAccessScope PUT /apps/{app_id}/access-scope，设置应用访问范围（官方 spark/v1 接口）。
// serverScope 取 MiaodaScopeTenant / MiaodaScopeAll；requireLogin 仅 All 档生效。
func (c *Client) SetMiaodaAccessScope(ctx context.Context, appID, serverScope string, requireLogin bool) error {
	resp := new(miaodaBaseResp)
	_, err := c.larkClient.RawRequest(ctx, &lark.RawRequestReq{
		Scope:               "Miaoda",
		API:                 "SetAccessScope",
		Method:              "PUT",
		URL:                 fmt.Sprintf("%s/apps/%s/access-scope", miaodaAPIBase, appID),
		Body:                buildAccessScopeReq(serverScope, requireLogin),
		NeedUserAccessToken: true,
		MethodOption:        c.userMethodOption(),
	}, resp)
	if err != nil {
		return fmt.Errorf("设置妙搭应用访问权限失败: %w", err)
	}
	return nil
}

// PublishResult 发布结果
type PublishResult struct {
	AppID string
	URL   string
	IsNew bool // 是否新建了应用（false 表示复用已有 app_id 做更新）

	// AppliedScope 本次提交的后端 scope（MiaodaScopeTenant/MiaodaScopeAll）；
	// 未设置权限（ShareSelected 或更新保持）时为空。
	AppliedScope string
	// ScopeErr 记录设置权限失败——发布产物已上线，权限设置失败降级为警告而非阻断。
	ScopeErr error
}

// PublishHTMLArtifact 高层封装：打包本地 HTML 文件/目录 → 发布 →（按 share）提交访问权限。
//   - appID 为空：新建应用后发布
//   - appID 非空：复用该应用做更新发布（URL 不变）
//
// share 为 --share 原值（"" 表示未指定，按 resolveShareScope 的默认语义处理）。larkdown 忠实
// 调用官方 spark/v1 access-scope 提交档位；对妙搭 HTML 应用是否真正改变网页 App availability
// 属妙搭侧行为。设置失败不阻断（发布产物已上线），错误记入 ScopeErr 交由调用方提示。
func (c *Client) PublishHTMLArtifact(ctx context.Context, name, path, appID, share string) (*PublishResult, error) {
	if !c.HasUserToken() {
		return nil, fmt.Errorf("发布妙搭应用需要 user_access_token，请先执行 larkdown login")
	}
	tarball, err := buildMiaodaTarball(path)
	if err != nil {
		return nil, err
	}
	isNew := appID == ""
	if isNew {
		appID, err = c.CreateMiaodaApp(ctx, name)
		if err != nil {
			return nil, err
		}
	}
	url, err := c.PublishMiaodaHTML(ctx, appID, tarball)
	if err != nil {
		return nil, err
	}
	result := &PublishResult{AppID: appID, URL: url, IsNew: isNew}
	if serverScope, apply := resolveShareScope(share, isNew); apply {
		// require_login 固定 true——「组织外获得链接需登录」的较安全公开；免登录公开留待后续 flag。
		if serr := c.SetMiaodaAccessScope(ctx, appID, serverScope, true); serr != nil {
			result.ScopeErr = serr
		} else {
			result.AppliedScope = serverScope
		}
	}
	return result, nil
}
