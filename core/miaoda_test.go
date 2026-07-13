package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveShareScope(t *testing.T) {
	cases := []struct {
		name      string
		explicit  string
		isNew     bool
		wantScope string
		wantApply bool
	}{
		// 显式档位：无论新建/更新都设置
		{"explicit tenant on new", ShareTenant, true, MiaodaScopeTenant, true},
		{"explicit tenant on update", ShareTenant, false, MiaodaScopeTenant, true},
		{"explicit public on new", SharePublic, true, MiaodaScopeAll, true},
		{"explicit public on update", SharePublic, false, MiaodaScopeAll, true},
		{"explicit selected on new", ShareSelected, true, "", false},
		{"explicit selected on update", ShareSelected, false, "", false},
		// 未指定：仅新建默认对机构开放；更新不覆盖
		{"default on new", "", true, MiaodaScopeTenant, true},
		{"default on update", "", false, "", false},
		// 未知值按未指定处理（cmd 层已白名单校验，这里防御）
		{"unknown on new", "garbage", true, MiaodaScopeTenant, true},
		{"unknown on update", "garbage", false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope, apply := resolveShareScope(c.explicit, c.isNew)
			assert.Equal(t, c.wantScope, scope)
			assert.Equal(t, c.wantApply, apply)
		})
	}
}

func TestBuildAccessScopeReq(t *testing.T) {
	t.Run("tenant omits require_login", func(t *testing.T) {
		req := buildAccessScopeReq(MiaodaScopeTenant, true)
		assert.Equal(t, MiaodaScopeTenant, req.Scope)
		assert.Nil(t, req.RequireLogin)
	})
	t.Run("all carries require_login", func(t *testing.T) {
		req := buildAccessScopeReq(MiaodaScopeAll, true)
		assert.Equal(t, MiaodaScopeAll, req.Scope)
		if assert.NotNil(t, req.RequireLogin) {
			assert.True(t, *req.RequireLogin)
		}
	})
	t.Run("all honors require_login false", func(t *testing.T) {
		req := buildAccessScopeReq(MiaodaScopeAll, false)
		if assert.NotNil(t, req.RequireLogin) {
			assert.False(t, *req.RequireLogin)
		}
	})
}
