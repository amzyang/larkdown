package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/spf13/cobra"
)

// configInitOptions 汇总 config init 的 flag。
type configInitOptions struct {
	force      bool   // 已有 appId/appSecret 时允许覆盖（同时清空旧应用的登录令牌）
	noWait     bool   // 仅发起注册并打印后立即返回、不轮询（供 agent/CI/无头环境两段式）
	deviceCode string // 用前一次 --no-wait 得到的设备码恢复轮询
	json       bool   // 输出机读 JSON（app_registration / app_registered 事件）
}

// newConfigInitCommand 构造 `config init` 子命令：匿名 device flow 自动创建飞书
// PersonalAgent 个人应用并把凭证写入配置，替代去开放平台手动建应用抄 appId/appSecret。
func newConfigInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a Feishu PersonalAgent app automatically and save its credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			noWait, _ := cmd.Flags().GetBool("no-wait")
			deviceCode, _ := cmd.Flags().GetString("device-code")
			jsonOut, _ := cmd.Flags().GetBool("json")
			return handleConfigInitCommand(cmd.Context(), configInitOptions{
				force:      force,
				noWait:     noWait,
				deviceCode: deviceCode,
				json:       jsonOut,
			})
		},
	}
	fl := cmd.Flags()
	fl.Bool("force", false, "Overwrite existing app credentials (also clears saved login tokens)")
	fl.Bool("no-wait", false, "Begin registration, print the device code and exit without polling (for agent/CI two-step flow)")
	fl.String("device-code", "", "Resume polling with a device code from a prior --no-wait run")
	fl.Bool("json", false, "Emit machine-readable JSON events (app_registration / app_registered)")
	return cmd
}

// validateConfigInitOptions 校验互斥的 flag 组合。
func validateConfigInitOptions(opts configInitOptions) error {
	if opts.noWait && opts.deviceCode != "" {
		return fmt.Errorf("--no-wait 与 --device-code 互斥：前者发起注册，后者恢复轮询")
	}
	return nil
}

// checkExistingCredentials 保护已有应用凭证：覆盖需显式 --force（并意味着清空旧应用的登录令牌）。
func checkExistingCredentials(config *core.Config, force bool) error {
	if force {
		return nil
	}
	if config.Feishu.AppId != "" || config.Feishu.AppSecret != "" {
		return fmt.Errorf("配置已有应用凭证（appId %s）。如需替换为新应用请加 --force（将同时清除已保存的登录令牌，需重新 larkdown auth login）", maskSecret(config.Feishu.AppId))
	}
	return nil
}

// handleConfigInitCommand 自动创建 PersonalAgent 应用（匿名 device flow）。
//
// 三种模式（与 auth login 同构）：
//   - 默认（阻塞）：发起注册 → 展示授权链接（尽力打开浏览器）→ 原地轮询到应用创建完成。
//   - --no-wait：仅发起注册并打印（含 device_code + 恢复命令）后立即返回，不轮询。
//   - --device-code <code>：跳过发起，用已有设备码恢复轮询换取并保存凭证。
func handleConfigInitCommand(ctx context.Context, opts configInitOptions) error {
	if err := validateConfigInitOptions(opts); err != nil {
		return err
	}

	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		config = core.NewConfig("", "")
	}
	// 覆盖保护在任何网络请求之前，begin 与 --device-code 恢复两条路径都要过
	if err := checkExistingCredentials(config, opts.force); err != nil {
		return err
	}

	reg := core.NewAppRegistrar(globalOpts.clientOpts)

	// 恢复轮询模式：用已有 device_code 直接轮询换取应用凭证
	if opts.deviceCode != "" {
		if !opts.json {
			fmt.Println("正在轮询应用创建结果...（在浏览器完成授权后将自动继续）")
		}
		creds, err := reg.PollAppRegistration(ctx, opts.deviceCode, resumePollInterval, resumePollExpiresIn)
		if err != nil {
			return err
		}
		return saveAppCredentials(os.Stdout, config, configPath, creds, opts.json)
	}

	code, err := reg.BeginAppRegistration(ctx)
	if err != nil {
		return fmt.Errorf("自动创建应用失败（%w）。可改用手动方式：在 open.feishu.cn 创建应用后执行 larkdown config --appId --appSecret", err)
	}

	// 发起-即返回模式：打印设备码供后续 --device-code 恢复，不阻塞轮询
	if opts.noWait {
		emitAppRegistration(os.Stdout, code, opts.json, true, opts.force)
		return nil
	}

	// 默认阻塞模式：展示授权入口 + 尽力打开浏览器 + 原地轮询到完成
	emitAppRegistration(os.Stdout, code, opts.json, false, opts.force)
	if err := openBrowser(code.VerificationURIComplete); err != nil && !opts.json {
		fmt.Printf("无法自动打开浏览器: %v\n", err)
	}
	if !opts.json {
		fmt.Println("\n等待授权中...（在浏览器完成授权后将自动继续）")
	}
	creds, err := reg.PollAppRegistration(ctx, code.DeviceCode, code.Interval, code.ExpiresIn)
	if err != nil {
		return err
	}
	return saveAppCredentials(os.Stdout, config, configPath, creds, opts.json)
}

