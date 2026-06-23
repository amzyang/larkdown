package core

import (
	"fmt"
	"strings"

	"github.com/chyroc/lark"
)

// NormalizeMarkdown 将 markdown 通过 md→DocxBlocks→md 管线规范化，
// 消除格式差异（表格对齐、代码围栏语言、空行等），只保留语义内容。
func NormalizeMarkdown(markdown string) (string, error) {
	result, err := ConvertMarkdownToDocxBlocks(markdown, "")
	if err != nil {
		return "", err
	}

	p := NewParser(OutputConfig{}, nil)

	// 注册所有 descendant 块到 blockMap
	for _, dg := range result.DescendantGroups {
		for _, block := range dg.Descendants {
			p.blockMap[block.BlockID] = block
		}
	}

	// 设置 ParentID：先处理嵌套子块，再处理顶层子块
	for i, dg := range result.DescendantGroups {
		for _, block := range dg.Descendants {
			for _, childID := range block.Children {
				if child := p.blockMap[childID]; child != nil {
					child.ParentID = block.BlockID
				}
			}
		}
		// 为 ChildrenIDs 中的顶层块创建合成父节点（Ordered 列表需要通过 ParentID 计算序号）
		syntheticID := fmt.Sprintf("_parent_%d", i)
		p.blockMap[syntheticID] = &lark.DocxBlock{
			BlockID:  syntheticID,
			Children: dg.ChildrenIDs,
		}
		for _, childID := range dg.ChildrenIDs {
			if child := p.blockMap[childID]; child != nil {
				if child.ParentID == "" {
					child.ParentID = syntheticID
				}
			}
		}
	}

	// 为扁平 TopBlocks 分配 BlockID（Ordered 列表需要通过 ParentID 计算序号）
	for i, block := range result.TopBlocks {
		if block != nil && block.BlockID == "" {
			block.BlockID = fmt.Sprintf("_top_%d", i)
			p.blockMap[block.BlockID] = block
		}
	}

	// 为连续的扁平 ordered 列表项创建合成父节点
	for i := 0; i < len(result.TopBlocks); {
		block := result.TopBlocks[i]
		if block == nil || block.BlockType != lark.DocxBlockTypeOrdered {
			i++
			continue
		}
		// 收集连续的 ordered 项
		var childIDs []string
		j := i
		for j < len(result.TopBlocks) {
			b := result.TopBlocks[j]
			if b == nil || b.BlockType != lark.DocxBlockTypeOrdered {
				break
			}
			childIDs = append(childIDs, b.BlockID)
			j++
		}
		syntheticID := fmt.Sprintf("_flat_parent_%d", i)
		p.blockMap[syntheticID] = &lark.DocxBlock{
			BlockID:  syntheticID,
			Children: childIDs,
		}
		for _, id := range childIDs {
			p.blockMap[id].ParentID = syntheticID
		}
		i = j
	}

	var buf strings.Builder
	for i, block := range result.TopBlocks {
		if block == nil {
			if dg := result.descendantGroupAt(i); dg != nil {
				for _, childID := range dg.ChildrenIDs {
					childBlock := p.blockMap[childID]
					buf.WriteString(p.ParseDocxBlock(childBlock, 0))
					buf.WriteString("\n")
				}
			}
		} else {
			buf.WriteString(p.ParseDocxBlock(block, 0))
			buf.WriteString("\n")
		}
	}

	return buf.String(), nil
}
