package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestSignatureFromBlock_TextLike(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: "hello"},
			}},
		},
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("2:hello"), sig)
}

func TestSignatureFromBlock_TextWithStyle(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{
					Content:          "bold",
					TextElementStyle: &lark.DocxTextElementStyle{Bold: true},
				},
			}},
		},
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("2:bold|B"), sig)
}

func TestSignatureFromBlock_Code(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeCode,
		Code: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{Language: lark.DocxCodeLanguageGo},
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: "fmt.Println()"},
			}},
		},
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("14:fmt.Println()|lang:22"), sig)
}

func TestSignatureFromBlock_Image(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeImage,
		Image:     &lark.DocxBlockImage{Token: "abc123"},
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("media:abc123"), sig)
}

func TestSignatureFromBlock_FileViaView(t *testing.T) {
	// File 被 View(33) 包裹：签名穿透 view 取内层 file token
	inner := &lark.DocxBlock{
		BlockID:   "innerfile",
		BlockType: lark.DocxBlockTypeFile,
		File:      &lark.DocxBlockFile{Token: "file_tok"},
	}
	view := &lark.DocxBlock{
		BlockID:   "viewblk",
		BlockType: lark.DocxBlockTypeView,
		Children:  []string{"innerfile"},
	}
	blockMap := map[string]*lark.DocxBlock{"innerfile": inner, "viewblk": view}
	assert.Equal(t, BlockSignature("media:file_tok"), SignatureFromBlock(view, blockMap))
	assert.Equal(t, BlockSignature("media:file_tok"), SignatureFromBlock(inner, blockMap))
}

func TestSignatureFromLocalEntry_Media(t *testing.T) {
	// 已解析 token 的本地图片 → media:<token>，与远程对称
	resolved := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "abc123"}},
		},
	}
	assert.Equal(t, BlockSignature("media:abc123"), SignatureFromLocalEntry(resolved, 0))

	// 未解析（新增/手动添加）→ media:new:<idx>，永不误配远程
	fresh := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeText},
			{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}},
		},
	}
	assert.Equal(t, BlockSignature("media:new:1"), SignatureFromLocalEntry(fresh, 1))
}

func TestSignatureFromBlock_Divider(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeDivider,
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("divider"), sig)
}

func TestSignatureFromBlock_Todo(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeTodo,
		Todo: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{Done: true},
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: "task"},
			}},
		},
	}
	sig := SignatureFromBlock(block, nil)
	assert.Equal(t, BlockSignature("17:task|done"), sig)
}

func TestSignatureFromBlock_Table(t *testing.T) {
	blockMap := map[string]*lark.DocxBlock{
		"table1": {
			BlockID:   "table1",
			BlockType: lark.DocxBlockTypeTable,
			Children:  []string{"cell1"},
			Table: &lark.DocxBlockTable{
				Cells: []string{"cell1"},
				Property: &lark.DocxBlockTableProperty{
					RowSize:    1,
					ColumnSize: 1,
				},
			},
		},
		"cell1": {
			BlockID:   "cell1",
			BlockType: lark.DocxBlockTypeTableCell,
			Children:  []string{"text1"},
			TableCell: &lark.DocxBlockTableCell{},
		},
		"text1": {
			BlockID:   "text1",
			BlockType: lark.DocxBlockTypeText,
			Text: &lark.DocxBlockText{
				Elements: []*lark.DocxTextElement{{
					TextRun: &lark.DocxTextElementTextRun{Content: "cell content"},
				}},
			},
		},
	}

	sig := SignatureFromBlock(blockMap["table1"], blockMap)
	assert.True(t, len(string(sig)) > 0)
	assert.Contains(t, string(sig), "desc:")
}

// --- ComputeDiff 测试 ---

func TestComputeDiff_Identical(t *testing.T) {
	sigs := []BlockSignature{"a", "b", "c"}
	ops := ComputeDiff(sigs, sigs)
	for _, op := range ops {
		assert.Equal(t, DiffOpEqual, op.Type)
	}
	assert.Len(t, ops, 3)
}

