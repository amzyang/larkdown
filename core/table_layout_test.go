package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// tlRepeat 构造 n 个值为 v 的 int 切片（列宽测试辅助）
func tlRepeat(v, n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// tlRepeat64 构造 n 个值为 v 的 int64 切片
func tlRepeat64(v int64, n int) []int64 {
	s := make([]int64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// tlSum 求 int64 切片之和
func tlSum(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

// distributeColumnWidths：纯 max-min 注水分配，确定性用例锁死期望值。
func TestDistributeColumnWidths(t *testing.T) {
	tests := []struct {
		name    string
		demands []int
		budget  int
		want    []int64
	}{
		{"empty returns nil", nil, 900, nil},
		{"fits: equal proportional fill", []int{100, 100, 100}, 900, []int64{300, 300, 300}},
		{"fits: single column fills budget", []int{100}, 900, []int64{900}},
		{"fits exactly: keeps demands", []int{300, 300, 300}, 900, []int64{300, 300, 300}},
		// 关键红线：小需求列拿满 demand 不被压，压缩集中到大列
		{"compress: small columns protected", []int{60, 60, 1000}, 900, []int64{60, 60, 780}},
		// 列极多，连 floor 都装不下 → 全 floorPx，靠水平滚动兜底
		{"overflow: floor fallback", tlRepeat(100, 40), 1440, tlRepeat64(50, 40)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := distributeColumnWidths(tt.demands, tt.budget)
			assert.Equal(t, tt.want, got)
		})
	}
}

// 撑满档：总需求 ≤ budget 时精确撑满 budget，且保持单调。
func TestDistributeColumnWidths_FillSumsToBudget(t *testing.T) {
	got := distributeColumnWidths([]int{100, 200, 300}, 900)
	assert.EqualValues(t, 900, tlSum(got))
	assert.LessOrEqual(t, got[0], got[1])
	assert.LessOrEqual(t, got[1], got[2])
}

// 压缩档：小列拿满、大列被压、总和精确、恒 ≥ floor。
func TestDistributeColumnWidths_CompressProtectsSmall(t *testing.T) {
	got := distributeColumnWidths([]int{60, 60, 1000}, 900)
	assert.EqualValues(t, 900, tlSum(got))
	assert.EqualValues(t, 60, got[0])
	assert.EqualValues(t, 60, got[1])
	assert.Less(t, got[2], int64(1000))
	for _, w := range got {
		assert.GreaterOrEqual(t, w, int64(floorPx))
	}
}

// 压缩档非整除：零头回填后总和精确等于 budget。
func TestDistributeColumnWidths_RemainderExact(t *testing.T) {
	got := distributeColumnWidths(tlRepeat(100, 20), 1450)
	assert.EqualValues(t, 1450, tlSum(got))
	for _, w := range got {
		assert.GreaterOrEqual(t, w, int64(floorPx))
	}
}

// calcColumnWidths：空输入返回 nil。
func TestCalcColumnWidths_Empty(t *testing.T) {
	assert.Nil(t, calcColumnWidths(nil))
	assert.Nil(t, calcColumnWidths([]int{}))
}

// 极窄内容 → 撑满 minViewPx。
func TestCalcColumnWidths_NarrowFillsMinView(t *testing.T) {
	got := calcColumnWidths([]int{1, 1, 1})
	assert.EqualValues(t, minViewPx, tlSum(got))
}

// 所有列恒 ≥ floorPx。
func TestCalcColumnWidths_AllAtLeastFloor(t *testing.T) {
	got := calcColumnWidths([]int{1, 50, 200, 0})
	for _, w := range got {
		assert.GreaterOrEqual(t, w, int64(floorPx))
	}
}

// 内容越宽，列宽不窄于内容更窄的列（单调）。
func TestCalcColumnWidths_Monotonic(t *testing.T) {
	got := calcColumnWidths([]int{1, 10, 30})
	assert.LessOrEqual(t, got[0], got[1])
	assert.LessOrEqual(t, got[1], got[2])
}

// 列极多：floor 之和 > maxViewPx → 全 floorPx。
func TestCalcColumnWidths_ManyColumnsFloorFallback(t *testing.T) {
	got := calcColumnWidths(tlRepeat(100, 50))
	assert.Len(t, got, 50)
	for _, w := range got {
		assert.EqualValues(t, floorPx, w)
	}
}
