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
}
