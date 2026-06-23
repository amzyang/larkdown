package core

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// InlineMath 是 inline 公式的 AST 节点
type InlineMath struct {
	ast.BaseInline
	Content string
}

var kindInlineMath = ast.NewNodeKind("InlineMath")

func (n *InlineMath) Kind() ast.NodeKind { return kindInlineMath }
func (n *InlineMath) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// MathBlock 是块级公式的 AST 节点
type MathBlock struct {
	ast.BaseBlock
	Content string
	lines   []text.Segment
}

var kindMathBlock = ast.NewNodeKind("MathBlock")

func (n *MathBlock) Kind() ast.NodeKind { return kindMathBlock }
func (n *MathBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}
func (n *MathBlock) IsRaw() bool { return true }

// MathExtension 是 goldmark 扩展
type MathExtension struct{}

func (e *MathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(
			util.Prioritized(&inlineMathParser{}, 500),
		),
		parser.WithBlockParsers(
			// 需要比 paragraph parser (优先级 ~200) 更高的优先级
			util.Prioritized(&blockMathParser{}, 100),
		),
	)
}

// --- inline math parser: $...$ ---

type inlineMathParser struct{}

func (p *inlineMathParser) Trigger() []byte { return []byte{'$'} }

func (p *inlineMathParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine()
	if len(line) == 0 || line[0] != '$' {
		return nil
	}
	// 不能是 $$ (block math)
	if len(line) > 1 && line[1] == '$' {
		return nil
	}

	// 严格模式：$ 前必须是行首、空白或标点
	pos := seg.Start
	src := block.Source()
	if pos > 0 {
		prev := src[pos-1]
		if !isSpaceOrPunct(prev) {
			return nil
		}
	}

	// 内容不能以空格开头
	if len(line) > 1 && line[1] == ' ' {
		return nil
	}

	// 找闭合 $
	for i := 1; i < len(line); i++ {
		if line[i] == '$' {
			// 闭合 $ 前不能以空格结尾
			if line[i-1] == ' ' {
				return nil
			}
			// 闭合 $ 后必须是行尾、空白或标点
			afterPos := seg.Start + i + 1
			if afterPos < len(src) {
				next := src[afterPos]
				if !isSpaceOrPunct(next) {
					continue
				}
			}
			content := string(line[1:i])
			if content == "" {
				return nil
			}
			block.Advance(i + 1)
			return &InlineMath{Content: content}
		}
	}
	return nil
}

func isSpaceOrPunct(b byte) bool {
	if b >= 0x80 {
		return true // 多字节字符（CJK 文本、Unicode 标点等）
	}
	switch b {
	case ' ', '\t', '\n', '\r',
		'.', ',', ';', ':', '!', '?',
		'(', ')', '[', ']', '{', '}',
		'"', '\'', '`', '-', '/', '\\',
		'<', '>', '|', '~', '#', '%', '&', '*', '+', '=', '^', '_':
		return true
	}
	return false
}

// --- block math parser: $$...$$ ---

type blockMathParser struct{}

func (p *blockMathParser) Trigger() []byte { return []byte{'$'} }

func (p *blockMathParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) < 2 || trimmed[0] != '$' || trimmed[1] != '$' {
		return nil, parser.NoChildren
	}

	// $$ 后面的内容（去除换行）
	rest := bytes.TrimRight(trimmed[2:], "\n\r")

	// 同行关闭: $$content$$
	if len(rest) >= 2 && rest[len(rest)-2] == '$' && rest[len(rest)-1] == '$' {
		content := string(rest[:len(rest)-2])
		return &MathBlock{Content: content}, parser.NoChildren
	}

	// $$ 后面不应有其他非空内容（除了同行关闭的情况）
	if len(bytes.TrimSpace(rest)) > 0 {
		return nil, parser.NoChildren
	}

	return &MathBlock{}, parser.NoChildren
}

func (p *blockMathParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, seg := reader.PeekLine()
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) >= 2 && trimmed[0] == '$' && trimmed[1] == '$' {
		reader.Advance(seg.Len())
		return parser.Close
	}
	mathNode := node.(*MathBlock)
	mathNode.lines = append(mathNode.lines, seg)
	return parser.Continue | parser.NoChildren
}

func (p *blockMathParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	mathNode := node.(*MathBlock)
	source := reader.Source()
	var buf bytes.Buffer
	for _, seg := range mathNode.lines {
		buf.Write(source[seg.Start:seg.Stop])
	}
	content := buf.String()
	if len(content) > 0 && content[len(content)-1] == '\n' {
		content = content[:len(content)-1]
	}
	mathNode.Content = content
}

func (p *blockMathParser) CanInterruptParagraph() bool { return true }
func (p *blockMathParser) CanAcceptIndentedLine() bool { return false }
