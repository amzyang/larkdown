package core

import (
	"os"

	"gopkg.in/yaml.v3"
)

// publishManifestName 是旧版发布边车文件名。中心化后发布记录改存中心 store，
// 此常量仅保留用于打包时过滤（防止历史残留的同名文件被打进妙搭产物 tar）。
const publishManifestName = ".feishu2md-publish.yaml"

// publishManifestHeader 是写入文件顶部的注释提示，解释文件用途与删除后果。
const publishManifestHeader = `# larkdown 发布记录 —— 请勿手动编辑或删除
#
# 本文件由 larkdown publish 自动生成与维护，按发布目标路径记录对应的远端应用，
# 供重新发布时复用同一个应用、保持访问 URL 不变。删除后下次发布会新建应用并产生新 URL。
#
# 存放于用户配置目录，按 target 路径一目标一文件，不随工作目录或版本库移动。
`

// PublishManifest 记录某个发布目标的发布状态。
//
// 不使用 type 判别符，而是为每个发布后端使用带前缀的独立字段（如 miaoda_*）。
// 这样同一个 manifest 可并存多个后端的发布状态，新增后端只需追加自己的字段，
// 不影响既有字段，向后/向前兼容性更好。
type PublishManifest struct {
	TargetPath  string `yaml:"target_path,omitempty"`  // 发布目标绝对路径（key 是其 sha256，此处冗余存原值供人读/排查）
	MiaodaAppID string `yaml:"miaoda_appid,omitempty"` // 妙搭应用 ID，重新发布时复用
	MiaodaURL   string `yaml:"miaoda_url,omitempty"`   // 妙搭应用访问链接
	Name        string `yaml:"name,omitempty"`         // 应用/产物名称
	PublishedAt string `yaml:"published_at,omitempty"` // 最近一次发布时间（RFC3339）
}

// ReadPublishManifest 按 target 绝对路径的 sha256 读中心 store 的发布记录；不存在返回 (nil, nil)。
// absTarget 须为绝对路径（filepath.Abs 依赖 cwd，留在调用方外壳层处理）。
func ReadPublishManifest(sp StatePaths, absTarget string) (*PublishManifest, error) {
	data, err := os.ReadFile(sp.PublishManifestFile(absTarget))
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

// WritePublishManifest 按 target 绝对路径的 sha256 写中心 store 的发布记录，文件顶部附用途说明注释。
// 原子写（writeFileAtomic）。TargetPath 字段始终冗余写入原始路径，供人读/排查。
func WritePublishManifest(sp StatePaths, absTarget string, m *PublishManifest) error {
	if m.TargetPath == "" {
		m.TargetPath = absTarget
	}
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(sp.PublishManifestFile(absTarget), append([]byte(publishManifestHeader), body...))
}
