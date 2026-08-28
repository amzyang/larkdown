package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifySync(t *testing.T) {
	base := []byte("# 标题\n\n正文\n")
	edited := []byte("# 标题\n\n改过的正文\n")
	rec := &DownloadRecord{Path: "/out/a.md", Version: "v1", ContentHash: ContentHash(base)}

	cases := []struct {
		name          string
		rec           *DownloadRecord
		local         []byte
		remoteVersion string
		want          SyncState
	}{
		{"无记录", nil, base, "v1", SyncNoBaseline},
		{"旧版记录无hash", &DownloadRecord{Path: "/out/a.md", Version: "v1"}, base, "v2", SyncNoBaseline},
		{"本地文件不可读", rec, nil, "v2", SyncNoBaseline},
		{"双侧未变", rec, base, "v1", SyncClean},
		{"仅本地编辑", rec, edited, "v1", SyncLocalEdited},
		{"仅远端变化", rec, base, "v2", SyncRemoteChanged},
		{"双侧都变_分叉", rec, edited, "v2", SyncDiverged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifySync(c.rec, c.local, c.remoteVersion); got != c.want {
				t.Errorf("ClassifySync = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHasUnresolvedConflictMarkers(t *testing.T) {
	conflicted := "前文\n<<<<<<< local\n本地行\n=======\n远端行\n>>>>>>> remote\n后文\n"
	if !HasUnresolvedConflictMarkers(conflicted) {
		t.Error("含自产冲突标记应返回 true")
	}
	// 代码块里的 git 冲突标记示例（<<<<<<< HEAD）不是自产标记，不误伤
	sample := "```\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> feature\n```\n"
	if HasUnresolvedConflictMarkers(sample) {
		t.Error("非自产标记（HEAD/feature 标签）不应误伤")
	}
	if HasUnresolvedConflictMarkers("普通正文\n") {
		t.Error("普通正文不应命中")
	}
}

func TestRecordSyncPointWithHashAndBase(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	path := filepath.Join("/", "out", "标题.md")
	content := []byte("# 标题\n\n正文\n<!-- source: https://x.feishu.cn/wiki/tok -->\n")

	rec := DownloadRecord{
		Path:         path,
		Version:      "v1",
		ContentHash:  ContentHash(content),
		RefsRecorded: true,
	}
	if err := RecordSyncPoint(cp, docID, rec, "# 标题\n\n正文\n"); err != nil {
		t.Fatal(err)
	}

	got := LookupDownloadRecord(cp, docID, filepath.Dir(path))
	if got == nil || got.ContentHash != rec.ContentHash || got.Conflict {
		t.Fatalf("同步点记录不符: %+v", got)
	}
	baseBody, ok := ReadBaseSnapshot(cp, docID, filepath.Dir(path))
	if !ok || baseBody != "# 标题\n\n正文\n" {
		t.Fatalf("base 快照不符: ok=%v body=%q", ok, baseBody)
	}

	// conflict 标志可持久化并随下一次同步点清除
	rec.Conflict = true
	if err := RecordSyncPoint(cp, docID, rec, "base2"); err != nil {
		t.Fatal(err)
	}
	if got := LookupDownloadRecord(cp, docID, filepath.Dir(path)); got == nil || !got.Conflict {
		t.Fatalf("conflict 标志未持久化: %+v", got)
	}

	// version 为空 → 清除记录与 base 快照
	if err := RecordSyncPoint(cp, docID, DownloadRecord{Path: path}, ""); err != nil {
		t.Fatal(err)
	}
	if got := LookupDownloadRecord(cp, docID, filepath.Dir(path)); got != nil {
		t.Errorf("清除后应无记录: %+v", got)
	}
	if _, ok := ReadBaseSnapshot(cp, docID, filepath.Dir(path)); ok {
		t.Error("清除后 base 快照应删除")
	}
}

func TestReadBaseSnapshotMissing(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	if _, ok := ReadBaseSnapshot(cp, "docNone", "/out"); ok {
		t.Error("无快照应返回 ok=false")
	}
}

func TestSyncErrorsAreActionable(t *testing.T) {
	for name, msg := range map[string]string{
		"DivergedError_分叉":   (&DivergedError{Path: "/out/a.md", RemoteChanged: true}).Error(),
		"DivergedError_仅本地":  (&DivergedError{Path: "/out/a.md"}).Error(),
		"MergeConflictError": (&MergeConflictError{Path: "/out/a.md"}).Error(),
		"SyncDriftError":     (&SyncDriftError{Source: "https://x.feishu.cn/wiki/tok"}).Error(),
		"ConflictPendingErr": (&ConflictPendingError{Path: "/out/a.md"}).Error(),
	} {
		if strings.TrimSpace(msg) == "" {
			t.Errorf("%s 错误消息为空", name)
		}
	}
}
