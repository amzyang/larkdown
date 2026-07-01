package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
	"github.com/pkg/errors"
)

type DownloadOpts struct {
	outputDir     string
	recursive     bool
	generateIndex bool
	comments      bool
	noDiff        bool
	force         bool // 忽略本地版本标记，强制重新下载
}

var dlOpts = DownloadOpts{}
var dlConfig core.Config

// nodeDisplayName 返回节点的显示名称，空标题时回退到 token
func nodeDisplayName(title, token string) string {
	name := utils.SanitizeFileName(strings.TrimSpace(title))
	if name != "" {
		return name
	}
	return token
}

// downloadDocument 下载单个文档，返回文档元信息（用于索引生成）
func downloadDocument(ctx context.Context, client *core.Client, url string, opts *DownloadOpts) (*core.DocMeta, error) {
	// Validate the url to download
	docType, docToken, err := utils.ValidateDocumentURL(url)
	if err != nil {
		return nil, err
	}
	fmt.Println("Captured document token:", docToken)

	urlToken := docToken // 保存 URL 中的原始 token（用于文件命名）

	// for a wiki page, we need to renew docType and docToken first
	var nodeTitle string
	var objEditTime string // Wiki 节点编辑时间（用于白板缓存）
	if docType == "wiki" {
		node, err := client.GetWikiNodeInfo(ctx, docToken)
		if err != nil {
			return nil, fmt.Errorf("GetWikiNodeInfo err: %v for %v", err, url)
		}

		docType = node.ObjType
		docToken = node.ObjToken
		nodeTitle = node.Title
		objEditTime = node.ObjEditTime
	}

	// 根据类型分流处理
	switch docType {
	case "docs":
		return nil, errors.Errorf(
			`Feishu Docs is no longer supported. ` +
				`Please refer to the Readme/Release for v1_support.`)
	case "file":
		return nil, downloadFile(ctx, client, docToken, nodeTitle, opts.outputDir, docType)
	case "sheet":
		return nil, downloadSheet(ctx, client, docToken, nodeTitle, opts.outputDir, url)
	case "mindnote", "bitable":
		fmt.Printf("跳过不支持的文档类型 '%s'\n", docType)
		return nil, nil
	case "docx":
		// 继续原有的 docx 处理逻辑
	default:
		return nil, errors.Errorf("不支持的文档类型: %s", docType)
	}

	// 跳过未变化文档：本地已有带版本标记的下载产物，且远程版本一致 → 免拉取块与素材。
	// 仅当本地候选存在时才多打一次轻量 GetDocxDocument；全新下载零额外 API 调用。
	if !opts.force {
		localPath, localFM, localBody := core.FindLocalFileByToken(opts.outputDir, urlToken, nodeTitle, dlConfig.Output)
		if localFM != nil && localFM.Version != "" {
			if doc, err := client.GetDocxDocument(ctx, docToken); err == nil &&
				core.DownloadVersion(objEditTime, doc.RevisionID) == localFM.Version {
				fmt.Printf("未变化，跳过: %s\n", localPath)
				meta := core.LocalDocMeta(localPath, localBody)
				return &meta, nil
			}
		}
	}

	// Process the download (docx type)
	docx, blocks, err := client.GetDocxContent(ctx, docToken)
	if err != nil {
		return nil, err
	}

	parser := core.NewParser(dlConfig.Output, client)
	parser.SetContext(ctx)
	parser.SetOutputDir(filepath.Join(opts.outputDir, dlConfig.Output.ImageDir))
	parser.SetPrefixURL(utils.ExtractPrefixURL(url))
	parser.SetObjEditTime(objEditTime) // Wiki 文档设置 objEditTime 用于白板缓存

	// 解析 @用户 显示名
	if userIDs := core.CollectMentionUserIDs(blocks); len(userIDs) > 0 {
		parser.SetUserNames(client.ResolveUserNames(ctx, userIDs, core.UserIDType()))
	}

	title := docx.Title
	markdown := parser.ParseDocxContent(docx, blocks)

	// 注入文档 token 和域名前缀，用于 cookie 回退的按文档隔离和 Referer 构建
	ctx = core.WithDocToken(ctx, docToken)
	ctx = core.WithPrefixURL(ctx, utils.ExtractPrefixURL(url))

	var failedAssets []string
	var hasPermErr bool
	if !dlConfig.Output.SkipImgDownload {
		for _, imgToken := range parser.ImgTokens {
			localLink, err := client.DownloadImage(
				ctx, imgToken, filepath.Join(opts.outputDir, dlConfig.Output.ImageDir),
			)
			if err != nil {
				log.Printf("警告: 图片下载失败，跳过 %s: %v", imgToken, err)
				failedAssets = append(failedAssets, imgToken)
				if isPermissionError(err) {
					hasPermErr = true
				}
				continue
			}
			markdown = strings.Replace(markdown, imgToken, localLink, 1)
		}
		for _, fileToken := range parser.FileTokens {
			localLink, err := client.DownloadMedia(
				ctx, fileToken, filepath.Join(opts.outputDir, dlConfig.Output.ImageDir),
			)
			if err != nil {
				log.Printf("警告: 附件下载失败，跳过 %s: %v", fileToken, err)
				failedAssets = append(failedAssets, fileToken)
				if isPermissionError(err) {
					hasPermErr = true
				}
				continue
			}
			markdown = strings.Replace(markdown, fileToken, localLink, 1)
		}
	}

	// 获取并追加评论
	if opts.comments {
		commentData, err := client.GetDocumentComments(ctx, docToken, lark.FileTypeDocx)
		if err != nil {
			log.Printf("警告: 获取评论失败，跳过: %v", err)
		} else {
			markdown += core.RenderComments(commentData)
		}
	}

	// Add Front Matter (appended at file end)
	// 版本标记用于下次下载跳过未变化文档；素材下载不完整时不落标记，保证下次重试
	version := core.DownloadVersion(objEditTime, docx.RevisionID)
	if len(failedAssets) > 0 {
		version = ""
	}
	frontMatter := core.GenerateFrontMatter(core.FrontMatter{
		Source:  url,
		Version: version,
	})
	markdown = strings.TrimRight(markdown, "\n") + "\n" + frontMatter

	// Handle the output directory and name
	if err := os.MkdirAll(opts.outputDir, 0o755); err != nil {
		return nil, err
	}

	// Compute markdown filename (使用 urlToken 确保文件名与 source URL 一致)
	mdName := core.ComputeMdFilename(title, urlToken, dlConfig.Output)

	// 检测并清理标题变更导致的旧文件
	if staleFile, err := core.FindStaleFile(opts.outputDir, mdName, urlToken); err == nil && staleFile != "" {
		utils.MoveToTrash(staleFile)
		fmt.Printf("标题变更: %s → %s (旧文件已移入回收站)\n", filepath.Base(staleFile), mdName)
	}

	outputPath := filepath.Join(opts.outputDir, mdName)

	if !opts.noDiff {
		showDiff(outputPath, markdown, dlConfig.Output.DiffStyle)
	}

	if err = os.WriteFile(outputPath, []byte(markdown), 0o644); err != nil {
		return nil, err
	}
	fmt.Printf("Downloaded markdown file to %s\n", outputPath)

	if len(failedAssets) > 0 {
		total := len(parser.ImgTokens) + len(parser.FileTokens)
		fmt.Printf("\n⚠ %d/%d 张图片/附件下载失败\n", len(failedAssets), total)
		if hasPermErr {
			fmt.Println("  可能原因: 应用或用户对文档缺少复制或者编辑权限（图片下载需要 复制 或 Edit 权限或协作者身份）")
			fmt.Println("  解决方案: 通过「分享」将文档授予应用或当前用户为协作者或可复制")
			fmt.Println("  参考: https://open.feishu.cn/document/server-docs/docs/faq#16c6475a")
		}
	}

	// 返回文档元信息
	return &core.DocMeta{
		Title:    title,
		RelPath:  mdName,
		Headings: core.BuildHeadingTree(parser.GetHeadings()),
	}, nil
}