func TestComputeDiff_PureInsert(t *testing.T) {
	remote := []BlockSignature{}
	local := []BlockSignature{"a", "b"}
	ops := ComputeDiff(remote, local)
	assert.Len(t, ops, 2)
	assert.Equal(t, DiffOpInsert, ops[0].Type)
	assert.Equal(t, DiffOpInsert, ops[1].Type)
}

func TestComputeDiff_PureDelete(t *testing.T) {
	remote := []BlockSignature{"a", "b"}
	local := []BlockSignature{}
	ops := ComputeDiff(remote, local)
	assert.Len(t, ops, 2)
	assert.Equal(t, DiffOpDelete, ops[0].Type)
	assert.Equal(t, DiffOpDelete, ops[1].Type)
}

func TestComputeDiff_MixedChanges(t *testing.T) {
	remote := []BlockSignature{"a", "b", "c", "d"}
	local := []BlockSignature{"a", "x", "c", "d", "e"}
	ops := ComputeDiff(remote, local)

	// 预期: Equal(a), Delete(b), Insert(x), Equal(c), Equal(d), Insert(e)
	assert.Len(t, ops, 6)
	assert.Equal(t, DiffOpEqual, ops[0].Type)
	assert.Equal(t, DiffOpDelete, ops[1].Type)
	assert.Equal(t, DiffOpInsert, ops[2].Type)
	assert.Equal(t, DiffOpEqual, ops[3].Type)
	assert.Equal(t, DiffOpEqual, ops[4].Type)
	assert.Equal(t, DiffOpInsert, ops[5].Type)
}

func TestComputeDiff_SingleMiddleChange(t *testing.T) {
	remote := []BlockSignature{"a", "b", "c"}
	local := []BlockSignature{"a", "x", "c"}
	ops := ComputeDiff(remote, local)

	// Equal(a), Delete(b), Insert(x), Equal(c)
	assert.Len(t, ops, 4)
	assert.Equal(t, DiffOpEqual, ops[0].Type)
	assert.Equal(t, DiffOpDelete, ops[1].Type)
	assert.Equal(t, DiffOpInsert, ops[2].Type)
	assert.Equal(t, DiffOpEqual, ops[3].Type)
}

// --- GroupChangeRegions 测试 ---

func TestGroupChangeRegions_SingleRegion(t *testing.T) {
	ops := []DiffOp{
		{Type: DiffOpEqual, RemoteIdx: 0, LocalIdx: 0},
		{Type: DiffOpDelete, RemoteIdx: 1},
		{Type: DiffOpInsert, LocalIdx: 1},
		{Type: DiffOpEqual, RemoteIdx: 2, LocalIdx: 2},
	}
	regions := GroupChangeRegions(ops)
	assert.Len(t, regions, 1)
	assert.Equal(t, []int{1}, regions[0].DeleteIndices)
	assert.Equal(t, []int{1}, regions[0].InsertIndices)
	assert.Equal(t, 1, regions[0].RemoteStartIndex)
}

func TestGroupChangeRegions_MultipleRegions(t *testing.T) {
	ops := []DiffOp{
		{Type: DiffOpDelete, RemoteIdx: 0},
		{Type: DiffOpEqual, RemoteIdx: 1, LocalIdx: 0},
		{Type: DiffOpDelete, RemoteIdx: 2},
		{Type: DiffOpInsert, LocalIdx: 1},
		{Type: DiffOpInsert, LocalIdx: 2},
	}
	regions := GroupChangeRegions(ops)
	assert.Len(t, regions, 2)
	assert.Equal(t, []int{0}, regions[0].DeleteIndices)
	assert.Nil(t, regions[0].InsertIndices)
	assert.Equal(t, []int{2}, regions[1].DeleteIndices)
	assert.Equal(t, []int{1, 2}, regions[1].InsertIndices)
}

func TestGroupChangeRegions_BoundaryChange(t *testing.T) {
	ops := []DiffOp{
		{Type: DiffOpEqual, RemoteIdx: 0, LocalIdx: 0},
		{Type: DiffOpInsert, LocalIdx: 1},
	}
	regions := GroupChangeRegions(ops)
	assert.Len(t, regions, 1)
	assert.Nil(t, regions[0].DeleteIndices)
	assert.Equal(t, []int{1}, regions[0].InsertIndices)
}

