package core_test

import (
	"errors"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/stretchr/testify/assert"
)

func ref(token string) core.DocRef {
	return core.DocRef{Token: token, ObjType: "docx", URL: "https://x.feishu.cn/docx/" + token}
}

// graphFetch 用内存引用图模拟下载：返回 Fetch 回调与按序记录的下载 token 列表。
func graphFetch(graph map[string][]core.DocRef) (func(core.DocRef) ([]core.DocRef, error), *[]string) {
	fetched := &[]string{}
	return func(r core.DocRef) ([]core.DocRef, error) {
		*fetched = append(*fetched, r.Token)
		return graph[r.Token], nil
	}, fetched
}

func TestFollowRefs(t *testing.T) {
	t.Run("深度 1 只下直接引用", func(t *testing.T) {
		fetch, fetched := graphFetch(map[string][]core.DocRef{"B": {ref("D")}})
		ok, fail := core.FollowRefs([]core.DocRef{ref("B"), ref("C")}, core.FollowOptions{Depth: 1, Fetch: fetch})
		assert.Equal(t, []string{"B", "C"}, *fetched)
		assert.Equal(t, 2, ok)
		assert.Equal(t, 0, fail)
	})

	t.Run("深度 2 追传递引用", func(t *testing.T) {
		fetch, fetched := graphFetch(map[string][]core.DocRef{"B": {ref("D")}})
		ok, _ := core.FollowRefs([]core.DocRef{ref("B"), ref("C")}, core.FollowOptions{Depth: 2, Fetch: fetch})
		assert.Equal(t, []string{"B", "C", "D"}, *fetched)
		assert.Equal(t, 3, ok)
	})

	t.Run("环终止", func(t *testing.T) {
		fetch, fetched := graphFetch(map[string][]core.DocRef{"A": {ref("B")}, "B": {ref("A")}})
		core.FollowRefs([]core.DocRef{ref("A")}, core.FollowOptions{Depth: 10, Fetch: fetch})
		assert.Equal(t, []string{"A", "B"}, *fetched)
	})

	t.Run("菱形只下一次", func(t *testing.T) {
		fetch, fetched := graphFetch(map[string][]core.DocRef{"A": {ref("C")}, "B": {ref("C")}})
		core.FollowRefs([]core.DocRef{ref("A"), ref("B")}, core.FollowOptions{Depth: 2, Fetch: fetch})
		assert.Equal(t, []string{"A", "B", "C"}, *fetched)
	})

	t.Run("Skip 命中不下载但仍 OnVisit", func(t *testing.T) {
		// 接线方式与 mirror 相同：Skip 与 OnVisit 共享同一集合。
		// 回归点：Skip 必须在 OnVisit 之前求值，否则刚 Add 的 token 会让 Skip 恒真。
		seen := map[string]bool{"B": true}
		var visited []string
		fetch, fetched := graphFetch(nil)
		core.FollowRefs([]core.DocRef{ref("A"), ref("B")}, core.FollowOptions{
			Depth:   1,
			Skip:    func(token string) bool { return seen[token] },
			OnVisit: func(r core.DocRef) { visited = append(visited, r.Token); seen[r.Token] = true },
			Fetch:   fetch,
		})
		assert.Equal(t, []string{"A", "B"}, visited)
		assert.Equal(t, []string{"A"}, *fetched)
	})

	t.Run("Fetch 失败继续并计数", func(t *testing.T) {
		fetch := func(r core.DocRef) ([]core.DocRef, error) {
			if r.Token == "A" {
				return nil, errors.New("permission denied")
			}
			return nil, nil
		}
		ok, fail := core.FollowRefs([]core.DocRef{ref("A"), ref("B")}, core.FollowOptions{Depth: 1, Fetch: fetch})
		assert.Equal(t, 1, ok)
		assert.Equal(t, 1, fail)
	})
}