func isPermissionError(err error) bool {
	return core.IsPermissionError(err)
}

func downloadDocuments(ctx context.Context, client *core.Client, url string) error {
	// Validate the url to download
	folderToken, err := utils.ValidateFolderURL(url)
	if err != nil {
		return err
	}
	fmt.Println("Captured folder token:", folderToken)

	// 初始化索引收集器
	var docsIndex *core.DocsIndex
	var mu sync.Mutex
	if dlOpts.generateIndex {
		docsIndex = core.NewDocsIndex(filepath.Base(dlOpts.outputDir), dlOpts.outputDir)
	}

	var firstErr error
	wg := sync.WaitGroup{}

	// Recursively go through the folder and download the documents
	var processFolder func(ctx context.Context, folderPath, folderToken string) error
	processFolder = func(ctx context.Context, folderPath, folderToken string) error {
		files, err := client.GetDriveFolderFileList(ctx, nil, &folderToken)
		if err != nil {
			return err
		}
		opts := DownloadOpts{outputDir: folderPath, comments: dlOpts.comments, noDiff: dlOpts.noDiff, force: dlOpts.force}
		for _, file := range files {
			if file.Type == "folder" {
				_folderPath := filepath.Join(folderPath, file.Name)
				if err := processFolder(ctx, _folderPath, file.Token); err != nil {
					return err
				}
			} else if file.Type == "docx" {
				// concurrently download the document
				wg.Add(1)
				go func(_url string, _folderPath string) {
					meta, err := downloadDocument(ctx, client, _url, &opts)
					if err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
					} else if meta != nil && docsIndex != nil {
						mu.Lock()
						docsIndex.AddDoc(*meta, _folderPath)
						mu.Unlock()
					}
					wg.Done()
				}(file.URL, folderPath)
			}
		}
		return nil
	}
	if err := processFolder(ctx, dlOpts.outputDir, folderToken); err != nil {
		return err
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	// 生成索引文件
	if docsIndex != nil {
		if err := core.WriteDocsIndex(docsIndex, dlOpts.outputDir); err != nil {
			return err
		}
	}

	return nil
}