// --- PairBlocks 测试 ---

func TestPairBlocks_SameType(t *testing.T) {
	remoteBlocks := []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}},
	}
	localResult := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}},
		},
	}
	region := ChangeRegion{
		DeleteIndices: []int{0},
		InsertIndices: []int{0},
	}

	ops := PairBlocks(region, remoteBlocks, localResult, false)
	assert.Len(t, ops, 1)
	assert.Equal(t, PairedOpReplace, ops[0].Type)
}

func TestPairBlocks_DifferentType(t *testing.T) {
	remoteBlocks := []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}},
	}
	localResult := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeDivider},
		},
	}
	region := ChangeRegion{
		DeleteIndices: []int{0},
		InsertIndices: []int{0},
	}

	ops := PairBlocks(region, remoteBlocks, localResult, false)
	assert.Len(t, ops, 2)
	assert.Equal(t, PairedOpDelete, ops[0].Type)
	assert.Equal(t, PairedOpInsert, ops[1].Type)
}

func TestPairBlocks_DescendantNotPaired(t *testing.T) {
	remoteBlocks := []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeTable, Table: &lark.DocxBlockTable{}},
	}
	localResult := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{nil},
		DescendantGroups: []DescendantGroup{
			{TopBlockIndex: 0},
		},
	}
	region := ChangeRegion{
		DeleteIndices: []int{0},
		InsertIndices: []int{0},
	}

	ops := PairBlocks(region, remoteBlocks, localResult, false)
	assert.Len(t, ops, 2)
	assert.Equal(t, PairedOpDelete, ops[0].Type)
	assert.Equal(t, PairedOpInsert, ops[1].Type)
}

func TestPairBlocks_ImagePaired(t *testing.T) {
	remoteBlocks := []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "old"}},
	}
	localResult := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}},
		},
	}
	region := ChangeRegion{
		DeleteIndices: []int{0},
		InsertIndices: []int{0},
	}

	ops := PairBlocks(region, remoteBlocks, localResult, false)
	assert.Len(t, ops, 1)
	assert.Equal(t, PairedOpReplace, ops[0].Type)
}

func TestCanDocsAIReplace(t *testing.T) {
	text := &lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}}
	h2 := &lark.DocxBlock{BlockType: lark.DocxBlockTypeHeading2, Heading2: &lark.DocxBlockText{}}
	bullet := &lark.DocxBlock{BlockType: lark.DocxBlockTypeBullet, Bullet: &lark.DocxBlockText{}}
	ordered := &lark.DocxBlock{BlockType: lark.DocxBlockTypeOrdered, Ordered: &lark.DocxBlockText{}}
	board := &lark.DocxBlock{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "t"}}
	addons := &lark.DocxBlock{BlockType: lark.DocxBlockTypeAddOns, AddOns: &lark.DocxBlockAddOns{}}
	img := &lark.DocxBlock{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "i"}}
	table := &lark.DocxBlock{BlockType: lark.DocxBlockTypeTable, Table: &lark.DocxBlockTable{}}
	r := &ConvertResult{}

	// 跨类型 text-like + enabled → true
	assert.True(t, canDocsAIReplace(text, h2, 0, r, true))
	assert.True(t, canDocsAIReplace(bullet, ordered, 0, r, true))

	// disabled → false
	assert.False(t, canDocsAIReplace(text, h2, 0, r, false))
	// 同类型（canReplace 优先）→ false
	assert.False(t, canDocsAIReplace(text, text, 0, r, true))
	// 画板 / AddOns / 媒体 → false
	assert.False(t, canDocsAIReplace(text, board, 0, r, true))
	assert.False(t, canDocsAIReplace(text, addons, 0, r, true))
	assert.False(t, canDocsAIReplace(text, img, 0, r, true))
	assert.False(t, canDocsAIReplace(board, text, 0, r, true))
	// 容器块（remote 为 descendant）→ false
	assert.False(t, canDocsAIReplace(table, text, 0, r, true))
}

