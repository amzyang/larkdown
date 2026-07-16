package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/amzyang/larkdown/core"
)

// refsDirName follow 下载的被引用文档统一存放的子目录名
const refsDirName = "_refs"

// runFollowPhase 主下载完成后执行 follow：BFS 下载正文引用的 docx/wiki 文档到 <outputDir>/_refs。
// seen 非 nil 时：已覆盖的文档跳过下载，且每个出队 token 在下载前加入 seen——
// 即使下载失败也保护对应本地文件不被 mirror prune（远端存在与下载成败无关）。
// docsIndex 非 nil 时把成功下载的文档记入「引用文档」索引节。返回 (成功数, 失败数)。
func runFollowPhase(ctx context.Context, client *core.Client, outputDir string,
	initial []core.DocRef, depth int, seen *tokenSet, docsIndex *core.DocsIndex,
	base *DownloadOpts) (int, int) {

	if len(initial) == 0 {
		return 0, 0
	}
	refsDir := filepath.Join(outputDir, refsDirName)
	fmt.Fprintf(dlProgress, "follow: 发现 %d 篇被引用文档，下载到 %s（深度 %d）\n", len(initial), refsDir, depth)

	succeeded, failed := core.FollowRefs(initial, core.FollowOptions{
		Depth:   depth,
		Skip:    seen.Has,
		OnVisit: func(r core.DocRef) { seen.Add(r.Token) },
		Fetch: func(r core.DocRef) ([]core.DocRef, error) {
			sub := core.NewRefCollector()
			opts := DownloadOpts{
				outputDir: refsDir,
				comments:  base.comments,
				noDiff:    true,
				force:     base.force,
				refs:      sub,
			}
			meta, err := downloadDocument(ctx, client, r.URL, &opts)
			if err != nil {
				return nil, err
			}
			if meta != nil && docsIndex != nil {
				docsIndex.AddRef(core.RefDoc{
					Title:     meta.Title,
					RelPath:   filepath.Join(refsDirName, meta.RelPath),
					SourceURL: r.URL,
				})
			}
			return sub.Drain(), nil
		},
		Logf: log.Printf,
	})
	fmt.Fprintf(dlProgress, "follow: 已下载 %d 篇引用文档到 %s（失败 %d 篇）\n", succeeded, refsDir, failed)
	if failed > 0 {
		dlReport.AddFailure("follow", fmt.Errorf("%d 篇引用文档下载失败", failed))
	}
	return succeeded, failed
}
