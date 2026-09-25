package core

import (
	"bytes"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/text"
	"github.com/yuin/goldmark/v2/util"
)

// tableParagraphSplitter 在 goldmark 的表格段落转换器（优先级 200）之前把「被表格打断的段落」
// 在首个分隔行前切开，使表格转换器只会看到分隔行位于第二行的段落。
//
// goldmark v2 的 tableParagraphTransformer 遍历 node.Source() 的快照切片：首个表格已把其后
// 所有行收进 body，循环却继续扫描同一份快照，段落里的第二个分隔行会再建一张表，把同一批行
// 第二次写进文档，且经 InsertAfter 插在前一张表之前。分隔行落在第二行时该转换器会清空段落并
// break，是唯一不触发此缺陷的形态。判定逻辑（分隔行识别、表头列数上限）与其保持一致，否则切分
// 点会错位。
type tableParagraphSplitter struct{}

func (tableParagraphSplitter) Transform(node *ast.Paragraph, reader text.Reader, _ parser.Context) {
	lines := node.Source()
	if len(lines) < 2 {
		return
	}
	source := reader.Source()
	split, delims := -1, 0
	for i := 1; i < len(lines); i++ {
		cols := tableDelimiterColumns(lines[i].Bytes(source))
		if cols == 0 {
			continue
		}
		if split < 0 {
			if tableRowCellCount(lines[i-1], source) > cols {
				// 表头列数多于分隔行，转换器会整体放弃，段落保持原样
				return
			}
			split = i
		}
		delims++
	}
	// split < 2：段落会被整体消费掉，循环随即 break；delims < 2：不存在第二次命中
	if split < 2 || delims < 2 {
		return
	}

	head := ast.NewParagraph()
	head.SetBlankPreviousLines(node.HasBlankPreviousLines())
	for _, line := range lines[:split-1] {
		head.AppendSource(line)
	}
	last := head.Source()[split-2]
	last.Stop-- // 去掉行尾换行，与转换器截断段落时的处理一致
	head.Source()[split-2] = last

	node.SetSource(lines[split-1:])
	node.SetBlankPreviousLines(false)
	node.Parent().InsertBefore(node, head)
}

// tableDelimiterColumns 返回分隔行的列数，不是分隔行则返回 0
func tableDelimiterColumns(line []byte) int {
	if w, _ := util.IndentWidth(line, 0); w > 3 {
		return 0
	}
	allSep := true
	for _, b := range line {
		if b != '-' {
			allSep = false
		}
		if !util.IsSpace(b) && b != '-' && b != '|' && b != ':' {
			return 0
		}
	}
	if allSep {
		return 0
	}
	cols := bytes.Split(line, []byte{'|'})
	if util.IsBlank(cols[0]) {
		cols = cols[1:]
	}
	if len(cols) > 0 && util.IsBlank(cols[len(cols)-1]) {
		cols = cols[:len(cols)-1]
	}
	for _, col := range cols {
		if !isTableDelimiterColumn(col) {
			return 0
		}
	}
	return len(cols)
}

func isTableDelimiterColumn(col []byte) bool {
	col = util.TrimRightSpace(util.TrimLeftSpace(col))
	if len(col) > 0 && col[0] == ':' {
		col = col[1:]
	}
	if len(col) > 0 && col[len(col)-1] == ':' {
		col = col[:len(col)-1]
	}
	if len(col) == 0 {
		return false
	}
	for _, b := range col {
		if b != '-' {
			return false
		}
	}
	return true
}

// tableRowCellCount 数出一行里被未转义 | 分隔的单元格个数
func tableRowCellCount(segment text.Segment, source []byte) int {
	line := segment.TrimLeftSpace(source).TrimRightSpace(source).Bytes(source)
	pos, limit := 0, len(line)
	if limit > 0 && line[0] == '|' {
		pos++
	}
	if limit > 0 && line[limit-1] == '|' {
		limit--
	}
	cells := 0
	for ; pos < limit; cells++ {
		closure := pos
		for ; closure < limit; closure++ {
			if line[closure] == '|' && (closure == 0 || line[closure-1] != '\\') {
				break
			}
		}
		pos = closure + 1
	}
	return cells
}
