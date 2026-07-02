package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/amzyang/larkdown/core"
)

// loginOptions 汇总 auth login 的 flag。
type loginOptions struct {
	noWait     bool   // 仅申请设备码并打印后立即返回、不轮询（供 agent/CI/无头环境两段式登录）
	deviceCode string // 用前一次 --no-wait 得到的设备码恢复轮询
	json       bool   // 输出机读 JSON（device_authorization / authorized 事件）
}

const (
	// 恢复轮询（--device-code）时的默认节奏：设备码的真实过期由服务端 expired_token 兜底终止，
	// 故这里只需给出足够宽的上界与标准间隔。
	resumePollInterval  = 5
	resumePollExpiresIn = 600
)

// validateLoginOptions 校验互斥的 flag 组合。
func validateLoginOptions(opts loginOptions) error {
	if opts.noWait && opts.deviceCode != "" {
		return fmt.Errorf("--no-wait 与 --device-code 互斥：前者发起授权，后者恢复轮询")
	}
	return nil
}

// handleLoginCommand 走 OAuth 2.0 设备码流程（device flow）登录。
//
// 三种模式：
//   - 默认（阻塞）：申请设备码 → 展示 URL + 验证码（尽力打开浏览器）→ 原地轮询到授权完成。
//   - --no-wait：仅申请设备码并打印（含 device_code + 恢复命令）后立即返回，不轮询。
//   - --device-code <code>：跳过申请，用已有设备码恢复轮询换取并保存令牌。
//
// 后两者组合成两段式登录：agent/CI 先 --no-wait 拿到 URL 展示给人，人授权后再 --device-code 换令牌，
// 避免单次调用被长时间轮询阻塞。--json 让上述各步输出机读事件，便于程序解析。
func handleLoginCommand(opts loginOptions) error {
	if err := validateLoginOptions(opts); err != nil {
		return err
	}

	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		return fmt.Errorf("请先运行 'larkdown config --appId=xxx --appSecret=xxx' 配置应用凭证: %w", err)
	}
	if config.Feishu.AppId == "" || config.Feishu.AppSecret == "" {
		return fmt.Errorf("请先配置 appId 和 appSecret")
	}

	ctx := context.Background()
	oauthMgr := core.NewOAuthManager(config.Feishu.AppId, config.Feishu.AppSecret, globalOpts.clientOpts)

	// 恢复轮询模式：用已有 device_code 直接轮询换取令牌
	if opts.deviceCode != "" {
		if !opts.json {
			fmt.Println("正在轮询授权结果...（在浏览器完成授权后将自动继续）")
		}
		result, err := oauthMgr.PollDeviceToken(ctx, opts.deviceCode, resumePollInterval, resumePollExpiresIn)
		if err != nil {
			return err
		}
		return saveToken(os.Stdout, config, configPath, result, opts.json)
	}

	dc, err := oauthMgr.RequestDeviceCode(ctx, core.DeviceFlowScope)
	if err != nil {
		return err
	}

	// 发起-即返回模式：打印设备码供后续 --device-code 恢复，不阻塞轮询
	if opts.noWait {
		emitDeviceAuthorization(os.Stdout, dc, opts.json, true)
		return nil
	}

	// 默认阻塞模式：展示授权入口 + 尽力打开浏览器 + 原地轮询到完成
	emitDeviceAuthorization(os.Stdout, dc, opts.json, false)
	if err := openBrowser(dc.VerificationURIComplete); err != nil && !opts.json {
		fmt.Printf("无法自动打开浏览器: %v\n", err)
	}
	if !opts.json {
		fmt.Println("\n等待授权中...（在浏览器完成授权后将自动继续）")
	}
	result, err := oauthMgr.PollDeviceToken(ctx, dc.DeviceCode, dc.Interval, dc.ExpiresIn)
	if err != nil {
		return err
	}
	return saveToken(os.Stdout, config, configPath, result, opts.json)
}

// emitDeviceAuthorization 输出设备码授权入口；showResumeHint 为 true 时附上 --device-code 恢复命令（--no-wait 用）。
func emitDeviceAuthorization(out io.Writer, dc *core.DeviceCodeResult, asJSON, showResumeHint bool) {
	if asJSON {
		printJSON(out, map[string]any{
			"event":                     "device_authorization",
			"device_code":               dc.DeviceCode,
			"user_code":                 dc.UserCode,
			"verification_uri":          dc.VerificationURI,
			"verification_uri_complete": dc.VerificationURIComplete,
			"expires_in":                dc.ExpiresIn,
			"interval":                  dc.Interval,
		})
		return
	}
	fmt.Fprintln(out, "请在浏览器中打开以下链接完成飞书授权：")
	fmt.Fprintf(out, "\n    %s\n\n", dc.VerificationURIComplete)
	if dc.UserCode != "" {
		fmt.Fprintf(out, "如页面提示输入验证码，请输入：%s\n", dc.UserCode)
		fmt.Fprintf(out, "（或手动访问 %s 并输入上述验证码）\n", dc.VerificationURI)
	}
	if showResumeHint {
		fmt.Fprintf(out, "\n设备码 device_code：%s\n", dc.DeviceCode)
		fmt.Fprintln(out, "授权完成后运行以下命令换取并保存令牌（适合 agent/CI/无头环境两段式登录）：")
		fmt.Fprintf(out, "    larkdown auth login --device-code %s\n", dc.DeviceCode)
	}
}

// saveToken 把设备码流程/恢复轮询换到的令牌写回 config 并落盘，然后输出结果。
func saveToken(out io.Writer, config *core.Config, configPath string, result *core.TokenResult, asJSON bool) error {
	now := time.Now().Unix()
	config.Feishu.UserAccessToken = result.UserAccessToken
	config.Feishu.RefreshToken = result.RefreshToken
	config.Feishu.TokenExpireTime = now + result.ExpiresIn
	if result.RefreshExpiresIn > 0 {
		config.Feishu.RefreshTokenExpireTime = now + result.RefreshExpiresIn
	}

	if err := config.WriteConfig2File(configPath); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	if asJSON {
		printJSON(out, map[string]any{
			"event":                     "authorized",
			"token_expire_time":         config.Feishu.TokenExpireTime,
			"refresh_token_expire_time": config.Feishu.RefreshTokenExpireTime,
		})
		return nil
	}
	fmt.Fprintf(out, "\n登录成功！\n")
	fmt.Fprintf(out, "Token 有效期至: %s\n", time.Unix(config.Feishu.TokenExpireTime, 0).Format("2006-01-02 15:04:05"))
	fmt.Fprintln(out, "\n现在可以使用 'larkdown download <url>' 下载文档了。")
	return nil
}

// printJSON 把 v 序列化为单行 JSON 写入 out（JSONL，便于逐行解析）。
// 关闭 HTML 转义，让 verification_uri 里的 & 等字符保持原样、URL 更干净。
func printJSON(out io.Writer, v any) {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // Encode 自带换行
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
	return cmd.Start()
}