func downloadWikiNodeRecursive(ctx context.Context, client *core.Client,
	prefixURL, spaceID, nodeTitle, baseOutputDir string, parentNodeToken *string,
	docsIndex *core.DocsIndex) error {

	// 输出目录为 baseOutputDir/nodeTitle
	folderPath := filepath.Join(baseOutputDir, nodeTitle)

	var firstErr error
	var maxConcurrency = 10
	wg := sync.WaitGroup{}
	semaphore := make(chan struct{}, maxConcurrency)
	var mu sync.Mutex

	collectErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	var downloadNode func(ctx context.Context, client *core.Client,
		spaceID string, folderPath string, parentNodeToken *string)
	downloadNode = func(ctx context.Context, client *core.Client,
		spaceID string, folderPath string, parentNodeToken *string) {
		nodes, err := client.GetWikiNodeList(ctx, spaceID, parentNodeToken)
		if err != nil {
			log.Printf("警告: 获取子节点列表失败，跳过该子树: %v", err)
			collectErr(err)
			return
		}
		for _, n := range nodes {
			if n.HasChild {
				_folderPath := filepath.Join(folderPath, nodeDisplayName(n.Title, n.NodeToken))
				downloadNode(ctx, client, spaceID, _folderPath, &n.NodeToken)
			}
			switch n.ObjType {
			case "docx":
				opts := DownloadOpts{outputDir: folderPath, comments: dlOpts.comments, noDiff: dlOpts.noDiff, force: dlOpts.force}
				wg.Add(1)
				semaphore <- struct{}{}
				go func(_url string, _folderPath string) {
					meta, err := downloadDocument(ctx, client, _url, &opts)
					if err != nil {
						log.Printf("警告: 文档下载失败，跳过: %v", err)
						collectErr(err)
					} else if meta != nil && docsIndex != nil {
						mu.Lock()
						docsIndex.AddDoc(*meta, _folderPath)
						mu.Unlock()
					}
					wg.Done()
					<-semaphore
				}(prefixURL+"/wiki/"+n.NodeToken, folderPath)
			case "file":
				wg.Add(1)
				semaphore <- struct{}{}
				go func(token, title, outDir string) {
					if err := downloadFile(ctx, client, token, title, outDir, "file"); err != nil {
						log.Printf("警告: 文件下载失败，跳过: %v", err)
						collectErr(err)
					}
					wg.Done()
					<-semaphore
				}(n.ObjToken, n.Title, folderPath)
			case "sheet":
				wg.Add(1)
				semaphore <- struct{}{}
				go func(token, title, outDir, sourceURL string) {
					if err := downloadSheet(ctx, client, token, title, outDir, sourceURL); err != nil {
						log.Printf("警告: 电子表格下载失败，跳过: %v", err)
						collectErr(err)
					}
					wg.Done()
					<-semaphore
				}(n.ObjToken, n.Title, folderPath, prefixURL+"/wiki/"+n.NodeToken)
			case "mindnote", "bitable":
				fmt.Printf("跳过不支持的文档类型 '%s': %s\n", n.ObjType, n.Title)
			}
		}
	}

	downloadNode(ctx, client, spaceID, folderPath, parentNodeToken)

	// 等待所有下载完成
	wg.Wait()
	if firstErr != nil {
		log.Printf("警告: 部分文档下载失败: %v", firstErr)
	}
	return nil
}

