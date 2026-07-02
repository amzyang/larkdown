package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
)

type MirrorOpts struct {
	outputDir string
	comments  bool
	force     bool
	noPrune   bool // 保留远端已不存在的本地文件（跳过清理）
}

var mirrorOpts = MirrorOpts{}

// tokenSet 收集本次同步中远端存在的文档/节点 token（并发安全，nil 接收者为 no-op）。
// 「远端存在」与下载成功与否无关：下载失败的文档远端仍在，其本地文件不应被清理。
type tokenSet struct {
	mu     sync.Mutex
	tokens map[string]bool
}

func newTokenSet() *tokenSet {
	return &tokenSet{tokens: make(map[string]bool)}
}

func (s *tokenSet) Add(token string) {
	if s == nil || token == "" {
		return
	}
	s.mu.Lock()
	s.tokens[token] = true
	s.mu.Unlock()
}

func (s *tokenSet) Has(token string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[token]
}

// handleMirrorCommand 单向只下载同步：把飞书知识库/文件夹镜像到本地目录。
// 与 download -r 的区别：输出目录本身即镜像根（不嵌套 Wiki 名子目录）、
// 固定生成索引（llms.txt / docs_map.md）与 CLAUDE.md 说明、
// 同步后清理远端已不存在的本地文档（移入回收站）。
func handleMirrorCommand(urlArg string) error {
	// Load config
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		return err
	}
	dlConfig = *config

	// 归一为绝对路径：索引 RelPath 计算与清理遍历都依赖稳定的根路径
	outputDir, err := filepath.Abs(mirrorOpts.outputDir)
	if err != nil {
		return err
	}

	// 未指定 URL 时从镜像元数据边车读取来源（重同步场景）
	url := urlArg
	if url == "" {
		m, err := core.ReadMirrorManifest(outputDir)
		if err != nil {
			return err
		}
		if m == nil || m.Source == "" {
			return fmt.Errorf("未指定 URL，且 %s 下没有 %s；首次镜像请提供知识库/文件夹 URL",
				outputDir, core.MirrorManifestFilename)
		}
		url = m.Source
		fmt.Printf("从 %s 读取镜像来源: %s\n",
			filepath.Join(outputDir, core.MirrorManifestFilename), url)
	}

	// mirror 复用 download 的全局选项：固定递归 + 关闭 diff 输出（索引单独由各同步路径生成）
	dlOpts = DownloadOpts{
		outputDir: outputDir,
		recursive: true,
		comments:  mirrorOpts.comments,
		noDiff:    true,
		force:     mirrorOpts.force,
	}

	ctx := context.Background()
	client := createClientFromConfig(ctx, config, configPath)

	parsed, err := utils.ParseFeishuUrl(url)
	if err != nil {
		return err
	}

	seen := newTokenSet()
	var rootName string
	var syncErr error

	switch parsed.Type {
	case utils.UrlTypeFolder:
		rootName = filepath.Base(outputDir)
		syncErr = downloadDocuments(ctx, client, url, seen, true)

	case utils.UrlTypeWikiSettings:
		rootName, syncErr = mirrorWikiSpace(ctx, client, parsed.PrefixURL, parsed.Token, seen)
		if rootName == "" {
			return syncErr
		}

	case utils.UrlTypeWikiNode:
		isSpace, spaceID, node, err := client.ResolveWikiToken(ctx, parsed.Token)
		if err != nil {
			return err
		}
		if isSpace {
			rootName, syncErr = mirrorWikiSpace(ctx, client, parsed.PrefixURL, parsed.Token, seen)
			if rootName == "" {
				return syncErr
			}
		} else {
			rootName, syncErr = mirrorWikiNode(ctx, client, url, parsed.PrefixURL, spaceID, node, seen)
		}

	default:
		return fmt.Errorf("mirror 仅支持知识库/文件夹 URL；单文档请使用 larkdown download")
	}

	// 写入 CLAUDE.md 说明与镜像元数据（部分失败也写：索引/说明反映已同步内容）
	claudePath := filepath.Join(outputDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(core.GenerateMirrorClaudeMd(rootName, url)), 0o644); err != nil {
		return err
	}
	fmt.Printf("Generated %s\n", claudePath)
	if err := core.WriteMirrorManifest(outputDir, &core.MirrorManifest{Source: url}); err != nil {
		return err
	}

	// 清理远端已不存在的本地文档；部分失败时跳过（远端列表可能不完整，避免误删）
	if syncErr != nil {
		log.Printf("警告: 部分内容同步失败，本次跳过陈旧文件清理: %v", syncErr)
		return syncErr
	}
	if mirrorOpts.noPrune {
		return nil
	}
	removed, err := core.PruneStaleMirrorFiles(outputDir, seen.Has)
	if err != nil {
		return fmt.Errorf("清理陈旧文件失败: %w", err)
	}
	for _, path := range removed {
		fmt.Printf("远端已删除，移入回收站: %s\n", path)
	}
	fmt.Printf("镜像同步完成: %s\n", outputDir)
	return nil
}

// mirrorWikiSpace 镜像整个知识库空间到 dlOpts.outputDir（目录本身即镜像根）。
// 返回知识库名称（rootName 为空表示未开始下载，调用方直接返回错误）。
func mirrorWikiSpace(ctx context.Context, client *core.Client,
	prefixURL, spaceID string, seen *tokenSet) (string, error) {

	wikiName, err := client.GetWikiName(ctx, spaceID)
	if err != nil {
		return "", err
	}
	if wikiName == "" {
		wikiName = spaceID
	}

	docsIndex := core.NewDocsIndex(wikiName, dlOpts.outputDir)
	syncErr := downloadWikiNodeRecursive(ctx, client, prefixURL, spaceID, "", dlOpts.outputDir, nil, docsIndex, seen)
	if err := core.WriteDocsIndex(docsIndex, dlOpts.outputDir); err != nil {
		return wikiName, err
	}
	return wikiName, syncErr
}

// mirrorWikiNode 镜像单个 Wiki 节点及其子树到 dlOpts.outputDir（目录本身即镜像根）。
func mirrorWikiNode(ctx context.Context, client *core.Client,
	url, prefixURL, spaceID string, node *lark.GetWikiNodeRespNode, seen *tokenSet) (string, error) {

	rootName := nodeDisplayName(node.Title, node.NodeToken)
	docsIndex := core.NewDocsIndex(rootName, dlOpts.outputDir)

	// 先下载根节点自身（不递归）
	seen.Add(node.NodeToken)
	rootOpts := DownloadOpts{outputDir: dlOpts.outputDir, comments: dlOpts.comments, noDiff: dlOpts.noDiff, force: dlOpts.force}
	meta, syncErr := downloadDocument(ctx, client, url, &rootOpts)
	if syncErr != nil {
		log.Printf("警告: 根节点下载失败，继续下载子节点: %v", syncErr)
	} else if meta != nil {
		docsIndex.AddDoc(*meta, dlOpts.outputDir)
	}

	// 递归下载子节点（无子节点时列表为空，自然退化为单文档镜像）
	childErr := downloadWikiNodeRecursive(ctx, client, prefixURL, spaceID, "", dlOpts.outputDir, &node.NodeToken, docsIndex, seen)
	if syncErr == nil {
		syncErr = childErr
	}

	if err := core.WriteDocsIndex(docsIndex, dlOpts.outputDir); err != nil {
		return rootName, err
	}
	return rootName, syncErr
}
