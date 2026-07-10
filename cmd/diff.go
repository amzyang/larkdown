package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/pmezard/go-difflib/difflib"
)

var diffMu sync.Mutex

// writeDiffOutput 输出 diff 文本，TTY 时着色，pipeline 时输出纯文本
func writeDiffOutput(text, style string) {
	if fileInfo, _ := os.Stdout.Stat(); fileInfo.Mode()&os.ModeCharDevice != 0 {
		if style == "" {
			style = "monokai"
		}
		quick.Highlight(os.Stdout, text, "diff", "terminal16m", style)
	} else {
		fmt.Print(text)
	}
}

func showDiff(outputPath, newContent, style string) {
	oldContent, err := os.ReadFile(outputPath)
	if err != nil {
		fmt.Printf("New file: %s\n", outputPath)
		return
	}

	oldStr := string(oldContent)
	if oldStr == newContent {
		fmt.Printf("No changes: %s\n", outputPath)
		return
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldStr),
		B:        difflib.SplitLines(newContent),
		FromFile: outputPath,
		ToFile:   outputPath,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return
	}

	diffMu.Lock()
	defer diffMu.Unlock()
	writeDiffOutput(text, style)
}

// fetchRemoteMarkdown 从飞书获取远端文档并转换为 markdown，不下载图片、不追加 frontmatter
func fetchRemoteMarkdown(ctx context.Context, client *core.Client, url string, outputConfig core.OutputConfig) (string, error) {
	docType, docToken, err := utils.ValidateDocumentURL(url)
	if err != nil {
		return "", err
	}

	if docType == "wiki" {
		node, err := client.GetWikiNodeInfo(ctx, docToken)
		if err != nil {
			return "", fmt.Errorf("GetWikiNodeInfo err: %v for %v", err, url)
		}
		docType = node.ObjType
		docToken = node.ObjToken
	}

	if docType != "docx" {
		return "", fmt.Errorf("diff 仅支持 docx 类型，当前类型: %s", docType)
	}

	docx, blocks, err := client.GetDocxContent(ctx, docToken)
	if err != nil {
		return "", err
	}

	parser := core.NewParser(outputConfig, client)
	parser.SetContext(ctx)
	parser.SetPrefixURL(utils.ExtractPrefixURL(url))

	if userIDs := core.CollectMentionUserIDs(blocks); len(userIDs) > 0 {
		parser.SetUserNames(client.ResolveUserNames(ctx, userIDs, core.UserIDType()))
	}

	return parser.ParseDocxContent(docx, blocks), nil
}

var diffOpts struct {
	invert bool
}

func handleDiffCommand(filePath string) error {
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	fm, localBody, err := core.ParseFrontMatter(string(content))
	if err != nil {
		return fmt.Errorf("解析 %s 的 frontmatter 失败: %w", filePath, err)
	}
	if fm == nil || fm.Source == "" {
		return fmt.Errorf("%s 中未找到 source URL", filePath)
	}

	ctx := context.Background()
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}

	// 规范化本地 markdown：通过 md→DocxBlocks→md 管线消除格式差异
	normalizedLocal, err := core.NormalizeMarkdown(localBody, filepath.Dir(filePath))
	if err != nil {
		return fmt.Errorf("规范化本地文件失败: %w", err)
	}

	fmt.Printf("正在获取远端文档: %s\n", fm.Source)
	remoteMarkdown, err := fetchRemoteMarkdown(ctx, client, fm.Source, config.Output)
	if err != nil {
		return err
	}

	// 统一 trim 尾部空白，确保单个换行结尾
	normalizedLocal = strings.TrimRight(normalizedLocal, "\n") + "\n"
	remoteMarkdown = strings.TrimRight(remoteMarkdown, "\n") + "\n"

	if normalizedLocal == remoteMarkdown {
		fmt.Printf("No changes: %s\n", filePath)
		return nil
	}

	baseName := filepath.Base(filePath)
	fromLines, toLines := difflib.SplitLines(normalizedLocal), difflib.SplitLines(remoteMarkdown)
	fromFile, toFile := "a/"+baseName+" (local)", "b/"+baseName+" (remote)"
	if diffOpts.invert {
		fromLines, toLines = toLines, fromLines
		fromFile, toFile = toFile, fromFile
	}
	diff := difflib.UnifiedDiff{
		A:        fromLines,
		B:        toLines,
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return err
	}

	writeDiffOutput(text, config.Output.DiffStyle)

	// 空消息 + exit 1：git diff --exit-code 式契约（README 承诺，脚本依赖）
	return exitWithMessage("", 1)
}
