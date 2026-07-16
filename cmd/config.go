package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/spf13/cobra"
)

type ConfigOpts struct {
	appId     string
	appSecret string
}

var configOpts = ConfigOpts{}

// configKeys 是 config get/set 支持的键（secret 类 get 时脱敏展示）。
var configKeys = []string{"app_id", "app_secret", "image_dir", "title_as_filename", "use_html_tags", "skip_img_download", "diff_style"}

// resolveConfigPath 决定配置文件路径：--config flag > LARKDOWN_CONFIG env > 默认路径。
func resolveConfigPath() (string, error) {
	if globalOpts.configPath != "" {
		return globalOpts.configPath, nil
	}
	if p := os.Getenv("LARKDOWN_CONFIG"); p != "" {
		return p, nil
	}
	return core.GetConfigFilePath()
}

// loadConfigFile 读纯文件配置（写回场景用，不做 env 凭证覆盖）。
func loadConfigFile() (*core.Config, string, error) {
	path, err := resolveConfigPath()
	if err != nil {
		return nil, "", err
	}
	config, err := core.ReadConfigFromFile(path)
	if err != nil {
		return nil, path, err
	}
	return config, path, nil
}

// loadConfig 读配置供 API 调用：--as bot 时应用 LARKDOWN_APP_ID / LARKDOWN_APP_SECRET
// 环境变量凭证覆盖（仅内存，绝不写盘）。user 身份不覆盖——token 刷新会把配置写回文件，
// env 凭证混入会污染落盘配置，且 refresh_token 与原应用绑定、换凭证必然刷新失败。
// 纯 CI 场景（--as bot + 两个 env 齐备）允许配置文件不存在。
func loadConfig() (*core.Config, string, error) {
	envAppID, envAppSecret := os.Getenv("LARKDOWN_APP_ID"), os.Getenv("LARKDOWN_APP_SECRET")
	config, path, err := loadConfigFile()
	if err != nil {
		if os.IsNotExist(err) && globalOpts.as == identityBot && envAppID != "" && envAppSecret != "" {
			config = core.NewConfig("", "")
		} else {
			return nil, path, err
		}
	}
	if globalOpts.as == identityBot {
		if envAppID != "" {
			config.Feishu.AppId = envAppID
		}
		if envAppSecret != "" {
			config.Feishu.AppSecret = envAppSecret
		}
	}
	return config, path, nil
}

// configGetValue 读取单个配置键的展示值（secret 类脱敏）。
func configGetValue(config *core.Config, key string) (string, error) {
	switch key {
	case "app_id":
		return config.Feishu.AppId, nil
	case "app_secret":
		return maskSecret(config.Feishu.AppSecret), nil
	case "image_dir":
		return config.Output.ImageDir, nil
	case "title_as_filename":
		return strconv.FormatBool(config.Output.TitleAsFilename), nil
	case "use_html_tags":
		return strconv.FormatBool(config.Output.UseHTMLTags), nil
	case "skip_img_download":
		return strconv.FormatBool(config.Output.SkipImgDownload), nil
	case "diff_style":
		return config.Output.DiffStyle, nil
	default:
		return "", exitWithMessage(fmt.Sprintf("未知配置键 %q（可选: %s）", key, strings.Join(configKeys, ", ")), 1)
	}
}

// configSetValue 校验并写入单个配置键（布尔键校验 true/false，diff_style 校验 chroma 样式名）。
func configSetValue(config *core.Config, key, value string) error {
	parseBool := func() (bool, error) {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return false, exitWithMessage(fmt.Sprintf("%s 需要布尔值 true/false（收到 %q）", key, value), 1)
		}
		return b, nil
	}
	switch key {
	case "app_id":
		config.Feishu.AppId = value
	case "app_secret":
		config.Feishu.AppSecret = value
	case "image_dir":
		config.Output.ImageDir = value
	case "title_as_filename":
		b, err := parseBool()
		if err != nil {
			return err
		}
		config.Output.TitleAsFilename = b
	case "use_html_tags":
		b, err := parseBool()
		if err != nil {
			return err
		}
		config.Output.UseHTMLTags = b
	case "skip_img_download":
		b, err := parseBool()
		if err != nil {
			return err
		}
		config.Output.SkipImgDownload = b
	case "diff_style":
		if !slices.Contains(styles.Names(), value) {
			return exitWithMessage(fmt.Sprintf("diff_style %q 不是有效的 chroma 样式名（如 monokai / github / dracula）", value), 1)
		}
		config.Output.DiffStyle = value
	default:
		return exitWithMessage(fmt.Sprintf("未知配置键 %q（可选: %s）", key, strings.Join(configKeys, ", ")), 1)
	}
	return nil
}

// configKeyCompletion 补全 config get/set 的键位置参数。
func configKeyCompletion(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return configKeys, cobra.ShellCompDirectiveNoFileComp
}

func newConfigGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "get [key]",
		Short:             "Print one config value, or the whole (masked) config without a key",
		ValidArgsFunction: configKeyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitWithMessage("config get 只接受一个键参数", 1)
			}
			config, path, err := loadConfigFile()
			if err != nil {
				if os.IsNotExist(err) {
					return exitWithMessage("配置文件不存在: "+path+"；请先运行 larkdown config init 或 larkdown config --appId=xxx --appSecret=xxx", 1)
				}
				return err
			}
			if len(args) == 0 {
				fmt.Println(utils.PrettyPrint(maskConfigForDisplay(config)))
				return nil
			}
			value, err := configGetValue(config, args[0])
			if err != nil {
				return err
			}
			fmt.Println(value)
			return nil
		},
	}
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one config value (output options, app credentials)",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				return configKeys, cobra.ShellCompDirectiveNoFileComp
			case 1:
				switch args[0] {
				case "title_as_filename", "use_html_tags", "skip_img_download":
					return []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
				case "diff_style":
					return styles.Names(), cobra.ShellCompDirectiveNoFileComp
				}
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return exitWithMessage("用法: larkdown config set <key> <value>", 1)
			}
			path, err := resolveConfigPath()
			if err != nil {
				return err
			}
			config, err := core.ReadConfigFromFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				config = core.NewConfig("", "")
			}
			if err := configSetValue(config, args[0], args[1]); err != nil {
				return err
			}
			if err := config.WriteConfig2File(path); err != nil {
				return err
			}
			value, _ := configGetValue(config, args[0])
			fmt.Printf("%s = %s\n", args[0], value)
			return nil
		},
	}
}

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
	configPath, err := resolveConfigPath()
	if err != nil {
		return err
	}

	fmt.Println("配置文件: " + configPath)
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
