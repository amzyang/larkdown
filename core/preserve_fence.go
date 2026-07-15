package core

import (
	"bytes"
	"sort"
	"strings"

	"github.com/chyroc/lark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

// localFence 描述 markdown 中一个普通代码块围栏（mermaid/plantuml 走特殊通道，不收集）
type localFence struct {
	info      string                // 完整信息串（可含 title=x 等附加属性），无信息串时为空
	lang      lark.DocxCodeLanguage // 上传等价枚举（未知语言与 newCodeBlock 一致回落 PlainText）
	content   string
	infoSeg   *gmtext.Segment // fresh 侧改写用；Info 缺失时为 nil
	lineStart int             // 首行内容起始偏移（裸围栏插入定位用），空代码块为 -1
	matched   bool
}

func collectFences(source []byte) []*localFence {
	doc := newMarkdownParser().Parser().Parse(gmtext.NewReader(source))
	var fences []*localFence
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		fcb, ok := n.(*ast.FencedCodeBlock)
		if !ok {
			return ast.WalkContinue, nil
		}
		lang := strings.ToLower(fencedCodeLang(fcb, source))
		if lang == "mermaid" || lang == "plantuml" {
			return ast.WalkContinue, nil
		}
		f := &localFence{lineStart: -1}
		if fcb.Info != nil {
			seg := fcb.Info.Segment
			f.info = string(seg.Value(source))
			f.infoSeg = &seg
		}
		langID, known := MdStr2DocxCodeLang[lang]
		if !known {
			langID = lark.DocxCodeLanguagePlainText
		}
		f.lang = langID
		lines := fcb.Lines()
		var sb strings.Builder
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			sb.Write(seg.Value(source))
		}
		f.content = sb.String()
		if lines.Len() > 0 {
			f.lineStart = lines.At(0).Start
		}
		fences = append(fences, f)
		return ast.WalkContinue, nil
	})
	return fences
}

// PreserveLocalFenceInfo 在 download 覆写本地文件前，把 fresh（新生成内容）中
// 与 local（旧本地内容）语言枚举等价的代码块围栏信息串改回本地拼写，
// 使 jsonc→JSON→json 这类 round-trip 不产生拼写噪音（issue #7）。
// 等价判定与上传侧一致：双方按 MdStr2DocxCodeLang 归一到同一枚举即视为无变更。
func PreserveLocalFenceInfo(local, fresh string) string {
	freshSrc := []byte(fresh)
	freshFences := collectFences(freshSrc)
	if len(freshFences) == 0 {
		return fresh
	}
	localFences := collectFences([]byte(local))
	if len(localFences) == 0 {
		return fresh
	}

	type pair struct{ f, l *localFence }
	var pairs []pair

	// 第一趟：枚举 + 内容精确锚定（文档序单调），远端插入/删除块不会错位
	next := 0
	for _, f := range freshFences {
		for j := next; j < len(localFences); j++ {
			l := localFences[j]
			if l.lang == f.lang && l.content == f.content {
				l.matched, f.matched = true, true
				pairs = append(pairs, pair{f, l})
				next = j + 1
				break
			}
		}
	}

	// 第二趟：剩余围栏按同枚举 FIFO 配对，覆盖「内容被编辑但语言未变」的块
	queues := map[lark.DocxCodeLanguage][]*localFence{}
	for _, l := range localFences {
		if !l.matched {
			queues[l.lang] = append(queues[l.lang], l)
		}
	}
	for _, f := range freshFences {
		if f.matched {
			continue
		}
		if q := queues[f.lang]; len(q) > 0 {
			pairs = append(pairs, pair{f, q[0]})
			queues[f.lang] = q[1:]
		}
	}

	type edit struct {
		start, stop int
		text        string
	}
	var edits []edit
	for _, p := range pairs {
		if p.l.info == p.f.info {
			continue
		}
		switch {
		case p.f.infoSeg != nil:
			edits = append(edits, edit{p.f.infoSeg.Start, p.f.infoSeg.Stop, p.l.info})
		case p.l.info != "" && p.f.lineStart > 0:
			// 裸围栏：开围栏行的换行符 = 首行内容前最近的 '\n'（引用/列表前缀下同样成立）
			if nl := bytes.LastIndexByte(freshSrc[:p.f.lineStart], '\n'); nl >= 0 {
				edits = append(edits, edit{nl, nl, p.l.info})
			}
		}
	}
	if len(edits) == 0 {
		return fresh
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := freshSrc
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.text), out[e.stop:]...)...)
	}
	return string(out)
}
