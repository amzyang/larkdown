package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkdown/core"
)

// UploadOpts upload 命令选项
type UploadOpts struct {
	source          string
	spaceID         string
	parentNodeToken string
	incremental     bool
	dryRun          bool
	verbose         bool
	ours            bool // 远端自上次同步点后已变化时仍以本地为准强制上传
	json            bool // 输出机读 JSON（file/is_new/url），上传进度改道 stderr
}

var uploadOpts = UploadOpts{}

// uploadedDoc 是一篇成功上传的文档，JSON 字段与单文件 --json 输出（file/is_new/url）同名。
type uploadedDoc struct {
	File  string `json:"file"`
	IsNew bool   `json:"is_new"`
	URL   string `json:"url"`
}

// linkRepair 是一次二次上传补链的结果。补链失败不影响退出码（文档本体已上传成功）。
type linkRepair struct {
	File  string `json:"file"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// uploadReport 收集多文件上传的产出与失败，驱动部分成功退出码（exit 3）与 --json 汇总。
// 顺序上传、无并发，不需要锁。
type uploadReport struct {
	docs    []uploadedDoc
	failed  []reportFailure
	repairs []linkRepair
}

// uploadReportView 是 upload 多文件 --json 的输出模型。
type uploadReportView struct {
	Documents   []uploadedDoc   `json:"documents"`
	Failed      []reportFailure `json:"failed,omitempty"`
	LinkRepairs []linkRepair    `json:"link_repairs,omitempty"`
}

func (r *uploadReport) view() uploadReportView {
	view := uploadReportView{Documents: r.docs, Failed: r.failed, LinkRepairs: r.repairs}
	if view.Documents == nil {
		view.Documents = []uploadedDoc{}
	}
	return view
}

// uploadOutcome 是 pass 1 中单个文件的结局，驱动二次补链决策。
type uploadOutcome struct {
	file string   // CLI 传入的文件路径
	ok   bool     // 是否上传成功
	refs []string // 转换期降级的本地 .md 引用目标（相对引用已按文件目录展开）
}

// planLinkRepairs 返回需要二次上传补链的文件（保持 pass 1 顺序）：上传成功、
// 且至少一个降级 .md 引用的目标也在本批次成功上传（现已有 source）的文件，
// 再传一次即可让增量 diff 识别「纯文本→链接」的块变更并原地补上链接。
// 目标不在批次内或上传失败的引用补不了，跳过。路径按绝对路径归一比较。
func planLinkRepairs(outcomes []uploadOutcome) []string {
	uploaded := make(map[string]bool, len(outcomes))
	for _, o := range outcomes {
		if o.ok {
			uploaded[absPath(o.file)] = true
		}
	}
	var repairs []string
	for _, o := range outcomes {
		if !o.ok {
			continue
		}
		for _, ref := range o.refs {
			if uploaded[absPath(ref)] {
				repairs = append(repairs, o.file)
				break
			}
		}
	}
	return repairs
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// isUserResolvableUploadErr 判断错误是否属于用户可自助解决的上传拒绝
// （不上报 Sentry）：source 目标已删/不存在、远端漂移、合并冲突未解决。
func isUserResolvableUploadErr(err error) bool {
	var sge *core.SourceGoneError
	var drift *core.SyncDriftError
	var pending *core.ConflictPendingError
	return errors.As(err, &sge) || errors.As(err, &drift) || errors.As(err, &pending)
}

// finalize 决定最终退出状态：无失败 → firstErr 原样（通常 nil）；有失败且有产出 →
// 部分成功（exit 3，用户可自助场景，不上报 Sentry）；有失败且零产出 → 整体失败（exit 1）。
func (r *uploadReport) finalize(firstErr error) error {
	if len(r.failed) == 0 {
		return firstErr
	}
	if len(r.docs) == 0 {
		if firstErr != nil {
			return firstErr
		}
		return exitWithMessage(fmt.Sprintf("上传失败: %d 项失败", len(r.failed)), 1)
	}
	return exitWithMessage(fmt.Sprintf("部分文件上传失败: %d 项失败（成功 %d 项）", len(r.failed), len(r.docs)), 3)
}

func handleUploadCommand(files []string) error {
	// 加载配置
	config, configPath, err := loadConfig()
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}

	// 多文件时的分隔标头通道：--json 时进度全在 stderr，标头随之
	headerW := io.Writer(os.Stdout)
	if uploadOpts.json {
		headerW = os.Stderr
	}

	report := &uploadReport{}
	var firstErr error
	var outcomes []uploadOutcome
	for _, filePath := range files {
		if len(files) > 1 {
			fmt.Fprintf(headerW, "\n===== %s =====\n", filePath)
		}
		result, err := uploadOneFile(ctx, client, filePath)
		if err != nil {
			// source 目标已删/不存在、远端漂移、冲突未解决均属用户可自助解决的错误，
			// 走 exitError 通道不上报 Sentry
			if len(files) == 1 {
				if isUserResolvableUploadErr(err) {
					return exitWithMessage(err.Error(), 1)
				}
				return err
			}
			log.Printf("警告: %s 上传失败: %v", filePath, err)
			report.failed = append(report.failed, reportFailure{Ref: filePath, Error: err.Error()})
			outcomes = append(outcomes, uploadOutcome{file: filePath, ok: false})
			if firstErr == nil {
				if isUserResolvableUploadErr(err) {
					firstErr = exitWithMessage(err.Error(), 1)
				} else {
					firstErr = err
				}
			}
			continue
		}
		outcomes = append(outcomes, uploadOutcome{file: filePath, ok: true, refs: result.UnresolvedMdRefs})
		report.docs = append(report.docs, uploadedDoc{
			File:  filePath,
			IsNew: result.IsNew,
			URL:   result.FrontMatter.Source,
		})

		// 输出结果
		if uploadOpts.json || uploadOpts.dryRun {
			continue
		}
		if result.IsNew {
			fmt.Printf("\n成功创建文档: %s\n", filePath)
		} else {
			fmt.Printf("\n成功更新文档: %s\n", filePath)
		}
		fmt.Printf("Wiki URL: %s\n", result.FrontMatter.Source)
	}

	if len(files) > 1 {
		runLinkRepairs(ctx, client, outcomes, report, headerW)
	}

	if uploadOpts.json {
		if len(files) == 1 {
			// 单文件 JSON 形状零迁移：扁平单对象（单文件失败已提前 return，必有一项）
			d := report.docs[0]
			printJSON(os.Stdout, map[string]any{
				"file":   d.File,
				"is_new": d.IsNew,
				"url":    d.URL,
			})
		} else {
			printJSON(os.Stdout, report.view())
		}
	}
	return report.finalize(firstErr)
}

// runLinkRepairs 多文件 pass 2：对「降级 .md 引用的目标已在本批次上传」的文件
// 自动二次增量上传补链。dry-run 只提示不执行（pass 1 未落库、目标无 source，
// 补链无从谈起）；补链失败仅警告并计入 --json 的 link_repairs，不改退出码。
func runLinkRepairs(ctx context.Context, client *core.Client, outcomes []uploadOutcome, report *uploadReport, headerW io.Writer) {
	if uploadOpts.dryRun {
		// dry-run 下所有文件都视作「将被上传」，据此提示实际上传时的补链行为
		hint := make([]uploadOutcome, len(outcomes))
		copy(hint, outcomes)
		for i := range hint {
			hint[i].ok = true
		}
		if hints := planLinkRepairs(hint); len(hints) > 0 {
			fmt.Fprintf(headerW, "\n[dryrun] %d 个文件含指向本批次文件的文档引用，实际上传时将自动二次上传补链: %s\n",
				len(hints), strings.Join(hints, ", "))
		}
		return
	}

	repairs := planLinkRepairs(outcomes)
	if len(repairs) == 0 {
		return
	}
	fmt.Fprintf(headerW, "\n检测到 %d 个文件的文档引用目标已在本批次完成上传，自动二次上传补链\n", len(repairs))
	for _, filePath := range repairs {
		fmt.Fprintf(headerW, "\n===== %s (二次上传补链) =====\n", filePath)
		if _, err := repairOneFile(ctx, client, filePath); err != nil {
			log.Printf("警告: %s 补链失败（文档本体已上传成功，重跑 larkdown upload 可修复链接）: %v", filePath, err)
			report.repairs = append(report.repairs, linkRepair{File: filePath, OK: false, Error: err.Error()})
			continue
		}
		report.repairs = append(report.repairs, linkRepair{File: filePath, OK: true})
	}
}

// repairOneFile 二次上传补链：pass 1 已把 source 写回目标文件 frontmatter，重传一次
// 让增量 diff 识别「纯文本→链接」的块签名变化并原地 update_text_elements 补上。
// 强制增量（--full 也不重建第二遍）；不带 --source/--space/--parent（本文件 frontmatter 已有 source）。
func repairOneFile(ctx context.Context, client *core.Client, filePath string) (*core.UploadResult, error) {
	uploader, err := core.NewUploader(client)
	if err != nil {
		return nil, err
	}
	if uploadOpts.json {
		uploader.SetOutput(os.Stderr)
	}
	// 补链是对刚上传内容的立即重传，漂移检查无增益且 wiki obj_edit_time
	// 异步滞后可能造成假阳性拒绝，固定 Ours 跳过
	return uploader.Upload(ctx, filePath, core.UploadOptions{Incremental: true, Ours: true})
}

// uploadOneFile 上传单个文件。每个文件新建 Uploader，隔离 pendingBoardMappings/
// pendingMediaMappings 等 per-document 累积状态；client 复用（限流器共享）。
func uploadOneFile(ctx context.Context, client *core.Client, filePath string) (*core.UploadResult, error) {
	// 创建上传器；--json 时把上传进度改道 stderr，保持 stdout 纯 JSON
	uploader, err := core.NewUploader(client)
	if err != nil {
		return nil, err
	}
	if uploadOpts.json {
		uploader.SetOutput(os.Stderr)
	}

	return uploader.Upload(ctx, filePath, core.UploadOptions{
		Source:          uploadOpts.source,
		SpaceID:         uploadOpts.spaceID,
		ParentNodeToken: uploadOpts.parentNodeToken,
		Incremental:     uploadOpts.incremental,
		DryRun:          uploadOpts.dryRun,
		Verbose:         uploadOpts.verbose,
		Ours:            uploadOpts.ours,
	})
}
