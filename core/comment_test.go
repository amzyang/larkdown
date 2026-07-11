package core

import (
	"context"
	"errors"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

type fetchCall struct {
	batch  []string
	idType lark.IDType
}

// echoFetcher 返回「id → 名-id」，并记录每次调用的批次与 id 类型。
func echoFetcher(calls *[]fetchCall) basicUserFetcher {
	return func(_ context.Context, batch []string, idType lark.IDType) ([]BasicUser, error) {
		*calls = append(*calls, fetchCall{batch: batch, idType: idType})
		users := make([]BasicUser, 0, len(batch))
		for _, id := range batch {
			users = append(users, BasicUser{UserID: id, Name: "名-" + id})
		}
		return users, nil
	}
}

func TestResolveUserNamesGroupsByIDPrefix(t *testing.T) {
	var calls []fetchCall

	got := resolveUserNames(context.Background(), echoFetcher(&calls),
		[]string{"on_a", "ou_b", "on_c", "x_d"}, lark.IDTypeUnionID)

	// on_ → union_id，ou_ → open_id，其余归 defaultIDType（union_id）桶；桶按 union→open→user 序处理
	assert.Equal(t, []fetchCall{
		{batch: []string{"on_a", "on_c", "x_d"}, idType: lark.IDTypeUnionID},
		{batch: []string{"ou_b"}, idType: lark.IDTypeOpenID},
	}, calls)
	assert.Equal(t, map[string]string{
		"on_a": "名-on_a", "ou_b": "名-ou_b", "on_c": "名-on_c", "x_d": "名-x_d",
	}, got)
}

func TestResolveUserNamesSplitsBatchesOfTen(t *testing.T) {
	var calls []fetchCall
	ids := make([]string, 11)
	for i := range ids {
		ids[i] = "on_" + string(rune('a'+i))
	}

	got := resolveUserNames(context.Background(), echoFetcher(&calls), ids, lark.IDTypeUnionID)

	assert.Len(t, calls, 2)
	assert.Equal(t, ids[:10], calls[0].batch)
	assert.Equal(t, ids[10:], calls[1].batch)
	assert.Len(t, got, 11)
}

func TestResolveUserNamesPartialFailure(t *testing.T) {
	fetch := func(_ context.Context, batch []string, idType lark.IDType) ([]BasicUser, error) {
		if idType == lark.IDTypeUnionID {
			return nil, errors.New("boom")
		}
		return []BasicUser{{UserID: batch[0], Name: "开放平台用户"}}, nil
	}

	got := resolveUserNames(context.Background(), fetch, []string{"on_a", "ou_b"}, lark.IDTypeUnionID)

	// union 批失败仅告警，不影响 open 批的结果
	assert.Equal(t, map[string]string{"ou_b": "开放平台用户"}, got)
}

func TestResolveUserNamesSkipsEmptyName(t *testing.T) {
	fetch := func(_ context.Context, batch []string, _ lark.IDType) ([]BasicUser, error) {
		return []BasicUser{{UserID: batch[0], Name: ""}}, nil
	}

	got := resolveUserNames(context.Background(), fetch, []string{"on_a"}, lark.IDTypeUnionID)

	assert.Empty(t, got)
}

func TestResolveUserNamesEmptyInput(t *testing.T) {
	fetch := func(context.Context, []string, lark.IDType) ([]BasicUser, error) {
		t.Fatal("空输入不应触发请求")
		return nil, nil
	}

	assert.Empty(t, resolveUserNames(context.Background(), fetch, nil, lark.IDTypeUnionID))
}
