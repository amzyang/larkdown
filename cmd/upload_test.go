package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUploadReportFinalize 锁多文件上传的部分成功退出码契约（与 download 同语义）：
// 无失败 → 原样透传；有失败且有产出 → exit 3（部分成功）；全失败 → exit 1。
func TestUploadReportFinalize(t *testing.T) {
	t.Run("无失败透传 nil", func(t *testing.T) {
		r := &uploadReport{docs: []uploadedDoc{{File: "a.md"}}}
		assert.NoError(t, r.finalize(nil))
	})

	t.Run("无失败透传 firstErr", func(t *testing.T) {
		r := &uploadReport{}
		sentinel := errors.New("x")
		assert.Equal(t, sentinel, r.finalize(sentinel))
	})

	t.Run("有产出有失败 exit 3", func(t *testing.T) {
		r := &uploadReport{
			docs:   []uploadedDoc{{File: "a.md"}},
			failed: []reportFailure{{Ref: "b.md", Error: "boom"}},
		}
		err := r.finalize(nil)
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 3, ee.code)
		assert.Contains(t, ee.msg, "部分文件上传失败: 1 项失败（成功 1 项）")
	})

	t.Run("全失败保留首错 exit 1", func(t *testing.T) {
		r := &uploadReport{failed: []reportFailure{{Ref: "a.md", Error: "boom"}}}
		sentinel := errors.New("boom")
		assert.Equal(t, sentinel, r.finalize(sentinel))
	})

	t.Run("全失败无首错 exit 1", func(t *testing.T) {
		r := &uploadReport{failed: []reportFailure{{Ref: "a.md", Error: "boom"}}}
		err := r.finalize(nil)
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 1, ee.code)
		assert.Contains(t, ee.msg, "上传失败")
	})
}

// TestUploadReportView 锁多文件 --json 的输出模型契约：
// documents 恒为数组（空报告不输出 null），字段名与单文件输出（file/is_new/url）同名。
func TestUploadReportView(t *testing.T) {
	t.Run("空报告 documents 为空数组", func(t *testing.T) {
		r := &uploadReport{}
		data, err := json.Marshal(r.view())
		require.NoError(t, err)
		assert.JSONEq(t, `{"documents":[]}`, string(data))
	})

	t.Run("JSON 字段名契约", func(t *testing.T) {
		r := &uploadReport{
			docs:   []uploadedDoc{{File: "a.md", IsNew: true, URL: "https://x.feishu.cn/wiki/t"}},
			failed: []reportFailure{{Ref: "b.md", Error: "boom"}},
		}
		data, err := json.Marshal(r.view())
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"documents":[{"file":"a.md","is_new":true,"url":"https://x.feishu.cn/wiki/t"}],
			"failed":[{"ref":"b.md","error":"boom"}]
		}`, string(data))
	})

	t.Run("补链结果进 link_repairs（omitempty）", func(t *testing.T) {
		r := &uploadReport{
			docs:    []uploadedDoc{{File: "a.md", IsNew: false, URL: "https://x.feishu.cn/wiki/t"}},
			repairs: []linkRepair{{File: "a.md", OK: true}, {File: "c.md", OK: false, Error: "boom"}},
		}
		data, err := json.Marshal(r.view())
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"documents":[{"file":"a.md","is_new":false,"url":"https://x.feishu.cn/wiki/t"}],
			"link_repairs":[{"file":"a.md","ok":true},{"file":"c.md","ok":false,"error":"boom"}]
		}`, string(data))
	})
}

// TestPlanLinkRepairs 锁二次补链的选取规则：上传成功、且至少一个降级 .md 引用的目标
// 也在本批次成功上传的文件，按 pass 1 顺序返回；路径按绝对路径归一比较。
func TestPlanLinkRepairs(t *testing.T) {
	t.Run("前向引用：目标在后面上传成功", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "docs/a.md", ok: true, refs: []string{"docs/b.md"}},
			{file: "docs/b.md", ok: true},
		}
		assert.Equal(t, []string{"docs/a.md"}, planLinkRepairs(out))
	})

	t.Run("互引环：双方都补链", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "a.md", ok: true, refs: []string{"b.md"}},
			{file: "b.md", ok: true, refs: []string{"a.md"}},
		}
		assert.Equal(t, []string{"a.md", "b.md"}, planLinkRepairs(out))
	})

	t.Run("目标上传失败：不补链", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "a.md", ok: true, refs: []string{"b.md"}},
			{file: "b.md", ok: false},
		}
		assert.Empty(t, planLinkRepairs(out))
	})

	t.Run("目标不在批次内：不补链", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "a.md", ok: true, refs: []string{"elsewhere/c.md"}},
		}
		assert.Empty(t, planLinkRepairs(out))
	})

	t.Run("引用方上传失败：跳过", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "a.md", ok: false, refs: []string{"b.md"}},
			{file: "b.md", ok: true},
		}
		assert.Empty(t, planLinkRepairs(out))
	})

	t.Run("路径拼写差异按绝对路径归一", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "./docs/a.md", ok: true, refs: []string{"docs/x/../b.md"}},
			{file: "docs/b.md", ok: true},
		}
		assert.Equal(t, []string{"./docs/a.md"}, planLinkRepairs(out))
	})

	t.Run("每文件只补一次", func(t *testing.T) {
		out := []uploadOutcome{
			{file: "a.md", ok: true, refs: []string{"b.md", "c.md"}},
			{file: "b.md", ok: true},
			{file: "c.md", ok: true},
		}
		assert.Equal(t, []string{"a.md"}, planLinkRepairs(out))
	})
}
