package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/amzyang/larkdown/core"
	"github.com/urfave/cli/v3"
)

// loginPortFlag 返回登录端口 flag，供 `auth login` 与顶层兼容别名 `login` 复用。
func loginPortFlag() cli.Flag {
	return &cli.IntFlag{Name: "port", Value: core.DefaultOAuthPort, Usage: "Local callback server port"}
}

// runLogin 是 `auth login` / `login` 的共享 action。
func runLogin(ctx context.Context, cmd *cli.Command) error {
	loginOpts.port = int(cmd.Int("port"))
	return handleLoginCommand()
}

// newAuthCommand 构造 `larkdown auth` 命令组（login / status / logout）。
func newAuthCommand() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Manage Feishu authentication (login / status / logout)",
		Commands: []*cli.Command{
			{
				Name:   "login",
				Usage:  "Login with Feishu OAuth to get user_access_token",
				Flags:  []cli.Flag{loginPortFlag()},
				Action: runLogin,
			},
			{
				Name:  "status",
				Usage: "Show current authentication status (read-only, does not refresh)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return handleAuthStatusCommand(ctx)
				},
			},
			{
				Name:  "logout",
				Usage: "Revoke and clear the local user_access_token",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return handleLogoutCommand(ctx)
				},
			},
		},
	}
}

// newLoginAliasCommand 保留顶层 `login` 作为 `auth login` 的隐藏兼容别名（零迁移不破坏老脚本）。
func newLoginAliasCommand() *cli.Command {
	return &cli.Command{
		Name:   "login",
		Hidden: true,
		Usage:  "Alias of 'auth login'",
		Flags:  []cli.Flag{loginPortFlag()},
		Action: runLogin,
	}
}

// formatAuthStatus 根据配置快照与当前时间生成 auth status 的展示行（纯函数，便于测试）。
// now 注入以保证测试确定；有效性判定与 core.FeishuConfig.HasValidUserToken/NeedsRefresh 语义一致。
func formatAuthStatus(cfg core.FeishuConfig, configPath string, now time.Time) []string {
	lines := []string{
		"配置文件: " + configPath,
		"app_id: " + cfg.AppId,
	}

	valid := cfg.UserAccessToken != "" && now.Unix() < cfg.TokenExpireTime-core.UserTokenExpiryLeewaySeconds
	needsRefresh := !valid && cfg.RefreshToken != "" && cfg.UserAccessToken != ""

	switch {
	case valid:
		expireAt := time.Unix(cfg.TokenExpireTime, 0).Format("2006-01-02 15:04:05")
		lines = append(lines, "认证方式: user_access_token（有效，至 "+expireAt+"）")
	case needsRefresh:
		lines = append(lines, "认证方式: user_access_token（已过期，下次命令将自动刷新）")
	default:
		lines = append(lines, "认证方式: tenant_access_token（应用凭证）；可用 larkdown auth login 获取用户身份")
	}
	return lines
}

// handleAuthStatusCommand 只读诊断当前认证状态，不触发刷新。
func handleAuthStatusCommand(ctx context.Context) error {
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}

	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("配置文件: " + configPath)
			fmt.Println("尚未配置，请先运行 'larkdown config --appId=xxx --appSecret=xxx'")
			return nil
		}
		return err
	}

	if config.Feishu.AppId == "" || config.Feishu.AppSecret == "" {
		fmt.Println("配置文件: " + configPath)
		fmt.Println("尚未配置 appId/appSecret，请先运行 'larkdown config --appId=xxx --appSecret=xxx'")
		return nil
	}

	for _, line := range formatAuthStatus(config.Feishu, configPath, time.Now()) {
		fmt.Println(line)
	}

	// 仅当 user token 当前有效时，展示登录用户身份；调用失败不阻断（token 可能已在服务端失效）
	if config.Feishu.HasValidUserToken() {
		info, err := core.FetchUserInfo(ctx, config.Feishu.AppId, config.Feishu.AppSecret, config.Feishu.UserAccessToken, globalOpts.clientOpts)
		if err != nil {
			fmt.Printf("身份验证失败（token 可能已在服务端失效）: %v\n", err)
		} else {
			fmt.Printf("当前用户: %s (open_id: %s)\n", info.Name, info.OpenID)
		}
	}
	return nil
}

// handleLogoutCommand 撤销并清除本地 user_access_token。
func handleLogoutCommand(ctx context.Context) error {
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}

	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("当前未登录（无 user_access_token）")
			return nil
		}
		return err
	}

	if config.Feishu.UserAccessToken == "" && config.Feishu.RefreshToken == "" {
		fmt.Println("当前未登录（无 user_access_token）")
		return nil
	}

	oauthMgr := core.NewOAuthManager(config.Feishu.AppId, config.Feishu.AppSecret, core.DefaultOAuthPort, globalOpts.clientOpts)
	if err := core.Logout(ctx, config, configPath, oauthMgr, os.Stderr); err != nil {
		return fmt.Errorf("清除本地 token 失败: %w", err)
	}

	fmt.Println("已登出，本地 token 已清除；后续命令将使用应用凭证 (tenant_access_token)")
	return nil
}
