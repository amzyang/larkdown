package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDownloadReportFinalize 锁部分成功退出码契约：
// 无失败 → 原样透传；有失败且有产出 → exit 3（部分成功）；全失败 → exit 1。
func TestDownloadReportFinalize(t *testing.T) {
	t.Run("nil 接收者透传", func(t *testing.T) {
		var r *downloadReport
		assert.NoError(t, r.finalize(nil))
		sentinel := errors.New("x")
		assert.Equal(t, sentinel, r.finalize(sentinel))
	})

	t.Run("无失败透传 nil", func(t *testing.T) {
		r := newDownloadReport()
		r.AddDoc("a.md", "A", false)
		assert.NoError(t, r.finalize(nil))
	})

	t.Run("有产出有失败 exit 3", func(t *testing.T) {
		r := newDownloadReport()
		r.AddDoc("a.md", "A", false)
		r.AddFailure("imgtoken", errors.New("perm denied"))
		err := r.finalize(nil)
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 3, ee.code)
		assert.Contains(t, ee.msg, "部分内容下载失败")
	})

	t.Run("全失败保留首错 exit 1", func(t *testing.T) {
		r := newDownloadReport()
		sentinel := errors.New("boom")
		r.AddFailure("url", sentinel)
		assert.Equal(t, sentinel, r.finalize(sentinel))
	})

	t.Run("全失败无首错 exit 1", func(t *testing.T) {
		r := newDownloadReport()
		r.AddFailure("url", errors.New("boom"))
		err := r.finalize(nil)
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 1, ee.code)
	})
}

// TestCodedError 锁 codedError 的包装语义：Error 透传内层消息，Unwrap 保留原错误链。
func TestCodedError(t *testing.T) {
	inner := errors.New("network broke")
	err := &codedError{err: inner, code: 2}
	assert.Equal(t, "network broke", err.Error())
	assert.True(t, errors.Is(err, inner))
}

// TestDownloadRootDir 锁 --follow 的 _refs/ 挂靠目录契约：唯一 .md 目标且未显式 -o 时
// 切到文件所在目录（与单目标旧行为一致，_refs/ 与主文档同目录），多目标或显式 -o 时维持 -o。
func TestDownloadRootDir(t *testing.T) {
	assert.Equal(t, "docs", downloadRootDir([]string{"docs/a.md"}, "./"))
	assert.Equal(t, ".", downloadRootDir([]string{"a.md"}, "./"))
	assert.Equal(t, "out", downloadRootDir([]string{"docs/a.md"}, "out"))
	assert.Equal(t, "./", downloadRootDir([]string{"docs/a.md", "docs/b.md"}, "./"))
	assert.Equal(t, "./", downloadRootDir([]string{"https://example.feishu.cn/docx/t"}, "./"))
}
