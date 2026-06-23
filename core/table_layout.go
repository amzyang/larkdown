package core

import "unicode"

// =============================================================
// Table column width helpers
// =============================================================

const tablePageWidth = 900 // 飞书文档页面可用宽度（px）

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

// calcColumnWidths 根据各列最大内容宽度，按比例分配 totalPx，最小 50px/列
func calcColumnWidths(maxWidths []int, totalPx int) []int64 {
	n := len(maxWidths)
	if n == 0 {
		return nil
	}
	totalContent := 0
	for i, w := range maxWidths {
		if w < 1 {
			maxWidths[i] = 1
		}
		totalContent += maxWidths[i]
	}
	minPx := 50
	result := make([]int64, n)
	for i, w := range maxWidths {
		px := totalPx * w / totalContent
		if px < minPx {
			px = minPx
		}
		result[i] = int64(px)
	}
	return result
}