func TestPairBlocks_DocsAIReplace(t *testing.T) {
	remoteBlocks := []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}},
	}
	localResult := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeHeading2, Heading2: &lark.DocxBlockText{}},
		},
	}
	region := ChangeRegion{DeleteIndices: []int{0}, InsertIndices: []int{0}}

	// enabled：跨类型 text→heading2 配成 docs_ai 替换（一对消费）
	ops := PairBlocks(region, remoteBlocks, localResult, true)
	assert.Len(t, ops, 1)
	assert.Equal(t, PairedOpDocsAIReplace, ops[0].Type)

	// disabled：回退 delete+insert
	ops = PairBlocks(region, remoteBlocks, localResult, false)
	assert.Len(t, ops, 2)
	assert.Equal(t, PairedOpDelete, ops[0].Type)
	assert.Equal(t, PairedOpInsert, ops[1].Type)
}

func TestSignatureFromBlock_Board(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeBoard,
		Board:     &lark.DocxBlockBoard{Token: "board_abc"},
	}
	sig := SignatureFromBlock(block, nil)
	// token-aware：round-trip 白板按 token 匹配
	assert.Equal(t, BlockSignature("board:board_abc"), sig)
}

func TestSignatureFromLocalEntry_Board(t *testing.T) {
	// 无 token 的 plantuml 白板：命名空间隔离，永不误配远程 token
	result := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{
				BlockType: lark.DocxBlockTypeBoard,
				Board:     &lark.DocxBlockBoard{},
			},
		},
		BoardIndices: []int{0},
		BoardCodes:   []string{"@startuml\nA -> B\n@enduml"},
	}
	sig := SignatureFromLocalEntry(result, 0)
	assert.Contains(t, string(sig), "board:plantuml:")
}

func TestSignatureFromLocalEntry_RoundTripBoard(t *testing.T) {
	// 带 token 的 round-trip 白板：与远程 board:<token> 对称
	result := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{
				BlockType: lark.DocxBlockTypeBoard,
				Board:     &lark.DocxBlockBoard{Token: "board_xyz"},
			},
		},
	}
	sig := SignatureFromLocalEntry(result, 0)
	assert.Equal(t, BlockSignature("board:board_xyz"), sig)
}

func TestSignatureFromLocalEntry_TextBlock(t *testing.T) {
	result := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{
				BlockType: lark.DocxBlockTypeText,
				Text: &lark.DocxBlockText{
					Elements: []*lark.DocxTextElement{{
						TextRun: &lark.DocxTextElementTextRun{Content: "hello"},
					}},
				},
			},
		},
	}
	sig := SignatureFromLocalEntry(result, 0)
	assert.Equal(t, BlockSignature("2:hello"), sig)
}

func TestSignatureFromLocalEntry_DescendantGroup(t *testing.T) {
	result := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{nil},
		DescendantGroups: []DescendantGroup{
			{TopBlockIndex: 0, ChildrenIDs: []string{"t1"}},
		},
	}
	sig := SignatureFromLocalEntry(result, 0)
	assert.Contains(t, string(sig), "desc:")
}

func textWith(content string) *lark.DocxBlockText {
	return &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{
		TextRun: &lark.DocxTextElementTextRun{Content: content},
	}}}
}

