package core

import (
	"context"
	"fmt"
	"io"
)

// TokenRevoker 抽象 token 撤销能力（consumer 侧接口），OAuthManager 天然满足。
type TokenRevoker interface {
	RevokeToken(ctx context.Context, token, tokenTypeHint string) error
}

// Logout 执行登出：best-effort 撤销远端 token，然后清空本地 token 三字段并落盘。
// 撤销优先 refresh_token（撤销后整个授权失效），失败再退回 access_token；
// 撤销失败只写 warn 警告、绝不阻断本地清除（本地不清会让 config 残留失效凭证）。
// 调用方负责先确认存在可清除的 token。
func Logout(ctx context.Context, config *Config, configPath string, revoker TokenRevoker, warn io.Writer) error {
	switch {
	case config.Feishu.RefreshToken != "":
		if err := revoker.RevokeToken(ctx, config.Feishu.RefreshToken, "refresh_token"); err != nil {
			fmt.Fprintf(warn, "warning: 撤销 refresh_token 失败: %v\n", err)
			if config.Feishu.UserAccessToken != "" {
				if err := revoker.RevokeToken(ctx, config.Feishu.UserAccessToken, "access_token"); err != nil {
					fmt.Fprintf(warn, "warning: 撤销 access_token 失败: %v\n", err)
				}
			}
		}
	case config.Feishu.UserAccessToken != "":
		if err := revoker.RevokeToken(ctx, config.Feishu.UserAccessToken, "access_token"); err != nil {
			fmt.Fprintf(warn, "warning: 撤销 access_token 失败: %v\n", err)
		}
	}

	config.Feishu.UserAccessToken = ""
	config.Feishu.RefreshToken = ""
	config.Feishu.TokenExpireTime = 0
	return config.WriteConfig2File(configPath)
}
