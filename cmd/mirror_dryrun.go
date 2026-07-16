package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
)

// remoteEntry 是 mirror --dry-run 轻量遍历得到的一个远端节点（不含块内容）。
type remoteEntry struct {
	title   string
	relPath string   // 相对镜像根的目录
	objType string   // docx / sheet / file / mindnote / bitable
	tokens  []string // NodeToken + ObjToken（prune 判定与 seen 同双口径）
}

// handleMirrorDryRun 预览镜像同步：只遍历远端节点列表（每层一次 API，不拉块内容），
// 对照本地 .md 的 frontmatter source token 标注 新增/已有，并列出 prune 候选；不写任何文件。
// 「已有」文档内容是否有变化需实际同步（拉块）才能判断，dry-run 不评估。
func handleMirrorDryRun(ctx context.Context, client *core.Client,
	outputDir, url string, parsed *utils.ParsedUrl, follow bool) error {

	var entries []remoteEntry
	switch parsed.Type {
	case utils.UrlTypeFolder:
		if err := listFolderTree(ctx, client, parsed.Token, "", &entries); err != nil {
			return err
		}

	case utils.UrlTypeWikiSettings:
		if err := listWikiTree(ctx, client, parsed.Token, nil, "", &entries); err != nil {
			return err
		}

	case utils.UrlTypeWikiNode:
		isSpace, spaceID, node, err := client.ResolveWikiToken(ctx, parsed.Token)
		if err != nil {
			return err
		}
		if isSpace {
			if err := listWikiTree(ctx, client, parsed.Token, nil, "", &entries); err != nil {
				return err
			}
		} else {
			entries = append(entries, remoteEntry{
				title: node.Title, objType: node.ObjType,
				tokens: []string{node.NodeToken, node.ObjToken},
			})
			if node.HasChild {
				if err := listWikiTree(ctx, client, spaceID, &node.NodeToken, "", &entries); err != nil {
					return err
				}
			}
		}

	default:
		return fmt.Errorf("mirror 仅支持知识库/文件夹 URL；单文档请使用 larkdown download")
	}

	localTokens := collectLocalMirrorTokens(outputDir)
	remoteTokens := map[string]bool{}
	for _, e := range entries {
		for _, t := range e.tokens {
			remoteTokens[t] = true
		}
	}

	fmt.Printf("[dry-run] 镜像预览: %s → %s\n\n", url, outputDir)

	var docs, skipped int
	fmt.Println("将同步的内容:")
	for _, e := range entries {
		name := nodeDisplayName(e.title, e.tokens[0])
		rel := filepath.Join(e.relPath, name)
		switch e.objType {
		case "mindnote", "bitable":
			skipped++
			fmt.Printf("  [跳过] %s (%s, 不支持的类型)\n", rel, e.objType)
			continue
		}
		docs++
		state := "[新增]"
		for _, t := range e.tokens {
			if _, ok := localTokens[t]; ok {
				state = "[已有]"
				break
			}
		}
		fmt.Printf("  %s %s (%s)\n", state, rel, e.objType)
	}
	fmt.Printf("共 %d 项（跳过 %d 项）；[已有] 内容是否变化需实际同步才能判断\n", docs, skipped)

	pruned := dryRunPruneCandidates(localTokens, remoteTokens, filepath.Join(outputDir, refsDirName), follow)
	if len(pruned) > 0 && !mirrorOpts.noPrune {
		fmt.Printf("\n将清理（远端已删除，实际同步时移入回收站）:\n")
		for _, p := range pruned {
			fmt.Printf("  %s\n", p)
		}
	}

	fmt.Println("\n[dry-run] 未写入任何文件")
	if follow {
		fmt.Println("[dry-run] --follow 引用下载不在预览中评估（引用需拉取正文才能发现）；_refs/ 由实际同步的引用收集保护，不列入清理预览")
	}
	return nil
}

// dryRunPruneCandidates 计算清理预览候选：本地有 source token 但远端已不存在的文件（排序保证输出稳定）。
// follow 开启时排除 refsDir 下的文件——实际同步中 _refs/ 由引用收集的 token 保护（出队即入 seen），
// 而 dry-run 不拉正文、无法发现引用，列入会造成「将清理」误报。
func dryRunPruneCandidates(localTokens map[string]string, remoteTokens map[string]bool, refsDir string, follow bool) []string {
	var pruned []string
	prefix := refsDir + string(filepath.Separator)
	for token, path := range localTokens {
		if remoteTokens[token] {
			continue
		}
		if follow && strings.HasPrefix(path, prefix) {
			continue
		}
		pruned = append(pruned, path)
	}
	sort.Strings(pruned)
	return pruned
}

// listWikiTree 串行遍历 Wiki 子树的节点列表（每层一次 API），收集到 out。
func listWikiTree(ctx context.Context, client *core.Client,
	spaceID string, parentNodeToken *string, relPath string, out *[]remoteEntry) error {

	nodes, err := client.GetWikiNodeList(ctx, spaceID, parentNodeToken)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		*out = append(*out, remoteEntry{
			title: n.Title, relPath: relPath, objType: n.ObjType,
			tokens: []string{n.NodeToken, n.ObjToken},
		})
		if n.HasChild {
			childPath := filepath.Join(relPath, nodeDisplayName(n.Title, n.NodeToken))
			if err := listWikiTree(ctx, client, spaceID, &n.NodeToken, childPath, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// listFolderTree 串行遍历云盘文件夹树，收集到 out（folder 自身不算条目）。
func listFolderTree(ctx context.Context, client *core.Client,
	folderToken, relPath string, out *[]remoteEntry) error {

	files, err := client.GetDriveFolderFileList(ctx, nil, &folderToken)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Type == "folder" {
			if err := listFolderTree(ctx, client, f.Token, filepath.Join(relPath, f.Name), out); err != nil {
				return err
			}
			continue
		}
		*out = append(*out, remoteEntry{
			title: f.Name, relPath: relPath, objType: f.Type,
			tokens: []string{f.Token},
		})
	}
	return nil
}

// collectLocalMirrorTokens 遍历镜像目录下的 .md，按 frontmatter source token 建
// token → 路径 映射（与 PruneStaleMirrorFiles 相同口径；无 source 的文件如 CLAUDE.md 自动忽略）。
func collectLocalMirrorTokens(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, _, err := core.ParseFrontMatter(string(content))
		if err != nil || fm == nil || fm.Source == "" {
			return nil
		}
		if token := core.ExtractTokenFromURL(fm.Source); token != "" {
			out[token] = path
		}
		return nil
	})
	return out
}
