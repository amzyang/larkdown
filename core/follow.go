package core

// FollowOptions 控制 FollowRefs 的 BFS 行为，下载动作经回调注入（纯逻辑，便于单测）。
type FollowOptions struct {
	Depth   int                                // 追多少层引用，>=1
	Skip    func(token string) bool            // 返回 true 表示文档已存在（如镜像树内），不下载
	OnVisit func(ref DocRef)                   // 每个 ref 出队即回调（无论 Skip/下载成败），mirror 用于 prune 保护
	Fetch   func(ref DocRef) ([]DocRef, error) // 下载该文档并返回其正文引用
	Logf    func(format string, args ...any)
}

// FollowRefs 逐层（BFS）下载被引用文档：visited 防环，Fetch 失败告警继续。
// 顺序执行——客户端限流（读 5 req/s）下并发收益有限，如需提速可引入 semaphore。
// 返回成功与失败的下载数。
func FollowRefs(initial []DocRef, opts FollowOptions) (succeeded, failed int) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	visited := make(map[string]bool)
	var queue []DocRef
	enqueue := func(refs []DocRef) {
		for _, ref := range refs {
			if visited[ref.Token] {
				continue
			}
			visited[ref.Token] = true
			queue = append(queue, ref)
		}
	}
	enqueue(initial)

	for depth := 1; depth <= opts.Depth && len(queue) > 0; depth++ {
		current := queue
		queue = nil
		for _, ref := range current {
			// Skip 须在 OnVisit 之前求值：mirror 把两者接到同一个 seen 集合
			// （Skip=seen.Has, OnVisit=seen.Add），先 Add 会让 Skip 恒真。
			skip := opts.Skip != nil && opts.Skip(ref.Token)
			if opts.OnVisit != nil {
				opts.OnVisit(ref)
			}
			if skip {
				continue
			}
			next, err := opts.Fetch(ref)
			if err != nil {
				failed++
				logf("警告: follow 下载失败（跳过）: %s (%s): %v", ref.Title, ref.URL, err)
				continue
			}
			succeeded++
			enqueue(next)
		}
	}
	return succeeded, failed
}
