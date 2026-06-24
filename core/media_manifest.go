package core

import (
	"os"

	"gopkg.in/yaml.v3"
)

// mediaManifestHeader 是写入文件顶部的注释提示，解释文件用途与删除后果。
const mediaManifestHeader = `# larkdown 媒体映射记录 —— 请勿手动编辑或删除
#
# 本文件由 larkdown upload 自动生成与维护，按飞书 document_id 记录本地图片/文件的
# markdown 引用路径到飞书素材 token 的映射，并附上传时的内容 md5。增量更新据此把
# 远程 token 写回本地块：内容未变则跳过重传、token 不抖动；内容已变则原地 replace
# （保 block_id、不破坏评论锚点）。删除后下次上传将重新上传素材并重建映射。
#
# 存放于用户配置目录，按 document_id 一文档一文件，不随工作目录或版本库移动。
`

// MediaManifest 记录某飞书文档下「markdown 引用路径 → 素材 token + 内容 md5」的映射，
// 以 document_id 为 scope。图片与文件共用一份（IsFile 区分），与白板映射（board_manifest.go）同构。
type MediaManifest struct {
	DocumentID string         `yaml:"document_id"`
	Entries    []MediaMapping `yaml:"entries"`
}

// MediaMapping 单条媒体映射。
type MediaMapping struct {
	Path   string `yaml:"path"`    // markdown 引用路径，归一化存（normalizeMediaPath：path.Clean，远程 URL 原样）
	Token  string `yaml:"token"`   // 飞书图片/文件素材 token
	MD5    string `yaml:"md5"`     // 上传字节的 md5，作下次变更基准
	IsFile bool   `yaml:"is_file"` // true=File 附件（含视频卡片），false=Image
}

// ReadMediaManifest 按 document_id 读取中心 store 的媒体映射；文件不存在返回 (nil, nil)。
func ReadMediaManifest(sp StatePaths, documentID string) (*MediaManifest, error) {
	data, err := os.ReadFile(sp.MediaManifestFile(documentID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m MediaManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteMediaManifest 按 document_id 写入/刷新中心 store 的媒体映射，文件顶部附用途说明注释。
// 原子写（writeFileAtomic）避免并发或中断产生半截文件。
func WriteMediaManifest(sp StatePaths, documentID string, m *MediaManifest) error {
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(sp.MediaManifestFile(documentID), append([]byte(mediaManifestHeader), body...))
}

// lookupUnusedToken 返回指定 document 下 path 对应、仍存在于远程（remoteTokens）且尚未被本次
// 复用（used）的素材 token 及其基准 md5；无可用条目返回 ("", "")。
// 仿 board lookupUnusedToken：使同一路径被多次引用时各取独立 token，避免签名碰撞。
func (m *MediaManifest) lookupUnusedToken(documentID, path string, remoteTokens, used map[string]bool) (token, md5 string) {
	if m == nil || m.DocumentID != documentID {
		return "", ""
	}
	for _, e := range m.Entries {
		if normalizeMediaPath(e.Path) == normalizeMediaPath(path) && remoteTokens[e.Token] && !used[e.Token] {
			return e.Token, e.MD5
		}
	}
	return "", ""
}

// upsert 插入或更新一条映射（按 token 去重）。document 变更时清空旧映射。
// 按 token（每个素材唯一）而非 path 去重：使多个引用同一路径的块各自保留独立 token，
// 避免折叠成一条而令重复引用每次都重传。
func (m *MediaManifest) upsert(documentID, path, token, md5 string, isFile bool) {
	if m.DocumentID != documentID {
		m.DocumentID = documentID
		m.Entries = nil
	}
	for i := range m.Entries {
		if m.Entries[i].Token == token {
			m.Entries[i].Path = path
			m.Entries[i].MD5 = md5
			m.Entries[i].IsFile = isFile
			return
		}
	}
	m.Entries = append(m.Entries, MediaMapping{Path: path, Token: token, MD5: md5, IsFile: isFile})
}

// applyMediaPathMappings 对无 token 的本地图片/文件块，用 manifest 映射按 markdown 路径写回 token
// ——仅当 token 仍存在于远程素材集合（remoteTokens）。写回后该块签名变 media:<token>、与远程 Equal，
// 增量更新进入「Equal 内容检测」据基准 md5 决定跳过 / 原地 replace。
// 纯函数（无 IO）。返回写回数量与 token→基准 md5（供 Equal 检测比对，token 唯一不冲突）。
// 远程 URL 路径（无本地编辑语义）跳过；已有 token 的块（文件名前缀机制已解析）不覆盖。
func applyMediaPathMappings(localResult *ConvertResult, documentID string, manifest *MediaManifest, remoteTokens map[string]bool) (int, map[string]string) {
	baselineByToken := map[string]string{}
	if manifest == nil || localResult == nil {
		return 0, baselineByToken
	}
	used := map[string]bool{}
	n := 0
	for i, idx := range localResult.ImageIndices {
		b := localResult.TopBlocks[idx]
		if b == nil || b.Image == nil || b.Image.Token != "" || isRemoteURL(localResult.ImagePaths[i]) {
			continue
		}
		if token, md5 := manifest.lookupUnusedToken(documentID, localResult.ImagePaths[i], remoteTokens, used); token != "" {
			b.Image.Token = token
			used[token] = true
			baselineByToken[token] = md5
			n++
		}
	}
	for i, idx := range localResult.FileIndices {
		b := localResult.TopBlocks[idx]
		if b == nil || b.File == nil || b.File.Token != "" || isRemoteURL(localResult.FilePaths[i]) {
			continue
		}
		if token, md5 := manifest.lookupUnusedToken(documentID, localResult.FilePaths[i], remoteTokens, used); token != "" {
			b.File.Token = token
			used[token] = true
			baselineByToken[token] = md5
			n++
		}
	}
	return n, baselineByToken
}
