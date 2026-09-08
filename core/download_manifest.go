package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// downloadManifestHeader 是写入文件顶部的注释提示，解释文件用途与删除后果。
const downloadManifestHeader = `# larkdown 下载版本记录 —— 可安全删除（仅导致下次重新下载）
#
# 本文件由 larkdown download 自动生成与维护，按飞书 document_id 记录各输出目录下
# 下载产物的路径与下载时的远程版本（Wiki: obj_edit_time.revision_id；docx: revision_id），
# 以及下载时采集的正文引用（--follow 跳过未变化文档时回放）。
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
// Refs 持久化下载时 parser 采集的正文引用（未过滤自引用），skip-unchanged 时直接回放，
// 免去从 markdown 产物反解——反解永远追不上渲染格式的所有细节（歧义围栏、嵌套方括号等），
// 漏一条 ref 就会让 mirror --follow 的 prune 误删 _refs/。RefsRecorded 区分「已采集但为零」
// 与旧版记录「未采集」：--follow 遇旧记录视为过期，重新下载一次补录。
type DownloadRecord struct {
	Path         string   `yaml:"path"`                    // 下载产物绝对路径（filepath.Clean）
	Version      string   `yaml:"version"`                 // 同步点的远程版本（见 DownloadVersion）
	ContentHash  string   `yaml:"content_hash,omitempty"`  // 同步点写盘产物全文 SHA-256，用于检测本地编辑；旧版记录无此字段 → 无基线
	Conflict     bool     `yaml:"conflict,omitempty"`      // 上次 --merge 写入了冲突标记且尚未确认解决（upload 据此拒绝）
	RefsRecorded bool     `yaml:"refs_recorded,omitempty"` // 本条记录是否已采集正文引用
	Refs         []DocRef `yaml:"refs,omitempty"`          // 正文引用的 docx/wiki 文档
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

// VersionRevision 取版本标记的 revision_id 分量（无 obj_edit_time 时即原串）。
// upload 漂移守卫只比对这一分量：upload 只覆盖正文内容，内容编辑必推进 revision_id；
// obj_edit_time 多覆盖的白板编辑不会被 upload 触碰（画板按 token 复用），且画板创建后
// wiki obj_edit_time 异步滞后数秒，上传完成瞬间读到的值必偏旧，拿它比对会误报漂移。
func VersionRevision(version string) string {
	return version[strings.LastIndexByte(version, '.')+1:]
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
func (m *DownloadManifest) upsertByDir(documentID string, rec DownloadRecord) {
	if m.DocumentID != documentID {
		m.DocumentID = documentID
		m.Entries = nil
	}
	rec.Path = filepath.Clean(rec.Path)
	dir := filepath.Dir(rec.Path)
	for i := range m.Entries {
		if filepath.Dir(m.Entries[i].Path) == dir {
			m.Entries[i] = rec
			return
		}
	}
	m.Entries = append(m.Entries, rec)
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

// RecordSyncPoint 记录一次同步点——download 写盘或 upload 成功后「本地文件与远端一致
// （或已知对应关系）」的时刻：rec.Version 非空则 upsert 记录并写 base 快照（正文，
// 供 download --merge 做三方合并的公共祖先）；为空（素材下载不完整）则清除该目录的
// 记录与快照，保证下次重新下载重试。base 快照属可重建缓存，丢失仅导致无法自动合并。
func RecordSyncPoint(cp CachePaths, documentID string, rec DownloadRecord, baseBody string) error {
	m, err := ReadDownloadManifest(cp, documentID)
	if err != nil {
		return err
	}
	if m == nil {
		m = &DownloadManifest{DocumentID: documentID}
	}
	absDir := filepath.Dir(filepath.Clean(rec.Path))
	if rec.Version == "" {
		m.removeByDir(documentID, absDir)
		os.Remove(cp.DownloadBaseFile(documentID, absDir))
	} else {
		m.upsertByDir(documentID, rec)
		if err := writeFileAtomic(cp.DownloadBaseFile(documentID, absDir), []byte(baseBody)); err != nil {
			return err
		}
	}
	return WriteDownloadManifest(cp, documentID, m)
}

// ReadBaseSnapshot 读取 documentID 在 absDir 目录下的同步点 base 快照正文；
// 不存在或读取失败返回 ("", false)。
func ReadBaseSnapshot(cp CachePaths, documentID, absDir string) (string, bool) {
	data, err := os.ReadFile(cp.DownloadBaseFile(documentID, absDir))
	if err != nil {
		return "", false
	}
	return string(data), true
}
