package core

import (
	"os"
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
	if err := RecordDownloadVersion(cp, docID, path, "1700000000.42", nil); err != nil {
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
	if err := RecordDownloadVersion(cp, docID, filepath.Join(dir, "旧标题.md"), "v1", nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordDownloadVersion(cp, docID, filepath.Join(dir, "新标题.md"), "v2", nil); err != nil {
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
	if err := RecordDownloadVersion(cp, docID, filepath.Join("/", "other", "标题.md"), "v3", nil); err != nil {
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

	if err := RecordDownloadVersion(cp, docID, path, "v1", nil); err != nil {
		t.Fatal(err)
	}
	// version 为空（素材下载不完整）→ 清除记录
	if err := RecordDownloadVersion(cp, docID, path, "", nil); err != nil {
		t.Fatal(err)
	}
	if rec := LookupDownloadRecord(cp, docID, filepath.Dir(path)); rec != nil {
		t.Errorf("清除后应返回 nil，实际: %+v", rec)
	}
}

func TestDownloadManifestRecordsRefs(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	path := filepath.Join("/", "out", "标题.md")

	refs := []DocRef{{Token: "TokA", ObjType: "docx", URL: "https://x.feishu.cn/docx/TokA", Title: "规范"}}
	if err := RecordDownloadVersion(cp, docID, path, "v1", refs); err != nil {
		t.Fatal(err)
	}
	rec := LookupDownloadRecord(cp, docID, filepath.Dir(path))
	if rec == nil || !rec.RefsRecorded || len(rec.Refs) != 1 || rec.Refs[0] != refs[0] {
		t.Fatalf("refs 未随记录持久化: %+v", rec)
	}

	// 零引用文档同样标记已采集，与旧版「未采集」可区分
	if err := RecordDownloadVersion(cp, docID, path, "v2", nil); err != nil {
		t.Fatal(err)
	}
	rec = LookupDownloadRecord(cp, docID, filepath.Dir(path))
	if rec == nil || !rec.RefsRecorded || len(rec.Refs) != 0 {
		t.Fatalf("零引用应 RefsRecorded=true 且 Refs 为空: %+v", rec)
	}
}

// 旧版边车（无 refs 键）：RefsRecorded 为零值 false，
// --follow 时调用方据此视为过期、重新下载补录引用。
func TestDownloadManifestLegacyRecordHasNoRefs(t *testing.T) {
	cp := NewCachePaths(t.TempDir())
	const docID = "doccnXXX"
	legacy := "document_id: doccnXXX\nentries:\n  - path: /out/标题.md\n    version: v1\n"
	file := cp.DownloadManifestFile(docID)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := LookupDownloadRecord(cp, docID, "/out")
	if rec == nil || rec.Version != "v1" {
		t.Fatalf("旧版记录应可读取: %+v", rec)
	}
	if rec.RefsRecorded || rec.Refs != nil {
		t.Errorf("旧版记录应 RefsRecorded=false 且 Refs 为 nil: %+v", rec)
	}
}
