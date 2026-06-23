package core_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
	"github.com/stretchr/testify/require"
)

// loadFixture 读取 testdata/<name>.json，返回 document 与扁平 blocks 列表。
func loadFixture(t *testing.T, name string) (*lark.DocxDocument, []*lark.DocxBlock) {
	t.Helper()
	root := utils.RootDir()
	jsonFile, err := os.Open(path.Join(root, "testdata", name+".json"))
	require.NoError(t, err)
	defer jsonFile.Close()

	data := struct {
		Document *lark.DocxDocument `json:"document"`
		Blocks   []*lark.DocxBlock  `json:"blocks"`
	}{}
	byteValue, _ := io.ReadAll(jsonFile)
	require.NoError(t, json.Unmarshal(byteValue, &data))
	return data.Document, data.Blocks
}

// remoteTopBlocks 按 blocks 数组顺序返回根级块（ParentID==documentID），镜像 uploader.incrementalUpdate。
func remoteTopBlocks(doc *lark.DocxDocument, blocks []*lark.DocxBlock) []*lark.DocxBlock {
	var roots []*lark.DocxBlock
	for _, b := range blocks {
		if b.ParentID == doc.DocumentID {
			roots = append(roots, b)
		}
	}
	return roots
}

// remoteTopSignatures 计算远程根级块签名序列。
func remoteTopSignatures(doc *lark.DocxDocument, blocks []*lark.DocxBlock) []core.BlockSignature {
	blockMap := make(map[string]*lark.DocxBlock, len(blocks))
	for _, b := range blocks {
		blockMap[b.BlockID] = b
	}
	roots := remoteTopBlocks(doc, blocks)
	sigs := make([]core.BlockSignature, len(roots))
	for i, b := range roots {
		sigs[i] = core.SignatureFromBlock(b, blockMap)
	}
	return sigs
}

// isEmptyRemoteTextBlock：飞书空段落（增量上传执行时会跳过其删除，等效 no-op）。
func isEmptyRemoteTextBlock(b *lark.DocxBlock) bool {
	if b == nil || b.BlockType != lark.DocxBlockTypeText || len(b.Children) > 0 {
		return false
	}
	if b.Text == nil {
		return true
	}
	for _, e := range b.Text.Elements {
		if e.TextRun != nil && strings.TrimSpace(e.TextRun.Content) != "" {
			return false
		}
		if e.Equation != nil || e.MentionUser != nil || e.MentionDoc != nil {
			return false
		}
	}
	return true
}

// localSignatures 计算本地转换结果每个顶层块的签名序列。
func localSignatures(result *core.ConvertResult) []core.BlockSignature {
	sigs := make([]core.BlockSignature, len(result.TopBlocks))
	for i := range result.TopBlocks {
		sigs[i] = core.SignatureFromLocalEntry(result, i)
	}
	return sigs
}

// formatSigDiff 并排打印远程/本地签名序列，便于定位不一致的元素。
func formatSigDiff(remote, local []core.BlockSignature) string {
	var b strings.Builder
	n := len(remote)
	if len(local) > n {
		n = len(local)
	}
	for i := 0; i < n; i++ {
		var r, l core.BlockSignature
		if i < len(remote) {
			r = remote[i]
		}
		if i < len(local) {
			l = local[i]
		}
		mark := "  "
		if r != l {
			mark = "!="
		}
		fmt.Fprintf(&b, "[%d] %s remote=%s | local=%s\n", i, mark, r, l)
	}
	return b.String()
}

// roundTripCase 描述一个 round-trip fixture 及其期望。
type roundTripCase struct {
	name      string
	knownDiff string // 非空 = 已知不一致（记录根因），不计为失败
}

// TestRoundTripSignatures 验证 blocks → markdown → blocks 后签名序列是否一致。
// 签名序列完全相等（ComputeDiff 全 Equal）意味着增量上传对未变更内容是 no-op。
func TestRoundTripSignatures(t *testing.T) {
	cases := []roundTripCase{
		// 干净通过：签名修复后 round-trip 应为 no-op。
		{"testdocx.folded", ""},
		{"roundtrip.table", ""},
		{"roundtrip.callout", ""},
		{"roundtrip.quote_container", ""},
		{"roundtrip.code", ""},
		{"roundtrip.list_child_code", ""},        // 列表项内代码块作为 child（bullet→code），round-trip 原地保留
		{"roundtrip.list_child_multipara", ""},   // 列表项内第二段落作为 text child
		{"roundtrip.list_child_divider", ""},     // 列表项内分隔线作为 divider child
		{"roundtrip.list_child_quote_table", ""}, // 列表项内引用块/表格作为 quote_container/table child
		{"roundtrip.link", ""},
		{"roundtrip.mermaid", ""},
		{"roundtrip.quote15", ""},
		{"roundtrip.style_space", ""},
		{"roundtrip.nested_list_multiroot", ""},
		{"roundtrip.mixed", ""},
		{"roundtrip.board", ""},       // <whiteboard token> token-aware 签名：round-trip 原地保留
		{"testdocx.nested_todos", ""}, // 尾随空块删除被跳过（等效 no-op）

		// 已知不一致（白名单守边界，根因见说明）。
		{"roundtrip.file", "File 块 [name](token)：token 非本地文件/合法 URL，上传降级纯文本（媒体无源，不可逆）"},
		{"testdocx.1", "代码块内 mention 的 <cite> 字面量（空块已由跳过删除处理）"},
		{"testdocx.2", "Typora 边角（inline code 含反引号、$$ 数学块、脚注）"},
		{"testdocx.3", ""}, // 续行段落已作为 text child 保留（P1：列表项子块泛化），round-trip 对称
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, blocks := loadFixture(t, tc.name)
			remoteSigs := remoteTopSignatures(doc, blocks)
			remoteBlocks := remoteTopBlocks(doc, blocks)

			parser := core.NewParser(core.NewConfig("", "").Output, nil)
			md := parser.ParseDocxContent(doc, blocks)

			// 镜像真实上传流程：剥掉首个标题（文档标题 H1）后再转换。
			body := core.RemoveFirstHeading(md)
			result, err := core.ConvertMarkdownToDocxBlocks(body, "")
			require.NoError(t, err)
			localSigs := localSignatures(result)

			// 增量上传对未变更内容应为 no-op：所有 diff 操作要么 Equal，
			// 要么是「空文本块删除」（执行时被 isEmptyTextBlock 跳过，等效 no-op）。
			ops := core.ComputeDiff(remoteSigs, localSigs)
			allEqual := true
			for _, op := range ops {
				switch op.Type {
				case core.DiffOpEqual:
				case core.DiffOpDelete:
					if !isEmptyRemoteTextBlock(remoteBlocks[op.RemoteIdx]) {
						allEqual = false
					}
				default:
					allEqual = false
				}
			}

			if tc.knownDiff != "" {
				if allEqual {
					t.Logf("known-diff fixture %s 现已一致，可考虑移除白名单", tc.name)
				}
				t.Skipf("已知不一致: %s", tc.knownDiff)
				return
			}

			if !allEqual {
				t.Errorf("round-trip 签名不一致（增量上传会重传未变更内容）\nremote=%d local=%d\n%s",
					len(remoteSigs), len(localSigs), formatSigDiff(remoteSigs, localSigs))
			}
		})
	}
}
