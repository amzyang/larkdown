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
	if len(args) > 0 {
		return exitWithMessage("login 不接受位置参数", 1)
	}
	noWait, _ := cmd.Flags().GetBool("no-wait")
	deviceCode, _ := cmd.Flags().GetString("device-code")
	jsonOut, _ := cmd.Flags().GetBool("json")
	return handleLoginCommand(cmd.Context(), loginOptions{
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
		newAuthStatusCommand(),
		&cobra.Command{
			Use:   "logout",
			Short: "Revoke and clear the local user_access_token",
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) > 0 {
					return exitWithMessage("auth logout 不接受位置参数", 1)
				}
				return handleLogoutCommand(cmd.Context())
			},
		},
	)
	return auth
}

// newAuthStatusCommand 构造 `auth status`：只读诊断，--json 输出机读状态。
func newAuthStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current authentication status (read-only, does not refresh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitWithMessage("auth status 不接受位置参数", 1)
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			return handleAuthStatusCommand(cmd.Context(), asJSON)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON (config_path, state, expiry, user)")
	return cmd
}

// newLoginAliasCommand 保留顶层 `login` 作为 `auth login` 的隐藏兼容别名（零迁移不破坏老脚本）。
func newLoginAliasCommand() *cobra.Command {
	cmd := newLoginCommand()
	cmd.Hidden = true
	cmd.Short = "Alias of 'auth login'"
	return cmd
}

// auth status 的状态枚举（--json 的 state 字段；文本输出由 formatAuthStatus 渲染）。
const (
	authStateLoggedIn      = "logged_in"                 // user_access_token 当前有效
	authStateRefreshable   = "token_expired_refreshable" // 已过期但可自动刷新
	authStateLoginExpired  = "login_expired"             // refresh_token 失效，需重新登录
	authStateNotLoggedIn   = "not_logged_in"             // 从未登录
	authStateNotConfigured = "not_configured"            // 缺配置文件或 appId/appSecret
)

// authStatusState 计算认证状态（纯函数）；判定与 core.FeishuConfig.HasValidUserToken/
// NeedsRefresh 语义一致，now 注入保证测试确定。
func authStatusState(cfg core.FeishuConfig, now time.Time) string {
	valid := cfg.UserAccessToken != "" && now.Unix() < cfg.TokenExpireTime-core.UserTokenExpiryLeewaySeconds
	refreshExpired := cfg.RefreshTokenExpireTime > 0 && now.Unix() >= cfg.RefreshTokenExpireTime
	switch {
	case valid:
		return authStateLoggedIn
	case cfg.RefreshToken != "" && cfg.UserAccessToken != "" && !refreshExpired:
		return authStateRefreshable
	case (cfg.UserAccessToken != "" || cfg.RefreshToken != "") && refreshExpired:
		return authStateLoginExpired
	default:
		return authStateNotLoggedIn
	}
}

// formatAuthStatus 根据配置快照与当前时间生成 auth status 的展示行（纯函数，便于测试）。
func formatAuthStatus(cfg core.FeishuConfig, configPath string, now time.Time) []string {
	lines := []string{
		"配置文件: " + configPath,
		"app_id: " + cfg.AppId,
	}

	switch authStatusState(cfg, now) {
	case authStateLoggedIn:
		expireAt := time.Unix(cfg.TokenExpireTime, 0).Format("2006-01-02 15:04:05")
		lines = append(lines, "认证方式: user_access_token（有效，至 "+expireAt+"）")
		if cfg.RefreshTokenExpireTime > 0 {
			refreshAt := time.Unix(cfg.RefreshTokenExpireTime, 0).Format("2006-01-02 15:04:05")
			lines = append(lines, "刷新令牌有效期至: "+refreshAt)
		}
	case authStateRefreshable:
		lines = append(lines, "认证方式: user_access_token（已过期，下次命令将自动刷新）")
	case authStateLoginExpired:
		lines = append(lines, "认证方式: 登录已过期（刷新令牌失效），请重新执行 larkdown auth login")
	default:
		lines = append(lines, "认证方式: 未登录；命令默认使用用户身份（user_access_token），请执行 larkdown auth login（--as bot 可显式使用应用凭证 tenant_access_token）")
	}
	return lines
}

// authStatusView 是 auth status --json 的输出模型。
type authStatusView struct {
	ConfigPath           string        `json:"config_path"`
	AppID                string        `json:"app_id,omitempty"`
	State                string        `json:"state"`
	TokenExpireAt        string        `json:"token_expire_at,omitempty"`         // RFC3339
	RefreshTokenExpireAt string        `json:"refresh_token_expire_at,omitempty"` // RFC3339
	User                 *authUserView `json:"user,omitempty"`                    // 仅 logged_in 且服务端验证成功
}

type authUserView struct {
	Name   string `json:"name"`
	OpenID string `json:"open_id"`
}

// newAuthStatusView 组装 JSON 输出模型（纯函数，user 由调用方按需填充）。
func newAuthStatusView(cfg core.FeishuConfig, configPath string, now time.Time) authStatusView {
	view := authStatusView{
		ConfigPath: configPath,
		AppID:      cfg.AppId,
		State:      authStatusState(cfg, now),
	}
	if cfg.TokenExpireTime > 0 {
		view.TokenExpireAt = time.Unix(cfg.TokenExpireTime, 0).Format(time.RFC3339)
	}
	if cfg.RefreshTokenExpireTime > 0 {
		view.RefreshTokenExpireAt = time.Unix(cfg.RefreshTokenExpireTime, 0).Format(time.RFC3339)
	}
	return view
}

// handleAuthStatusCommand 只读诊断当前认证状态，不触发刷新。
func handleAuthStatusCommand(ctx context.Context, asJSON bool) error {
	configPath, err := resolveConfigPath()
	if err != nil {
		return err
	}

	config, err := core.ReadConfigFromFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	notConfigured := err != nil || config.Feishu.AppId == "" || config.Feishu.AppSecret == ""

	if notConfigured {
		if asJSON {
			printJSON(os.Stdout, authStatusView{ConfigPath: configPath, State: authStateNotConfigured})
			return nil
		}
		fmt.Println("配置文件: " + configPath)
		fmt.Println("尚未配置 appId/appSecret，请先运行 'larkdown config init' 或 'larkdown config --appId=xxx --appSecret=xxx'")
		return nil
	}

	if !asJSON {
		for _, line := range formatAuthStatus(config.Feishu, configPath, time.Now()) {
			fmt.Println(line)
		}
	}

	// 仅当 user token 当前有效时，展示登录用户身份；调用失败不阻断（token 可能已在服务端失效）
	var user *authUserView
	if config.Feishu.HasValidUserToken() {
		info, err := core.FetchUserInfo(ctx, config.Feishu.AppId, config.Feishu.AppSecret, config.Feishu.UserAccessToken, globalOpts.clientOpts)
		switch {
		case err != nil && !asJSON:
			fmt.Printf("身份验证失败（token 可能已在服务端失效）: %v\n", err)
		case err == nil:
			user = &authUserView{Name: info.Name, OpenID: info.OpenID}
			if !asJSON {
				fmt.Printf("当前用户: %s (open_id: %s)\n", info.Name, info.OpenID)
			}
		}
	}

	if asJSON {
		view := newAuthStatusView(config.Feishu, configPath, time.Now())
		view.User = user
		printJSON(os.Stdout, view)
	}
	return nil
}

// handleLogoutCommand 撤销并清除本地 user_access_token。
func handleLogoutCommand(ctx context.Context) error {
	configPath, err := resolveConfigPath()
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