// 核心对称性：本地 descendant group 签名应与等价远程块签名相等（剔除临时 ID / Property / 列宽）。
func TestSignatureFromLocalEntry_DescendantMatchesRemote(t *testing.T) {
	// 本地 table descendant group（临时 ID）
	local := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{nil},
		DescendantGroups: []DescendantGroup{{
			TopBlockIndex: 0,
			ChildrenIDs:   []string{"tbl"},
			Descendants: []*lark.DocxBlock{
				{BlockID: "tbl", BlockType: lark.DocxBlockTypeTable, Children: []string{"c1", "c2"}},
				{BlockID: "c1", BlockType: lark.DocxBlockTypeTableCell, Children: []string{"t1"}},
				{BlockID: "t1", BlockType: lark.DocxBlockTypeText, Text: textWith("A")},
				{BlockID: "c2", BlockType: lark.DocxBlockTypeTableCell, Children: []string{"t2"}},
				{BlockID: "t2", BlockType: lark.DocxBlockTypeText, Text: textWith("B")},
			},
		}},
	}
	localSig := SignatureFromLocalEntry(local, 0)

	// 等价远程 table 块：不同 block_id、含 Property/ColumnWidth（应被签名忽略）
	remoteMap := map[string]*lark.DocxBlock{
		"R_tbl": {BlockID: "R_tbl", BlockType: lark.DocxBlockTypeTable, Children: []string{"R_c1", "R_c2"},
			Table: &lark.DocxBlockTable{Property: &lark.DocxBlockTableProperty{RowSize: 1, ColumnSize: 2, ColumnWidth: []int64{200, 300}, HeaderRow: true}}},
		"R_c1": {BlockID: "R_c1", BlockType: lark.DocxBlockTypeTableCell, Children: []string{"R_t1"}},
		"R_t1": {BlockID: "R_t1", BlockType: lark.DocxBlockTypeText, Text: textWith("A")},
		"R_c2": {BlockID: "R_c2", BlockType: lark.DocxBlockTypeTableCell, Children: []string{"R_t2"}},
		"R_t2": {BlockID: "R_t2", BlockType: lark.DocxBlockTypeText, Text: textWith("B")},
	}
	remoteSig := SignatureFromBlock(remoteMap["R_tbl"], remoteMap)

	assert.Equal(t, remoteSig, localSig)
}

// Quote(15) 旧块签名应与上传重建的 QuoteContainer(34) 本地签名相等。
func TestSignatureFromBlock_Quote15MatchesQuoteContainer(t *testing.T) {
	quote15 := &lark.DocxBlock{BlockType: lark.DocxBlockTypeQuote, Quote: textWith("legacy quote")}
	sig15 := SignatureFromBlock(quote15, nil)

	local := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{nil},
		DescendantGroups: []DescendantGroup{{
			TopBlockIndex: 0, ChildrenIDs: []string{"qc"},
			Descendants: []*lark.DocxBlock{
				{BlockID: "qc", BlockType: lark.DocxBlockTypeQuoteContainer, Children: []string{"qt"}},
				{BlockID: "qt", BlockType: lark.DocxBlockTypeText, Text: textWith("legacy quote")},
			},
		}},
	}
	assert.Equal(t, SignatureFromLocalEntry(local, 0), sig15)
}

// Equation(16) 旧块签名应与上传重建的 Text(2) 本地签名相等（不再落 unknown 兜底）。
func TestSignatureFromBlock_Equation16MatchesText(t *testing.T) {
	eqElem := func() *lark.DocxBlockText {
		return &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{
			Equation: &lark.DocxTextElementEquation{Content: "x^2"},
		}}}
	}
	eq := &lark.DocxBlock{BlockType: lark.DocxBlockTypeEquation, Equation: eqElem()}
	sigEq := SignatureFromBlock(eq, nil)

	localText := &lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Text: eqElem()}
	local := &ConvertResult{TopBlocks: []*lark.DocxBlock{localText}}
	assert.Equal(t, SignatureFromLocalEntry(local, 0), sigEq)
	assert.NotContains(t, string(sigEq), "unknown")
}

// AddOns(Mermaid) 签名只取图源 data，忽略服务端不可控的 theme/view 及 JSON key 顺序。
func TestAddOnsSignature_StableAcrossRecordShape(t *testing.T) {
	a := `{"data":"graph TD\nA-->B","theme":"default","view":"chart"}`
	b := `{"view":"chart","theme":"dark","data":"graph TD\nA-->B"}`
	assert.Equal(t, addOnsSignatureContent(a), addOnsSignatureContent(b))

	blkA := &lark.DocxBlock{BlockType: lark.DocxBlockTypeAddOns, AddOns: &lark.DocxBlockAddOns{Record: a}}
	blkB := &lark.DocxBlock{BlockType: lark.DocxBlockTypeAddOns, AddOns: &lark.DocxBlockAddOns{Record: b}}
	assert.Equal(t, SignatureFromBlock(blkA, nil), SignatureFromBlock(blkB, nil))
}

