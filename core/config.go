package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Feishu FeishuConfig `json:"feishu"`
	Output OutputConfig `json:"output"`
}

type FeishuConfig struct {
	AppId           string `json:"app_id"`
	AppSecret       string `json:"app_secret"`
	UserAccessToken string `json:"user_access_token,omitempty"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	TokenExpireTime int64  `json:"token_expire_time,omitempty"`
}

// HasValidUserToken 检查 user_access_token 是否有效
func (c *FeishuConfig) HasValidUserToken() bool {
	if c.UserAccessToken == "" {
		return false
	}
	// 提前 5 分钟认为过期，触发刷新
	return time.Now().Unix() < c.TokenExpireTime-300
}

// NeedsRefresh 检查是否需要刷新 token
func (c *FeishuConfig) NeedsRefresh() bool {
	return c.RefreshToken != "" && c.UserAccessToken != "" && !c.HasValidUserToken()
}

type OutputConfig struct {
	ImageDir        string `json:"image_dir"`
	TitleAsFilename bool   `json:"title_as_filename"`
	UseHTMLTags     bool   `json:"use_html_tags"`
	SkipImgDownload bool   `json:"skip_img_download"`
	DiffStyle       string `json:"diff_style"` // chroma style name, default "monokai"
}

func NewConfig(appId, appSecret string) *Config {
	return &Config{
		Feishu: FeishuConfig{
			AppId:     appId,
			AppSecret: appSecret,
		},
		Output: OutputConfig{
			ImageDir:        "static",
			TitleAsFilename: true,
			UseHTMLTags:     false,
			SkipImgDownload: false,
		},
	}
}

func GetConfigFilePath() (string, error) {
	sp, err := DefaultStatePaths()
	if err != nil {
		return "", err
	}
	return sp.ConfigFile(), nil
}

func ReadConfigFromFile(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	config := NewConfig("", "")
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return config, nil
}

func (conf *Config) WriteConfig2File(configPath string) error {
	err := os.MkdirAll(filepath.Dir(configPath), 0o755)
	if err != nil {
		return err
	}
	file, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(configPath, file, 0o644)
	return err
}
