package main

import (
	"fmt"
	"os"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
)

type ConfigOpts struct {
	appId     string
	appSecret string
}

var configOpts = ConfigOpts{}

// maskSecret 脱敏敏感字段：长度 > 8 只留末 4 位，其余全遮，空值保持空
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// maskConfigForDisplay 返回脱敏后的值拷贝，仅用于展示，绝不写回文件
func maskConfigForDisplay(config *core.Config) core.Config {
	masked := *config
	masked.Feishu.AppSecret = maskSecret(masked.Feishu.AppSecret)
	masked.Feishu.UserAccessToken = maskSecret(masked.Feishu.UserAccessToken)
	masked.Feishu.RefreshToken = maskSecret(masked.Feishu.RefreshToken)
	return masked
}

func handleConfigCommand() error {
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}

	fmt.Println("Configuration file on: " + configPath)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config := core.NewConfig(configOpts.appId, configOpts.appSecret)
		if err = config.WriteConfig2File(configPath); err != nil {
			return err
		}
		fmt.Println(utils.PrettyPrint(maskConfigForDisplay(config)))
	} else {
		config, err := core.ReadConfigFromFile(configPath)
		if err != nil {
			return err
		}
		if configOpts.appId != "" {
			config.Feishu.AppId = configOpts.appId
		}
		if configOpts.appSecret != "" {
			config.Feishu.AppSecret = configOpts.appSecret
		}
		if configOpts.appId != "" || configOpts.appSecret != "" {
			if err = config.WriteConfig2File(configPath); err != nil {
				return err
			}
		}
		fmt.Println(utils.PrettyPrint(maskConfigForDisplay(config)))
	}
	return nil
}
