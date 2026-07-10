package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestEnrichSearchScopeError(t *testing.T) {
	t.Run("scope 错误码包装为重新登录指引", func(t *testing.T) {
		err := enrichSearchScopeError(&lark.Error{Code: 99991679, Msg: "access denied"})
		assert.Contains(t, err.Error(), "search:docs:read")
		assert.Contains(t, err.Error(), "larkdown auth login")
	})

	t.Run("PermissionViolations 非空视为 scope 错误", func(t *testing.T) {
		err := enrichSearchScopeError(&lark.Error{
			Code:        403,
			Msg:         "forbidden",
			ErrorDetail: &lark.ErrorDetail{PermissionViolations: []*lark.ErrorPermissionViolation{{}}},
		})
		assert.Contains(t, err.Error(), "search:docs:read")
	})

	t.Run("消息含 scope 关键词视为 scope 错误", func(t *testing.T) {
		err := enrichSearchScopeError(&lark.Error{Code: 20027, Msg: "token has no required Scope"})
		assert.Contains(t, err.Error(), "search:docs:read")
	})

	t.Run("包装保留原始错误链", func(t *testing.T) {
		orig := &lark.Error{Code: 99991679, Msg: "no scope"}
		err := enrichSearchScopeError(fmt.Errorf("search: %w", orig))
		var le *lark.Error
		assert.True(t, errors.As(err, &le))
		assert.Equal(t, int64(99991679), le.Code)
	})

	t.Run("其他 lark 错误透传", func(t *testing.T) {
		orig := &lark.Error{Code: 131005, Msg: "not found"}
		assert.Same(t, orig, enrichSearchScopeError(orig))
	})

	t.Run("非 lark 错误透传", func(t *testing.T) {
		orig := errors.New("network down")
		assert.Same(t, orig, enrichSearchScopeError(orig))
	})
}
