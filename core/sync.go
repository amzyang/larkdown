package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// SyncState 是本地产物与远端文档相对上次同步点（下载版本边车记录）的四象限状态。
type SyncState int

const (
	SyncNoBaseline    SyncState = iota // 无记录 / 旧版记录无 content_hash / 本地文件不可读：无从判定
	SyncClean                          // 本地未编辑且远端未变化
	SyncLocalEdited                    // 仅本地编辑（远端未变化，--force 强制重下时出现）
	SyncRemoteChanged                  // 仅远端变化（正常增量下载路径）
	SyncDiverged                       // 双侧都变：覆写任一侧都会丢改动，需 --theirs / --merge
)

// ContentHash 计算写盘产物全文的 SHA-256 hex，作为同步点的本地内容指纹。
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// ClassifySync 按「本地是否被编辑（内容 hash）× 远端是否变化（版本标记）」判定同步状态。
// localContent 为记录路径 rec.Path 的当前文件内容，nil 表示不可读。
func ClassifySync(rec *DownloadRecord, localContent []byte, remoteVersion string) SyncState {
	if rec == nil || rec.ContentHash == "" || localContent == nil {
		return SyncNoBaseline
	}
	localEdited := ContentHash(localContent) != rec.ContentHash
	remoteChanged := remoteVersion != rec.Version
	switch {
	case localEdited && remoteChanged:
		return SyncDiverged
	case localEdited:
		return SyncLocalEdited
	case remoteChanged:
		return SyncRemoteChanged
	default:
		return SyncClean
	}
}

// --merge 写入的冲突标记（git 风格 7 字符 + 固定标签）。检测按整行精确匹配自产标签，
// 代码块里的 "<<<<<<< HEAD" 等示例不会误伤。
const (
	conflictMarkerLocal  = "<<<<<<< local"
	conflictMarkerSep    = "======="
	conflictMarkerRemote = ">>>>>>> remote"
)

// HasUnresolvedConflictMarkers 检测正文是否仍含 larkdown --merge 写入的冲突标记行。
func HasUnresolvedConflictMarkers(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == conflictMarkerLocal || line == conflictMarkerRemote {
			return true
		}
	}
	return false
}

// DivergedError 表示 download 将覆写一个自上次同步点后被本地编辑过的文件。
type DivergedError struct {
	Path          string
	RemoteChanged bool // true=远端也变化（分叉）；false=仅本地编辑（--force 重下时出现）
}

func (e *DivergedError) Error() string {
	if e.RemoteChanged {
		return fmt.Sprintf(
			"本地文件与远端文档自上次同步后均已变化（分叉），已拒绝覆写以免丢失本地编辑: %s\n"+
				"  处理方式任选其一：\n"+
				"    1) larkdown download --merge <url>   三方合并本地与远端改动（冲突时写入冲突标记）\n"+
				"    2) larkdown download --theirs <url>  以远端为准覆写本地（放弃本地编辑）\n"+
				"    3) larkdown diff <file>              先查看双侧差异再决定",
			e.Path,
		)
	}
	return fmt.Sprintf(
		"本地文件含未上传的编辑而远端未变化，已拒绝覆写: %s\n"+
			"  处理方式任选其一：\n"+
			"    1) larkdown upload <file>             把本地改动推送到飞书\n"+
			"    2) larkdown download --theirs <url>   以远端为准覆写本地（放弃本地编辑）",
		e.Path,
	)
}

// MergeConflictError 表示 --merge 已完成但存在冲突，冲突标记已写入本地文件待人工解决。
type MergeConflictError struct{ Path string }

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf(
		"三方合并存在冲突，已把冲突标记写入: %s\n"+
			"  请编辑该文件解决 <<<<<<< local / >>>>>>> remote 标记，然后 larkdown upload 推送合并结果；\n"+
			"  若要放弃合并、以远端为准重下：larkdown download --force --theirs <url>。",
		e.Path,
	)
}

// SyncDriftError 表示 upload 时发现远端文档自上次同步点后已变化，直接上传会回滚远端改动。
type SyncDriftError struct {
	Source          string
	RecordedVersion string
	RemoteVersion   string
}

func (e *SyncDriftError) Error() string {
	return fmt.Sprintf(
		"远端文档自上次同步后已变化（可能协作者编辑或新增评论），已拒绝上传以免回滚远端改动: %s\n"+
			"  处理方式任选其一：\n"+
			"    1) larkdown download --merge <url>  先合并远端改动，再重新 upload\n"+
			"    2) larkdown upload --ours <file>    以本地为准强制上传（远端未同步的改动将被覆盖）\n"+
			"    3) larkdown diff <file>             先查看双侧差异再决定",
		e.Source,
	)
}

// ConflictPendingError 表示上次 --merge 写入的冲突标记尚未解决，拒绝上传半成品。
type ConflictPendingError struct {
	Path         string
	ManifestFile string // 边车记录路径，供正文确属字面标记的极端场景手动解除
}

func (e *ConflictPendingError) Error() string {
	msg := fmt.Sprintf(
		"文件仍含未解决的合并冲突标记（<<<<<<< local / >>>>>>> remote），已拒绝上传: %s\n"+
			"  请先编辑文件解决冲突；标记消失后 upload 会自动恢复。",
		e.Path,
	)
	if e.ManifestFile != "" {
		msg += fmt.Sprintf("\n  若这些标记确属正文内容：删除边车记录 %s 后重试（可安全删除，仅导致下次重新下载）。", e.ManifestFile)
	}
	return msg
}
