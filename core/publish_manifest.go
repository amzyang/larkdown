package core

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// publishManifestName 是发布状态边车文件名。
// 目录形态放在目录根；单文件形态放在文件旁（<file>.feishu2md-publish.yaml）。
// 该文件记录发布目标对应的远端应用，供重新发布时复用应用 ID（URL 不变）。
const publishManifestName = ".feishu2md-publish.yaml"

// publishManifestHeader 是写入文件顶部的注释提示，
// 解释文件用途与删除后果，便于用户在版本库或目录中看到时理解其作用。
const publishManifestHeader = `# larkdown 发布记录 —— 请勿手动删除或重命名
#
# 本文件由 larkdown publish 自动生成与维护，用于重新发布时复用同一个远端
# 应用，从而保持访问 URL 不变。删除后下次发布会新建应用并产生新的 URL。
#
# 可随产物一并提交到版本库；若不希望提交，请将其加入 .gitignore。
`

// PublishManifest 记录某个发布目标的发布状态。
//
// 不使用 type 判别符，而是为每个发布后端使用带前缀的独立字段（如 miaoda_*）。
// 这样同一个 manifest 可并存多个后端的发布状态，新增后端只需追加自己的字段，
// 不影响既有字段，向后/向前兼容性更好。
type PublishManifest struct {
	MiaodaAppID string `yaml:"miaoda_appid,omitempty"` // 妙搭应用 ID，重新发布时复用
	MiaodaURL   string `yaml:"miaoda_url,omitempty"`   // 妙搭应用访问链接
	Name        string `yaml:"name,omitempty"`         // 应用/产物名称
	PublishedAt string `yaml:"published_at,omitempty"` // 最近一次发布时间（RFC3339）
}

// PublishManifestPath 推导发布目标对应的 manifest 路径。
//   - 目录：<dir>/.feishu2md-publish.yaml
//   - 单文件：<file>.feishu2md-publish.yaml
func PublishManifestPath(target string) (string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(target, publishManifestName), nil
	}
	return target + publishManifestName, nil
}

// ReadPublishManifest 读取 manifest；文件不存在返回 (nil, nil)。
func ReadPublishManifest(target string) (*PublishManifest, error) {
	path, err := PublishManifestPath(target)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m PublishManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WritePublishManifest 写入/刷新 manifest，并在文件顶部附带用途说明注释。
func WritePublishManifest(target string, m *PublishManifest) error {
	path, err := PublishManifestPath(target)
	if err != nil {
		return err
	}
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(publishManifestHeader), body...), 0o644)
}