func downloadWiki(ctx context.Context, client *core.Client, url string) error {
	prefixURL, wikiToken, err := utils.ValidateWikiURL(url)
	if err != nil {
		return err
	}

	// 判断 token 是 space_id 还是 node_token
	var spaceID string
	wikiName, err := client.GetWikiName(ctx, wikiToken)
	if err == nil {
		spaceID = wikiToken
	} else {
		node, err := client.GetWikiNodeInfo(ctx, wikiToken)
		if err != nil {
			return fmt.Errorf("failed to get wiki node info: %v", err)
		}
		if node.SpaceID == "" {
			return fmt.Errorf("node does not have a space_id")
		}
		spaceID = node.SpaceID
		wikiName, err = client.GetWikiName(ctx, spaceID)
		if err != nil {
			return err
		}
	}
	if wikiName == "" {
		return fmt.Errorf("failed to GetWikiName")
	}

	sanitizedWikiName := utils.SanitizeFileName(wikiName)
	outputDir := filepath.Join(dlOpts.outputDir, sanitizedWikiName)

	var docsIndex *core.DocsIndex
	if dlOpts.generateIndex {
		docsIndex = core.NewDocsIndex(sanitizedWikiName, sanitizedWikiName)
	}

	err = downloadWikiNodeRecursive(ctx, client, prefixURL, spaceID, sanitizedWikiName, dlOpts.outputDir, nil, docsIndex)

	if docsIndex != nil {
		if err := core.WriteDocsIndex(docsIndex, outputDir); err != nil {
			return err
		}
	}

	return nil
}

