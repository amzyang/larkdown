package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkdown/utils"
)

// ExportSheetAsMarkdown 把整个 spreadsheet（所有非隐藏 tab）导出为单个 .md 文件。
// 用于 sheet 因 owner 关闭下载（错误码 1069902）导致 ExportSheet 失败时的 fallback。
//
// title 用作文档一级标题，输出文件名为 utils.SanitizeFileName(title)+".md"。
// sourceURL 非空时会在文件末尾追加 <!-- source: ... --> frontmatter，
// 与 docx 下载保持一致，便于 larkdown upload 增量回写。
//
// 多行单元格依赖 GetSheetContent 已经把 \n 替换为 <br>；
// 合并单元格切到 HTML <table> 输出 rowspan/colspan，否则用简单 markdown 表格语法。
func (c *Client) ExportSheetAsMarkdown(ctx context.Context, spreadsheetToken, outDir, title, sourceURL string) (string, error) {
	sheets, err := c.ListSpreadsheetSheets(ctx, spreadsheetToken)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "# %s\n\n", title)
	buf.WriteString("> ⚠️ 此文件由 BatchGetSheetValue API 生成。源 sheet 禁止 xlsx 导出，\n")
	buf.WriteString("> 此版本可能损失行高/列宽/字体/颜色/图表等格式信息。\n\n")

	rendered := 0
	for _, sheet := range sheets {
		if sheet.Hidden {
			continue
		}
		fmt.Fprintf(&buf, "## %s\n\n", sheet.Title)

		sheetData, err := c.GetSheetContent(ctx, spreadsheetToken+"_"+sheet.SheetID)
		if err != nil {
			fmt.Fprintf(&buf, "> ⚠️ 获取此工作表失败: %v\n\n", err)
			continue
		}
		if len(sheetData.Values) == 0 {
			buf.WriteString("> *此工作表为空*\n\n")
			continue
		}
		writeSheetMeta(&buf, sheetData)
		renderSheetTable(&buf, sheetData)
		buf.WriteString("\n")
		rendered++
	}

	if rendered == 0 {
		return "", fmt.Errorf("没有可导出的工作表（全部隐藏或失败）")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	// 双扩展名 .xlsx.md 表明这是因导出限制降级而来的 sheet markdown，
	// 与正常 ExportSheet 成功时的 .xlsx 区分，也方便 upload 路径识别。
	filename := utils.SanitizeFileName(title) + ".xlsx.md"
	filePath := filepath.Join(outDir, filename)

	content := buf.String()
	if frontMatter := GenerateFrontMatter(FrontMatter{Source: sourceURL}); frontMatter != "" {
		content = strings.TrimRight(content, "\n") + "\n" + frontMatter
	}
	if err := os.WriteFile(filePath, []byte(content), 0o666); err != nil {
		return "", err
	}
	return filePath, nil
}

// writeSheetMeta 在 H2 下输出工作表大小和冻结信息。
func writeSheetMeta(buf *strings.Builder, sheetData *SheetData) {
	var parts []string
	if sheetData.RowCount > 0 && sheetData.ColumnCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 行 × %d 列", sheetData.RowCount, sheetData.ColumnCount))
	}
	if sheetData.FrozenRowCount > 0 {
		parts = append(parts, fmt.Sprintf("前 %d 行冻结", sheetData.FrozenRowCount))
	}
	if sheetData.FrozenColumnCount > 0 {
		parts = append(parts, fmt.Sprintf("前 %d 列冻结", sheetData.FrozenColumnCount))
	}
	if len(parts) > 0 {
		fmt.Fprintf(buf, "> %s\n\n", strings.Join(parts, " · "))
	}
}

// renderSheetTable 选择 simple markdown 或 HTML 表格，复用 parser.go 的输出函数。
// 当冻结行数 > 1 且无合并时，把前 N 行用 <br> 折叠到一行作为多行表头。
// （有合并时不折叠：mergeMap 索引基于原始 row，折叠会破坏跨度信息。）
func renderSheetTable(buf *strings.Builder, sheetData *SheetData) {
	if len(sheetData.Merges) > 0 {
		spanMap := make(map[int64]map[int64]*mergeSpan)
		for _, m := range sheetData.Merges {
			if _, ok := spanMap[m.StartRowIndex]; !ok {
				spanMap[m.StartRowIndex] = make(map[int64]*mergeSpan)
			}
			spanMap[m.StartRowIndex][m.StartColumnIndex] = &mergeSpan{
				RowSpan: m.RowSpan,
				ColSpan: m.ColSpan,
			}
		}
		writeHTMLTable(buf, sheetData.Values, spanMap)
		return
	}
	values := sheetData.Values
	if sheetData.FrozenRowCount > 1 {
		values = collapseHeaderRows(values, int(sheetData.FrozenRowCount))
	}
	writeSimpleMarkdownTable(buf, values)
}

// collapseHeaderRows 把前 frozen 行折叠成单一表头行（用 <br> 在每列内串联），
// 让 markdown 的单行 header 也能表达多行表头。
func collapseHeaderRows(values [][]string, frozen int) [][]string {
	if frozen <= 1 || frozen > len(values) || len(values) == 0 {
		return values
	}
	cols := len(values[0])
	header := make([]string, cols)
	for c := 0; c < cols; c++ {
		var parts []string
		for r := 0; r < frozen; r++ {
			if c < len(values[r]) && values[r][c] != "" {
				parts = append(parts, values[r][c])
			}
		}
		header[c] = strings.Join(parts, "<br>")
	}
	out := make([][]string, 0, len(values)-frozen+1)
	out = append(out, header)
	out = append(out, values[frozen:]...)
	return out
}
