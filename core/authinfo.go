package core

import (
	"context"

	"github.com/chyroc/lark"
)

// UserInfo 是 auth status 展示所需的最小用户身份信息。
type UserInfo struct {
	Name   string
	OpenID string
}

// FetchUserInfo 用 user_access_token 调用 authen/v1/user_info 获取当前登录用户身份。
// 仅用于 auth status 的只读诊断展示；client 走 lark.New 轻量构造（对齐 NewOAuthManager）。
func FetchUserInfo(ctx context.Context, appId, appSecret, userAccessToken string, opts *ClientOptions) (*UserInfo, error) {
	larkOpts := []lark.ClientOptionFunc{
		lark.WithAppCredential(appId, appSecret),
	}
	if opts != nil && opts.Debug {
		larkOpts = append(larkOpts, lark.WithLogger(lark.NewLoggerStdout(), lark.LogLevelTrace))
	}
	client := lark.New(larkOpts...)

	resp, _, err := client.Auth.GetUserInfo(ctx, &lark.GetUserInfoReq{}, lark.WithUserAccessToken(userAccessToken))
	if err != nil {
		return nil, err
	}
	return &UserInfo{Name: resp.Name, OpenID: resp.OpenID}, nil
}
