package core

import (
	"fmt"

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

	// 注册扁平 TopBlocks 并按需分配 BlockID（Ordered 列表需要通过 ParentID 计算序号；
	// 含嵌套项的列表其无子项兄弟块自带临时 BlockID 进入 TopBlocks，同样需要注册）
	for i, block := range result.TopBlocks {
		if block == nil {
			continue
		}
		if block.BlockID == "" {
			block.BlockID = fmt.Sprintf("_top_%d", i)
		}
		p.blockMap[block.BlockID] = block
	}

	// orderedAt 返回位置 i 处的 ordered 顶层块：扁平块，或单根 descendant group 的根块
	orderedAt := func(i int) *lark.DocxBlock {
		if b := result.TopBlocks[i]; b != nil {
			if b.BlockType == lark.DocxBlockTypeOrdered {
				return b
			}
			return nil
		}
		dg := result.descendantGroupAt(i)
		if dg == nil || len(dg.ChildrenIDs) != 1 {
			return nil
		}
		if root := p.blockMap[dg.ChildrenIDs[0]]; root != nil && root.BlockType == lark.DocxBlockTypeOrdered {
			return root
		}
		return nil
	}

	// 为连续的 ordered 顶层项（扁平块与嵌套 descendant group 根块混排）创建
	// 同一个合成父节点，保证序号跨嵌套项连续
	for i := 0; i < len(result.TopBlocks); {
		if orderedAt(i) == nil {
			i++
			continue
		}
		var childIDs []string
		j := i
		for j < len(result.TopBlocks) {
			b := orderedAt(j)
			if b == nil {
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

	// 按 TopBlocks 顺序展开顶层块 ID 序列（nil 占位处展开 descendant group 的根块），
	// 复用下载侧 parseSiblingBlocks 的分隔策略，保证与远端下载产物排版对称
	var topIDs []string
	for i, block := range result.TopBlocks {
		if block == nil {
			if dg := result.descendantGroupAt(i); dg != nil {
				topIDs = append(topIDs, dg.ChildrenIDs...)
			}
		} else {
			topIDs = append(topIDs, block.BlockID)
		}
	}

	return p.parseSiblingBlocks(topIDs), nil
}
