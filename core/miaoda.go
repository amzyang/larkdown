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

// PublishResult 发布结果
type PublishResult struct {
	AppID string
	URL   string
	IsNew bool // 是否新建了应用（false 表示复用已有 app_id 做更新）
}

// PublishHTMLArtifact 高层封装：打包本地 HTML 文件/目录 → 发布，返回结果。
//   - appID 为空：新建应用后发布
//   - appID 非空：复用该应用做更新发布（URL 不变）
func (c *Client) PublishHTMLArtifact(ctx context.Context, name, path, appID string) (*PublishResult, error) {
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
	return &PublishResult{AppID: appID, URL: url, IsNew: isNew}, nil
}
