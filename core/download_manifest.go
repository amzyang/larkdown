package core

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// downloadManifestHeader 是写入文件顶部的注释提示，解释文件用途与删除后果。
const downloadManifestHeader = `# larkdown 下载版本记录 —— 可安全删除（仅导致下次重新下载）
#
# 本文件由 larkdown download 自动生成与维护，按飞书 document_id 记录各输出目录下
# 下载产物的路径与下载时的远程版本（Wiki: obj_edit_time.revision_id；docx: revision_id）。
# 重复下载时本地记录与远程版本一致 → 跳过拉取块与素材；download --force 忽略本记录。
#
# 存放于用户缓存目录，属可重建缓存，不随工作目录或版本库移动。
`

// DownloadManifest 记录某飞书文档各输出目录下的下载产物与版本标记，以 document_id 为 scope。
// 与 board/media 映射（StatePaths，不可重建）不同，本记录可重建，落 CachePaths。
type DownloadManifest struct {
	DocumentID string           `yaml:"document_id"`
	Entries    []DownloadRecord `yaml:"entries"`
}

// DownloadRecord 单条下载记录。同一文档在同一输出目录下仅一条（文件名可随标题变化）。
type DownloadRecord struct {
	Path    string `yaml:"path"`    // 下载产物绝对路径（filepath.Clean）
	Version string `yaml:"version"` // 下载时的远程版本（见 DownloadVersion）
}

// DownloadVersion 组合文档下载版本标记。revision_id 随内容编辑与评论变化，
// 但白板编辑不更新它；Wiki 节点的 obj_edit_time 随白板编辑变化，故两者拼接，
// 任一变化都触发重新下载。非 Wiki 文档无 obj_edit_time，仅用 revision_id
// （已知限制：纯白板编辑不会被感知，可用 download --force 强制刷新）。
func DownloadVersion(objEditTime string, revisionID int64) string {
	rev := strconv.FormatInt(revisionID, 10)
	if objEditTime == "" {
		return rev
	}
	return objEditTime + "." + rev
}

// ReadDownloadManifest 按 document_id 读取下载版本记录；文件不存在返回 (nil, nil)。
func ReadDownloadManifest(cp CachePaths, documentID string) (*DownloadManifest, error) {
	data, err := os.ReadFile(cp.DownloadManifestFile(documentID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m DownloadManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteDownloadManifest 按 document_id 写入/刷新下载版本记录，文件顶部附用途说明注释。
// 原子写（writeFileAtomic）避免并发或中断产生半截文件。
func WriteDownloadManifest(cp CachePaths, documentID string, m *DownloadManifest) error {
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(cp.DownloadManifestFile(documentID), append([]byte(downloadManifestHeader), body...))
}

// lookupByDir 返回 absDir 目录下的下载记录；无则返回 nil。
func (m *DownloadManifest) lookupByDir(documentID, absDir string) *DownloadRecord {
	if m == nil || m.DocumentID != documentID {
		return nil
	}
	absDir = filepath.Clean(absDir)
	for i := range m.Entries {
		if filepath.Dir(m.Entries[i].Path) == absDir {
			return &m.Entries[i]
		}
	}
	return nil
}

// upsertByDir 插入或更新一条记录，按产物所在目录去重（标题变更导致的重命名会覆盖旧条目）。
// document 变更时清空旧记录。
func (m *DownloadManifest) upsertByDir(documentID, absPath, version string) {
	if m.DocumentID != documentID {
		m.DocumentID = documentID
		m.Entries = nil
	}
	absPath = filepath.Clean(absPath)
	dir := filepath.Dir(absPath)
	for i := range m.Entries {
		if filepath.Dir(m.Entries[i].Path) == dir {
			m.Entries[i] = DownloadRecord{Path: absPath, Version: version}
			return
		}
	}
	m.Entries = append(m.Entries, DownloadRecord{Path: absPath, Version: version})
}

// removeByDir 删除 absDir 目录下的记录（如有）。
func (m *DownloadManifest) removeByDir(documentID, absDir string) {
	if m == nil || m.DocumentID != documentID {
		return
	}
	absDir = filepath.Clean(absDir)
	for i := range m.Entries {
		if filepath.Dir(m.Entries[i].Path) == absDir {
			m.Entries = append(m.Entries[:i], m.Entries[i+1:]...)
			return
		}
	}
}

// LookupDownloadRecord 查询 documentID 在 absDir 目录下的下载记录；无记录或读取失败返回 nil。
func LookupDownloadRecord(cp CachePaths, documentID, absDir string) *DownloadRecord {
	m, err := ReadDownloadManifest(cp, documentID)
	if err != nil || m == nil {
		return nil
	}
	return m.lookupByDir(documentID, absDir)
}

// RecordDownloadVersion 记录一次下载：version 非空则 upsert；为空（素材下载不完整）则
// 清除该目录的记录，保证下次重新下载重试。
func RecordDownloadVersion(cp CachePaths, documentID, absPath, version string) error {
	m, err := ReadDownloadManifest(cp, documentID)
	if err != nil {
		return err
	}
	if m == nil {
		m = &DownloadManifest{DocumentID: documentID}
	}
	if version == "" {
		m.removeByDir(documentID, filepath.Dir(absPath))
	} else {
		m.upsertByDir(documentID, absPath, version)
	}
	return WriteDownloadManifest(cp, documentID, m)
}
