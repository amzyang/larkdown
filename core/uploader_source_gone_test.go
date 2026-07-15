package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestSourceGoneErrorMessage(t *testing.T) {
	const source = "https://feishu.cn/wiki/Kd9cwBlcli1nImkAZpXcuEGPnmA"

	t.Run("131005 wiki 节点不存在或无权限", func(t *testing.T) {
		msg := (&SourceGoneError{Source: source, Code: 131005}).Error()
		assert.Contains(t, msg, source)
		assert.Contains(t, msg, "131005")
		assert.Contains(t, msg, "无权限")
		assert.Contains(t, msg, "--source")
	})

	t.Run("1770003 文档已删除可能在回收站", func(t *testing.T) {
		msg := (&SourceGoneError{Source: source, Code: 1770003}).Error()
		assert.Contains(t, msg, source)
		assert.Contains(t, msg, "1770003")
		assert.Contains(t, msg, "回收站")
		assert.Contains(t, msg, "--source")
	})

	t.Run("1770002 文档不存在", func(t *testing.T) {
		msg := (&SourceGoneError{Source: source, Code: 1770002}).Error()
		assert.Contains(t, msg, source)
		assert.Contains(t, msg, "1770002")
		assert.Contains(t, msg, "不存在")
		assert.Contains(t, msg, "--source")
	})
}

func TestAsSourceGone(t *testing.T) {
	const source = "https://feishu.cn/docx/BglTdUTHso3TeoxCmaNcm2Cxn08"

	tests := []struct {
		name     string
		err      error
		wantCode int64 // 0 表示期望 nil
	}{
		{"未包装 131005", &lark.Error{Code: 131005, Msg: "not found"}, 131005},
		{"未包装 1770002", &lark.Error{Code: 1770002, Msg: "not found"}, 1770002},
		{"未包装 1770003", &lark.Error{Code: 1770003, Msg: "resource deleted"}, 1770003},
		{"%w 包装后仍命中", fmt.Errorf("获取文档信息失败: %w", &lark.Error{Code: 1770003, Msg: "resource deleted"}), 1770003},
		{"其他 lark 错误码不命中", &lark.Error{Code: 99991679, Msg: "no scope"}, 0},
		{"非 lark 错误不命中", errors.New("network down"), 0},
		{"nil 不命中", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := asSourceGone(tt.err, source)
			if tt.wantCode == 0 {
				assert.Nil(t, got)
				return
			}
			assert.NotNil(t, got)
			assert.Equal(t, tt.wantCode, got.Code)
			assert.Equal(t, source, got.Source)
		})
	}
}
