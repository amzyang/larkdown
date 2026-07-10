package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/amzyang/larkdown/core"
	"github.com/spf13/cobra"
)

// newLoginCommand 构造 login 命令（flag 集 + RunE）。工厂返回新实例：一个 *cobra.Command
// 只能挂一个父命令，`auth login` 与顶层隐藏别名 `login` 需各持一份。
// --no-wait / --device-code 组成两段式登录（agent/CI/无头环境友好），--json 输出机读事件；
// --port 已废弃、保留为隐藏 no-op，以免 `larkdown login --port 9999` 这类老脚本因未定义 flag 中断。
func newLoginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login with Feishu OAuth device flow to get user_access_token",
		RunE:  runLogin,
	}
	fl := cmd.Flags()
	fl.Bool("no-wait", false, "Request a device code, print it and exit without polling (for agent/CI two-step login)")
	fl.String("device-code", "", "Resume polling with a device code from a prior --no-wait run")
	fl.Bool("json", false, "Emit machine-readable JSON events (device_authorization / authorized)")
	fl.Int("port", 0, "Deprecated: no-op (device flow needs no local callback)")
	_ = fl.MarkHidden("port")
	return cmd
}

// runLogin 是 `auth login` / `login` 的共享 RunE。
func runLogin(cmd *cobra.Command, args []string) error {
	noWait, _ := cmd.Flags().GetBool("no-wait")
	deviceCode, _ := cmd.Flags().GetString("device-code")
	jsonOut, _ := cmd.Flags().GetBool("json")
	return handleLoginCommand(loginOptions{
		noWait:     noWait,
		deviceCode: deviceCode,
		json:       jsonOut,
	})
}

// newAuthCommand 构造 `larkdown auth` 命令组（login / status / logout）。
func newAuthCommand() *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage Feishu authentication (login / status / logout)",
		// 无 RunE：裸 `larkdown auth` 打印 help
	}
	auth.AddCommand(
		newLoginCommand(),
		&cobra.Command{
			Use:   "status",
			Short: "Show current authentication status (read-only, does not refresh)",
			RunE: func(cmd *cobra.Command, args []string) error {
				return handleAuthStatusCommand(cmd.Context())
			},
		},
		&cobra.Command{
			Use:   "logout",
			Short: "Revoke and clear the local user_access_token",
			RunE: func(cmd *cobra.Command, args []string) error {
				return handleLogoutCommand(cmd.Context())
			},
		},
	)
	return auth
}

// newLoginAliasCommand 保留顶层 `login` 作为 `auth login` 的隐藏兼容别名（零迁移不破坏老脚本）。
func newLoginAliasCommand() *cobra.Command {
	cmd := newLoginCommand()
	cmd.Hidden = true
	cmd.Short = "Alias of 'auth login'"
	return cmd
}

// formatAuthStatus 根据配置快照与当前时间生成 auth status 的展示行（纯函数，便于测试）。
// now 注入以保证测试确定；有效性判定与 core.FeishuConfig.HasValidUserToken/NeedsRefresh 语义一致。
func formatAuthStatus(cfg core.FeishuConfig, configPath string, now time.Time) []string {
	lines := []string{
		"配置文件: " + configPath,
		"app_id: " + cfg.AppId,
	}

	valid := cfg.UserAccessToken != "" && now.Unix() < cfg.TokenExpireTime-core.UserTokenExpiryLeewaySeconds
	refreshExpired := cfg.RefreshTokenExpireTime > 0 && now.Unix() >= cfg.RefreshTokenExpireTime
	needsRefresh := !valid && cfg.RefreshToken != "" && cfg.UserAccessToken != "" && !refreshExpired

	switch {
	case valid:
		expireAt := time.Unix(cfg.TokenExpireTime, 0).Format("2006-01-02 15:04:05")
		lines = append(lines, "认证方式: user_access_token（有效，至 "+expireAt+"）")
		if cfg.RefreshTokenExpireTime > 0 {
			refreshAt := time.Unix(cfg.RefreshTokenExpireTime, 0).Format("2006-01-02 15:04:05")
			lines = append(lines, "刷新令牌有效期至: "+refreshAt)
		}
	case needsRefresh:
		lines = append(lines, "认证方式: user_access_token（已过期，下次命令将自动刷新）")
	case (cfg.UserAccessToken != "" || cfg.RefreshToken != "") && refreshExpired:
		lines = append(lines, "认证方式: 登录已过期（刷新令牌失效），请重新执行 larkdown auth login")
	default:
		lines = append(lines, "认证方式: 未登录；命令默认使用用户身份（user_access_token），请执行 larkdown auth login（--as bot 可显式使用应用凭证 tenant_access_token）")
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

	oauthMgr := core.NewOAuthManager(config.Feishu.AppId, config.Feishu.AppSecret, globalOpts.clientOpts)
	if err := core.Logout(ctx, config, configPath, oauthMgr, os.Stderr); err != nil {
		return fmt.Errorf("清除本地 token 失败: %w", err)
	}

	fmt.Println("已登出，本地 token 已清除；后续命令请重新执行 larkdown auth login，或加 --as bot 使用应用凭证 (tenant_access_token)")
	return nil
}
