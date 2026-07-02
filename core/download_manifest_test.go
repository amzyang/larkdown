package core

import (
	"path/filepath"
	"testing"
)

func TestDownloadVersion(t *testing.T) {
	if got := DownloadVersion("", 42); got != "42" {
		t.Errorf("非 Wiki 文档应仅用 revision_id，实际: %q", got)
	}
	if got := DownloadVersion("1700000000", 42); got != "1700000000.42" {
		t.Errorf("Wiki 文档应拼接 obj_edit_time 与 revision_id，实际: %q", got)
	}
}

func TestDownloadManifestRoundTrip(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	path := filepath.Join("/", "out", "标题.md")

	// 未写入时无记录
	if rec := LookupDownloadRecord(cp, docID, filepath.Dir(path)); rec != nil {
		t.Fatalf("未写入应返回 nil，实际: %+v", rec)
	}

	// 写入后可查回
	if err := RecordDownloadVersion(cp, docID, path, "1700000000.42"); err != nil {
		t.Fatal(err)
	}
	rec := LookupDownloadRecord(cp, docID, filepath.Dir(path))
	if rec == nil || rec.Path != path || rec.Version != "1700000000.42" {
		t.Fatalf("记录不符: %+v", rec)
	}

	// 其它目录查不到
	if rec := LookupDownloadRecord(cp, docID, "/elsewhere"); rec != nil {
		t.Errorf("其它目录应返回 nil，实际: %+v", rec)
	}
	// 其它文档查不到
	if rec := LookupDownloadRecord(cp, "docOther", filepath.Dir(path)); rec != nil {
		t.Errorf("其它文档应返回 nil，实际: %+v", rec)
	}
}

func TestDownloadManifestUpsertByDir(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	dir := filepath.Join("/", "out")

	// 同目录重命名（标题变更）应覆盖旧条目而非新增
	if err := RecordDownloadVersion(cp, docID, filepath.Join(dir, "旧标题.md"), "v1"); err != nil {
		t.Fatal(err)
	}
	if err := RecordDownloadVersion(cp, docID, filepath.Join(dir, "新标题.md"), "v2"); err != nil {
		t.Fatal(err)
	}
	m, err := ReadDownloadManifest(cp, docID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Path != filepath.Join(dir, "新标题.md") || m.Entries[0].Version != "v2" {
		t.Fatalf("同目录应去重覆盖: %+v", m.Entries)
	}

	// 不同目录各自独立
	if err := RecordDownloadVersion(cp, docID, filepath.Join("/", "other", "标题.md"), "v3"); err != nil {
		t.Fatal(err)
	}
	m, _ = ReadDownloadManifest(cp, docID)
	if len(m.Entries) != 2 {
		t.Fatalf("不同目录应各留一条: %+v", m.Entries)
	}
}

func TestDownloadManifestClearOnEmptyVersion(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	path := filepath.Join("/", "out", "标题.md")

	if err := RecordDownloadVersion(cp, docID, path, "v1"); err != nil {
		t.Fatal(err)
	}
	// version 为空（素材下载不完整）→ 清除记录
	if err := RecordDownloadVersion(cp, docID, path, ""); err != nil {
		t.Fatal(err)
	}
	if rec := LookupDownloadRecord(cp, docID, filepath.Dir(path)); rec != nil {
		t.Errorf("清除后应返回 nil，实际: %+v", rec)
	}
}