func handleDownloadCommand(url string) error {
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

	// 检测参数是否为本地 .md 文件，从 frontmatter 提取源 URL
	if strings.HasSuffix(url, ".md") {
		if content, err := os.ReadFile(url); err == nil {
			fm, _, err := core.ParseFrontMatter(string(content))
			if err != nil {
				return fmt.Errorf("解析 %s 的 frontmatter 失败: %w", url, err)
			}
			if fm == nil || fm.Source == "" {
				return fmt.Errorf("%s 中未找到 source URL", url)
			}
			// 输出目录默认为 .md 文件所在目录（若用户未通过 -o 指定）
			if dlOpts.outputDir == "./" {
				dlOpts.outputDir = filepath.Dir(url)
			}
			fmt.Printf("从 %s 提取源 URL: %s\n", url, fm.Source)
			url = fm.Source
		}
	}

	ctx := context.Background()
	client := createClientFromConfig(ctx, config, configPath)

	// 解析 URL 类型
	parsed, err := utils.ParseFeishuUrl(url)
	if err != nil {
		return err
	}

	switch parsed.Type {
	case utils.UrlTypeFolder:
		if !dlOpts.recursive {
			return fmt.Errorf("下载文件夹需要指定 -r 选项")
		}
		return downloadDocuments(ctx, client, url)

	case utils.UrlTypeWikiSettings:
		if !dlOpts.recursive {
			return fmt.Errorf("下载知识库需要指定 -r 选项")
		}
		return downloadWiki(ctx, client, url)

	case utils.UrlTypeFile:
		return downloadFile(ctx, client, parsed.Token, "", dlOpts.outputDir, "file")

	case utils.UrlTypeDocx:
		// 普通 docx 文档，-r 被忽略
		_, err = downloadDocument(ctx, client, url, &dlOpts)
		return err

	case utils.UrlTypeWikiNode:
		// Wiki 节点，需要判断是 space_id 还是 node_token
		isSpace, spaceID, node, err := client.ResolveWikiToken(ctx, parsed.Token)
		if err != nil {
			return err
		}

		if isSpace {
			// 是 space_id，需要 -r
			if !dlOpts.recursive {
				return fmt.Errorf("下载知识库需要指定 -r 选项")
			}
			return downloadWiki(ctx, client, url)
		}

		// 是 node_token
		if dlOpts.recursive && node.HasChild {
			displayName := nodeDisplayName(node.Title, node.NodeToken)
			outputDir := filepath.Join(dlOpts.outputDir, displayName)
			var docsIndex *core.DocsIndex
			if dlOpts.generateIndex {
				docsIndex = core.NewDocsIndex(displayName, outputDir)
			}

			// 先下载根节点自身（不递归）
			rootOpts := DownloadOpts{outputDir: dlOpts.outputDir, comments: dlOpts.comments, noDiff: dlOpts.noDiff, force: dlOpts.force}
			meta, rootErr := downloadDocument(ctx, client, url, &rootOpts)
			if rootErr != nil {
				log.Printf("警告: 根节点下载失败，继续下载子节点: %v", rootErr)
			} else if meta != nil && docsIndex != nil {
				docsIndex.AddDoc(*meta, dlOpts.outputDir)
			}

			// 递归下载子节点
			downloadWikiNodeRecursive(ctx, client, parsed.PrefixURL, spaceID, displayName, dlOpts.outputDir, &node.NodeToken, docsIndex)
			if docsIndex != nil {
				if writeErr := core.WriteDocsIndex(docsIndex, outputDir); writeErr != nil {
					return writeErr
				}
			}
			return nil
		}

		// 下载单个 Wiki 节点文档
		_, err = downloadDocument(ctx, client, url, &dlOpts)
		return err

	default:
		return fmt.Errorf("unsupported URL type: %s", url)
	}
}

func downloadFile(ctx context.Context, client *core.Client, fileToken, title, outputDir, objType string) error {
	// Download the file using DownloadFileWithMeta (with fallback to placeholder)
	filePath, err := client.DownloadFileWithMeta(ctx, fileToken, outputDir, objType, title)
	if err != nil {
		return fmt.Errorf("failed to download file %s: %v", title, err)
	}
	fmt.Printf("Downloaded file to %s\n", filePath)
	return nil
}

// downloadSheet 下载电子表格为 XLSX 文件，无导出权限时降级为 Markdown。
// 注意：电子表格是飞书的在线协作文档，不是云空间中的静态文件，
// 因此不能使用 DownloadDriveFile API 直接下载。必须通过 ExportTask
// 流程将其异步导出为 XLSX/CSV 格式后再下载。
//
// 当 owner 在文档安全设置里关闭了下载时（错误码 1069902），
// ExportSheet 会失败；此时降级用 BatchGetSheetValue 把所有工作表
// 拉成单个 .md 文件，保留多行单元格和合并信息，并在末尾写入
// source URL frontmatter（与 docx 下载一致）。
func downloadSheet(ctx context.Context, client *core.Client, sheetToken, title, outputDir, sourceURL string) error {
	filePath, err := client.ExportSheet(ctx, sheetToken, outputDir, title)
	if err == nil {
		fmt.Printf("已下载电子表格: %s\n", filePath)
		return nil
	}
	if !core.IsPermissionError(err) {
		return fmt.Errorf("failed to export sheet %s: %v", title, err)
	}
	log.Printf("sheet %q 无 xlsx 导出权限（owner 已禁用），降级 Markdown", title)

	mdPath, mdErr := client.ExportSheetAsMarkdown(ctx, sheetToken, outputDir, title, sourceURL)
	if mdErr != nil {
		return fmt.Errorf("sheet %q xlsx 与 Markdown 均失败: xlsx=%v; md=%v", title, err, mdErr)
	}
	fmt.Printf("已导出为 Markdown: %s（源文档禁止 xlsx 导出）\n", mdPath)
	return nil
}