// Link URL：远程 percent-encoded 与本地解码态归一后签名一致。
func TestCanonicalTextContent_LinkURLNormalized(t *testing.T) {
	mk := func(url string) *lark.DocxBlockText {
		return &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{
			TextRun: &lark.DocxTextElementTextRun{
				Content:          "link",
				TextElementStyle: &lark.DocxTextElementStyle{Link: &lark.DocxTextElementStyleLink{URL: url}},
			},
		}}}
	}
	encoded := mk("https%3A%2F%2Fexample.com%2Fa%3Fb%3D1")
	decoded := mk("https://example.com/a?b=1")
	assert.Equal(t, canonicalTextContent(decoded), canonicalTextContent(encoded))
}

// isEmptyTextBlock：识别飞书空段落（用于增量上传时跳过删除）。
func TestIsEmptyTextBlock(t *testing.T) {
	// 无 Text → 空
	assert.True(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText}))
	// 空 elements → 空
	assert.True(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}}))
	// 仅空白 → 空
	assert.True(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Text: textWith("   ")}))
	// 有内容 → 非空
	assert.False(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Text: textWith("hi")}))
	// 非 Text 类型 → 非空（不适用）
	assert.False(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeDivider}))
	// 带 children（如折叠块）→ 非空
	assert.False(t, isEmptyTextBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText, Children: []string{"x"}}))
}

// computeInsertIndex：变更区域内新块插入位置 = 起点 + 替换数（不减删除数）。
func TestComputeInsertIndex(t *testing.T) {
	// 纯删除+插入（descendant 块被改写），区域在非首位 → 插回原位，不上移。
	assert.Equal(t, int64(1), computeInsertIndex(1, 0))
	assert.Equal(t, int64(5), computeInsertIndex(5, 0))
	// 区域在首位 → 0。
	assert.Equal(t, int64(0), computeInsertIndex(0, 0))
	// 含原地替换：新块落在替换块之后。
	assert.Equal(t, int64(7), computeInsertIndex(5, 2))
	assert.Equal(t, int64(3), computeInsertIndex(3, 0))
}

// 样式两端空白归一：「空白在样式内」（远程）与「空白在样式外」（本地外提后）签名一致。
func TestCanonicalTextContent_StyleWhitespaceHoisted(t *testing.T) {
	bold := func(c string) *lark.DocxTextElement {
		return &lark.DocxTextElement{TextRun: &lark.DocxTextElementTextRun{
			Content: c, TextElementStyle: &lark.DocxTextElementStyle{Bold: true},
		}}
	}
	plain := func(c string) *lark.DocxTextElement {
		return &lark.DocxTextElement{TextRun: &lark.DocxTextElementTextRun{Content: c}}
	}
	// 远程：bold 内容带尾随空格
	remote := &lark.DocxBlockText{Elements: []*lark.DocxTextElement{bold("下载 larkdown "), plain("说明")}}
	// 本地：parser 外提空格后，bold 不含空格、空格归入后续普通文本
	local := &lark.DocxBlockText{Elements: []*lark.DocxTextElement{bold("下载 larkdown"), plain(" 说明")}}
	assert.Equal(t, canonicalTextContent(remote), canonicalTextContent(local))

	// 全空白的 bold 元素不产生样式标记（等价于普通空格）
	allspace := &lark.DocxBlockText{Elements: []*lark.DocxTextElement{bold(" ")}}
	justspace := &lark.DocxBlockText{Elements: []*lark.DocxTextElement{plain(" ")}}
	assert.Equal(t, canonicalTextContent(justspace), canonicalTextContent(allspace))
}

// --- blockTypeName 测试 ---

