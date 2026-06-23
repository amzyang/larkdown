package core_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkdown/core"
)

func getIdAndSecretFromEnv(t *testing.T) (string, string) {
	appID := ""
	appSecret := ""

	configPath, err := core.GetConfigFilePath()
	if err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		appID = os.Getenv("FEISHU_APP_ID")
		appSecret = os.Getenv("FEISHU_APP_SECRET")
	} else {
		config, err := core.ReadConfigFromFile(configPath)
		if err != nil {
			t.Error(err)
		}
		appID = config.Feishu.AppId
		appSecret = config.Feishu.AppSecret
	}

	return appID, appSecret
}

func TestNewClient(t *testing.T) {
	appID, appSecret := getIdAndSecretFromEnv(t)
	c := core.NewClient(appID, appSecret, nil)
	if c == nil {
		t.Errorf("Error creating DocClient")
	}
}

func TestDownloadImage(t *testing.T) {
	appID, appSecret := getIdAndSecretFromEnv(t)
	c := core.NewClient(appID, appSecret, nil)
	imgToken := "boxcnA1QKPanfMhLxzF1eMhoArM"
	filename, err := c.DownloadImage(
		context.Background(),
		imgToken,
		"static",
	)
	if err != nil {
		t.Error(err)
	}
	// 期望格式: {token}_{原始文件名}
	if !strings.HasPrefix(filename, "static/"+imgToken+"_") {
		fmt.Println(filename)
		t.Errorf("Error: not expected filename format")
	}
	if err := os.RemoveAll("static"); err != nil {
		t.Errorf("Error: failed to clean up the folder")
	}
}

func TestGetDocxContent(t *testing.T) {
	appID, appSecret := getIdAndSecretFromEnv(t)
	c := core.NewClient(appID, appSecret, nil)
	docx, blocks, err := c.GetDocxContent(
		context.Background(),
		"doxcnXhd93zqoLnmVPGIPTy7AFe",
	)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(docx.Title)
	if docx.Title == "" {
		t.Errorf("Error: parsed title is empty")
	}
	fmt.Printf("number of blocks: %d\n", len(blocks))
	if len(blocks) == 0 {
		t.Errorf("Error: parsed blocks are empty")
	}
}

func TestGetWikiNodeInfo(t *testing.T) {
	appID, appSecret := getIdAndSecretFromEnv(t)
	c := core.NewClient(appID, appSecret, nil)
	const token = "wikcnLgRX9AMtvaB5x1cl57Yuah"
	node, err := c.GetWikiNodeInfo(context.Background(), token)
	if err != nil {
		t.Error(err)
	}
	if node.ObjType != "docx" {
		t.Errorf("Error: node type incorrect")
	}
}

func TestGetDriveFolderFileList(t *testing.T) {
	appID, appSecret := getIdAndSecretFromEnv(t)
	c := core.NewClient(appID, appSecret, nil)
	folderToken := "G15mfSfIHlyquudfhq5cg9kdnjg"
	files, err := c.GetDriveFolderFileList(
		context.Background(), nil, &folderToken)
	if err != nil {
		t.Error(err)
	}
	if len(files) == 0 {
		t.Errorf("Error: no files found")
	}
}

func getUserTokenFromConfig(t *testing.T) (string, string, string) {
	t.Helper()
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		t.Skip("config file path not available:", err)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skip("config file not found, skipping user token test")
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		t.Skip("failed to read config:", err)
	}
	if config.Feishu.UserAccessToken == "" {
		t.Skip("user_access_token not configured, run 'larkdown login' first")
	}
	if config.Feishu.NeedsRefresh() {
		oauth := core.NewOAuthManager(config.Feishu.AppId, config.Feishu.AppSecret, core.DefaultOAuthPort, nil)
		result, err := oauth.RefreshUserToken(context.Background(), config.Feishu.RefreshToken)
		if err != nil {
			t.Skip("failed to refresh token:", err)
		}
		config.Feishu.UserAccessToken = result.UserAccessToken
		config.Feishu.RefreshToken = result.RefreshToken
		config.Feishu.TokenExpireTime = time.Now().Unix() + result.ExpiresIn
		if err := config.WriteConfig2File(configPath); err != nil {
			t.Logf("warning: failed to save refreshed token: %v", err)
		}
	}
	return config.Feishu.AppId, config.Feishu.AppSecret, config.Feishu.UserAccessToken
}

func TestGetWikiNodeList(t *testing.T) {
	appID, appSecret, userToken := getUserTokenFromConfig(t)
	c := core.NewClientWithUserToken(appID, appSecret, userToken, nil)
	wikiToken := "7611058517562133435"
	nodes, err := c.GetWikiNodeList(context.Background(), wikiToken, nil)
	if err != nil {
		t.Error(err)
	}
	if len(nodes) == 0 {
		t.Errorf("Error: no nodes found")
	}
}