// emitAppRegistration 输出应用注册授权入口；showResumeHint 为 true 时附上 --device-code
// 恢复命令（--no-wait 用），withForce 为 true 时恢复命令带 --force（第二段是独立进程，不继承覆盖确认）。
func emitAppRegistration(out io.Writer, code *core.AppRegistrationCode, asJSON, showResumeHint, withForce bool) {
	if asJSON {
		printJSON(out, map[string]any{
			"event":                     "app_registration",
			"device_code":               code.DeviceCode,
			"user_code":                 code.UserCode,
			"verification_uri":          code.VerificationURI,
			"verification_uri_complete": code.VerificationURIComplete,
			"expires_in":                code.ExpiresIn,
			"interval":                  code.Interval,
		})
		return
	}
	fmt.Fprintln(out, "请在浏览器中打开以下链接，登录飞书并确认创建个人应用：")
	fmt.Fprintf(out, "\n    %s\n\n", code.VerificationURIComplete)
	if code.UserCode != "" {
		fmt.Fprintf(out, "如页面提示输入验证码，请输入：%s\n", code.UserCode)
		if code.VerificationURI != "" {
			fmt.Fprintf(out, "（或手动访问 %s 并输入上述验证码）\n", code.VerificationURI)
		}
	}
	if showResumeHint {
		resume := "larkdown config init --device-code " + code.DeviceCode
		if withForce {
			resume += " --force"
		}
		fmt.Fprintf(out, "\n设备码 device_code：%s\n", code.DeviceCode)
		fmt.Fprintln(out, "授权完成后运行以下命令换取并保存应用凭证（适合 agent/CI/无头环境两段式）：")
		fmt.Fprintf(out, "    %s\n", resume)
	}
}

// saveAppCredentials 把注册到的应用凭证写回 config 并落盘，然后输出结果。
// 旧 token 属旧应用，无条件清空（全新配置时清空是 no-op）；Output 段原样保留。
func saveAppCredentials(out io.Writer, config *core.Config, configPath string, creds *core.AppCredentials, asJSON bool) error {
	config.Feishu.AppId = creds.AppId
	config.Feishu.AppSecret = creds.AppSecret
	config.Feishu.UserAccessToken = ""
	config.Feishu.RefreshToken = ""
	config.Feishu.TokenExpireTime = 0
	config.Feishu.RefreshTokenExpireTime = 0

	if err := config.WriteConfig2File(configPath); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	if asJSON {
		printJSON(out, map[string]any{
			"event":        "app_registered",
			"app_id":       creds.AppId,
			"open_id":      creds.OpenID,
			"tenant_brand": creds.TenantBrand,
			"config_path":  configPath,
		})
		return nil
	}
	fmt.Fprintf(out, "\n应用创建成功！\n")
	fmt.Fprintln(out, utils.PrettyPrint(maskConfigForDisplay(config)))
	fmt.Fprintln(out, "请执行 larkdown auth login 完成用户授权")
	return nil
}