func TestBlockTypeName(t *testing.T) {
	cases := []struct {
		name     string
		bt       lark.DocxBlockType
		expected string
	}{
		{"Page", lark.DocxBlockTypePage, "Page"},
		{"Text", lark.DocxBlockTypeText, "Text"},
		{"H1", lark.DocxBlockTypeHeading1, "H1"},
		{"H9", lark.DocxBlockTypeHeading9, "H9"},
		{"Code", lark.DocxBlockTypeCode, "Code"},
		{"Image", lark.DocxBlockTypeImage, "Image"},
		{"File", lark.DocxBlockTypeFile, "File"},
		{"Todo", lark.DocxBlockTypeTodo, "Todo"},
		{"Board", lark.DocxBlockTypeBoard, "Board"},
		{"Table", lark.DocxBlockTypeTable, "Table"},
		{"Divider", lark.DocxBlockTypeDivider, "Divider"},
		{"Unknown999", lark.DocxBlockType(999), "Type(999)"},
		{"Unknown0", lark.DocxBlockType(0), "Type(0)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, blockTypeName(tc.bt))
		})
	}
}

// --- blockTextPreview 测试 ---

func makeTextBlock(content string) *lark.DocxBlock {
	return &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: content},
			}},
		},
	}
}

func TestBlockTextPreview(t *testing.T) {
	t.Run("nil block", func(t *testing.T) {
		assert.Equal(t, "", blockTextPreview(nil, 10))
	})

	t.Run("image block (no text)", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeImage,
			Image:     &lark.DocxBlockImage{Token: "x"},
		}
		assert.Equal(t, "", blockTextPreview(block, 10))
	})

	t.Run("divider block (no text)", func(t *testing.T) {
		block := &lark.DocxBlock{BlockType: lark.DocxBlockTypeDivider}
		assert.Equal(t, "", blockTextPreview(block, 10))
	})

	t.Run("empty elements", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeText,
			Text:      &lark.DocxBlockText{Elements: []*lark.DocxTextElement{}},
		}
		assert.Equal(t, "", blockTextPreview(block, 10))
	})

	t.Run("short text no truncation", func(t *testing.T) {
		assert.Equal(t, "hello", blockTextPreview(makeTextBlock("hello"), 10))
	})

	t.Run("exact length no truncation", func(t *testing.T) {
		assert.Equal(t, "hello", blockTextPreview(makeTextBlock("hello"), 5))
	})

	t.Run("truncation with ellipsis", func(t *testing.T) {
		assert.Equal(t, "hello...", blockTextPreview(makeTextBlock("hello world test"), 5))
	})

	t.Run("multiple TextRuns concatenated", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeText,
			Text: &lark.DocxBlockText{
				Elements: []*lark.DocxTextElement{
					{TextRun: &lark.DocxTextElementTextRun{Content: "aaa"}},
					{TextRun: &lark.DocxTextElementTextRun{Content: "bbb"}},
				},
			},
		}
		assert.Equal(t, "aaabbb", blockTextPreview(block, 10))
	})

	t.Run("multiple TextRuns with truncation", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeText,
			Text: &lark.DocxBlockText{
				Elements: []*lark.DocxTextElement{
					{TextRun: &lark.DocxTextElementTextRun{Content: "aaa"}},
					{TextRun: &lark.DocxTextElementTextRun{Content: "bbb"}},
				},
			},
		}
		assert.Equal(t, "aaab...", blockTextPreview(block, 4))
	})

	t.Run("unicode rune-safe truncation", func(t *testing.T) {
		assert.Equal(t, "你好世...", blockTextPreview(makeTextBlock("你好世界测试"), 3))
	})

	t.Run("unicode exact length", func(t *testing.T) {
		assert.Equal(t, "你好", blockTextPreview(makeTextBlock("你好"), 2))
	})

	t.Run("heading block routes through getBlockText", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeHeading1,
			Heading1: &lark.DocxBlockText{
				Elements: []*lark.DocxTextElement{{
					TextRun: &lark.DocxTextElementTextRun{Content: "Title"},
				}},
			},
		}
		assert.Equal(t, "Title", blockTextPreview(block, 10))
	})

	t.Run("code block", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeCode,
			Code: &lark.DocxBlockText{
				Elements: []*lark.DocxTextElement{{
					TextRun: &lark.DocxTextElementTextRun{Content: "fmt.Println()"},
				}},
			},
		}
		assert.Equal(t, "fmt.P...", blockTextPreview(block, 5))
	})

	t.Run("maxLen=0", func(t *testing.T) {
		assert.Equal(t, "...", blockTextPreview(makeTextBlock("hello"), 0))
	})
}

