package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/amzyang/larkdown/core"
)

var loginOpts = struct {
	port int
}{}

func handleLoginCommand() error {
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

	port := loginOpts.port
	if port == 0 {
		port = core.DefaultOAuthPort
	}

	// state 用密码学随机数防 CSRF（crypto/rand.Read 从不返回错误）
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	oauthMgr := core.NewOAuthManager(config.Feishu.AppId, config.Feishu.AppSecret, port, globalOpts.clientOpts)
	callbackServer := core.NewOAuthCallbackServer(port, state)

	if err := callbackServer.Start(); err != nil {
		return err
	}
	defer callbackServer.Shutdown(context.Background())

	authURL := oauthMgr.GetAuthURL(state)

	fmt.Println("正在打开浏览器进行飞书授权...")
	fmt.Printf("如果浏览器未自动打开，请手动访问:\n%s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("无法自动打开浏览器: %v\n", err)
	}

	fmt.Println("等待授权中...")
	code, err := callbackServer.WaitForCode(5 * time.Minute)
	if err != nil {
		return err
	}

	fmt.Println("正在获取访问令牌...")
	ctx := context.Background()
	result, err := oauthMgr.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}

	config.Feishu.UserAccessToken = result.UserAccessToken
	config.Feishu.RefreshToken = result.RefreshToken
	config.Feishu.TokenExpireTime = time.Now().Unix() + result.ExpiresIn

	if err := config.WriteConfig2File(configPath); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	fmt.Printf("\n登录成功！\n")
	fmt.Printf("Token 有效期至: %s\n", time.Unix(config.Feishu.TokenExpireTime, 0).Format("2006-01-02 15:04:05"))
	fmt.Println("\n现在可以使用 'larkdown download <url>' 下载文档了。")

	return nil
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
