package core

import (
	"math"
	"unicode"
)

// =============================================================
// Table column width helpers
// =============================================================

// 列宽分配相关常量（单位 px）。
//   - minViewPx / maxViewPx / floorPx 为版心与单列约束。
//   - pxPerUnit / cellPadPx / capPx 是渲染相关经验值，需用真实飞书文档截屏标定。
const (
	minViewPx = 900  // 飞书普通版心可用宽度，内容装得下时撑满它
	maxViewPx = 1200 // 版心动态扩展上限（标定后从 1440 收窄），再宽则压缩 + 飞书水平滚动
	floorPx   = 50   // 单列最小宽度（飞书 API 硬约束）
	pxPerUnit = 6    // 每个 displayWidth 单位的像素（实测飞书表格汉字 12px/字、2 单位/字）
	cellPadPx = 16   // 单元格左右内边距合计像素（实测 padding 8px×2）
	capPx     = 480  // 单列内容宽度上限（≈38 汉字/行），超出则该列在格内换行而非无限加宽
)

// displayWidth 计算文本显示宽度（CJK 字符算 2，ASCII 算 1）
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.IsOneOf([]*unicode.RangeTable{unicode.Co}, r) ||
			(r >= 0xFF01 && r <= 0xFF60) || // 全角 ASCII
			(r >= 0xFFE0 && r <= 0xFFE6) { // 全角符号
			w += 2
		} else {
			w++
		}
	}
	return w
}

// calcColumnWidths 把各列内容显示宽度换算为像素需求，按内容总量动态决定版心预算
// （minViewPx..maxViewPx），再用 max-min 注水分配，保证内容少的列尽量不被压缩。
func calcColumnWidths(maxWidths []int) []int64 {
	n := len(maxWidths)
	if n == 0 {
		return nil
	}
	demands := make([]int, n)
	total := 0
	for i, w := range maxWidths {
		px := w*pxPerUnit + cellPadPx
		if px < floorPx {
			px = floorPx
		}
		if px > capPx {
			px = capPx
		}
		demands[i] = px
		total += px
	}
	budget := total
	if budget < minViewPx {
		budget = minViewPx
	}
	if budget > maxViewPx {
		budget = maxViewPx
	}
	return distributeColumnWidths(demands, budget)
}

// distributeColumnWidths 按 max-min fairness（注水法）把 budget 分配给各列：
//   - 总需求 ≤ budget：按需求等比撑满 budget（逐项重算余额，整数无漂移）
//   - 总需求 > budget：每列保底 floorPx，剩余预算逐轮均分给未满列，列达到自身需求即
//     冻结退出 —— 需求小的列先拿满、压缩集中到大列；末尾零头回填使 Σ 精确等于 budget
//   - 列极多到连 floorPx 都装不下：全部 floorPx，总宽超出由飞书水平滚动兜底
func distributeColumnWidths(demands []int, budget int) []int64 {
	n := len(demands)
	if n == 0 {
		return nil
	}

	total := 0
	for _, d := range demands {
		total += d
	}

	// 装得下：按需求比例撑满 budget
	if total <= budget {
		return fillProportional(demands, budget, total)
	}

	// 列极多，连 floorPx 都装不下 → 保 floorPx，靠飞书水平滚动
	if n*floorPx >= budget {
		result := make([]int64, n)
		for i := range result {
			result[i] = floorPx
		}
		return result
	}

	// max-min 注水压缩：每列从 floorPx 起注水，达到自身需求即冻结
	widths := make([]int, n)
	active := 0
	for i, d := range demands {
		widths[i] = floorPx
		if d > floorPx {
			active++ // 仅未满列参与注水
		}
	}
	remaining := budget - n*floorPx
	for remaining > 0 && active > 0 {
		share := remaining / active
		if share == 0 {
			break // 余额 < 活跃列数，留给零头回填
		}
		for i := 0; i < n; i++ {
			room := demands[i] - widths[i]
			if room <= 0 {
				continue // 已满（冻结）
			}
			add := share
			if add > room {
				add = room
			}
			widths[i] += add
			remaining -= add
			if widths[i] >= demands[i] {
				active--
			}
		}
	}
	// 零头（remaining < active）逐列 +1 给仍未满的列，保证 Σ 精确等于 budget
	for i := 0; i < n && remaining > 0; i++ {
		if widths[i] < demands[i] {
			widths[i]++
			remaining--
		}
	}

	result := make([]int64, n)
	for i := range widths {
		result[i] = int64(widths[i])
	}
	return result
}

// fillProportional 按 ratios 把 budget 分配为各列宽度，逐项重算余额避免取整漂移，
// 保证 Σ 精确等于 budget。
func fillProportional(ratios []int, budget, ratioSum int) []int64 {
	result := make([]int64, len(ratios))
	remaining := budget
	rsum := ratioSum
	for i, r := range ratios {
		give := remaining
		if rsum > 0 {
			give = int(math.Round(float64(r) * float64(remaining) / float64(rsum)))
		}
		result[i] = int64(give)
		remaining -= give
		rsum -= r
	}
	return result
}