// --- classifyReplaceKind 测试 ---

func TestClassifyReplaceKind(t *testing.T) {
	cases := []struct {
		name       string
		remoteType lark.DocxBlockType
		localType  lark.DocxBlockType
		expected   string
	}{
		{"both image", lark.DocxBlockTypeImage, lark.DocxBlockTypeImage, "image"},
		{"both file", lark.DocxBlockTypeFile, lark.DocxBlockTypeFile, "file"},
		{"remote code local code", lark.DocxBlockTypeCode, lark.DocxBlockTypeCode, "code"},
		{"remote code local text", lark.DocxBlockTypeCode, lark.DocxBlockTypeText, "code"},
		{"remote todo local todo", lark.DocxBlockTypeTodo, lark.DocxBlockTypeTodo, "todo"},
		{"remote todo local text", lark.DocxBlockTypeTodo, lark.DocxBlockTypeText, "todo"},
		{"both text", lark.DocxBlockTypeText, lark.DocxBlockTypeText, "text"},
		{"both heading", lark.DocxBlockTypeHeading1, lark.DocxBlockTypeHeading1, "text"},
		{"image vs file", lark.DocxBlockTypeImage, lark.DocxBlockTypeFile, "text"},
		{"file vs image", lark.DocxBlockTypeFile, lark.DocxBlockTypeImage, "text"},
		{"text vs image", lark.DocxBlockTypeText, lark.DocxBlockTypeImage, "text"},
		{"divider vs divider", lark.DocxBlockTypeDivider, lark.DocxBlockTypeDivider, "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := &lark.DocxBlock{BlockType: tc.remoteType}
			local := &lark.DocxBlock{BlockType: tc.localType}
			assert.Equal(t, tc.expected, classifyReplaceKind(remote, local))
		})
	}
}

// --- findImagePath 测试 ---

func TestFindImagePath(t *testing.T) {
	result := &ConvertResult{
		ImageIndices: []int{0, 3, 5},
		ImagePaths:   []string{"a.png", "b.png", "c.png"},
	}
	assert.Equal(t, "a.png", findImagePath(result, 0))
	assert.Equal(t, "b.png", findImagePath(result, 3))
	assert.Equal(t, "c.png", findImagePath(result, 5))
	assert.Equal(t, "", findImagePath(result, 2))

	empty := &ConvertResult{}
	assert.Equal(t, "", findImagePath(empty, 0))

	single := &ConvertResult{
		ImageIndices: []int{7},
		ImagePaths:   []string{"/path/to/img.jpg"},
	}
	assert.Equal(t, "/path/to/img.jpg", findImagePath(single, 7))
	assert.Equal(t, "", findImagePath(single, 0))
}

// --- findFilePath 测试 ---

func TestFindFilePath(t *testing.T) {
	result := &ConvertResult{
		FileIndices: []int{1, 4, 8},
		FilePaths:   []string{"doc.pdf", "sheet.xlsx", "note.txt"},
	}
	assert.Equal(t, "doc.pdf", findFilePath(result, 1))
	assert.Equal(t, "sheet.xlsx", findFilePath(result, 4))
	assert.Equal(t, "note.txt", findFilePath(result, 8))
	assert.Equal(t, "", findFilePath(result, 3))

	empty := &ConvertResult{}
	assert.Equal(t, "", findFilePath(empty, 0))

	single := &ConvertResult{
		FileIndices: []int{2},
		FilePaths:   []string{"/files/readme.md"},
	}
	assert.Equal(t, "/files/readme.md", findFilePath(single, 2))
	assert.Equal(t, "", findFilePath(single, 5))
}
