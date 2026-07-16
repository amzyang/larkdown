package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

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
	json            bool // 输出机读 JSON（file/is_new/url），上传进度改道 stderr
}

var uploadOpts = UploadOpts{}

// uploadedDoc 是一篇成功上传的文档，JSON 字段与单文件 --json 输出（file/is_new/url）同名。
type uploadedDoc struct {
	File  string `json:"file"`
	IsNew bool   `json:"is_new"`
	URL   string `json:"url"`
}

// uploadReport 收集多文件上传的产出与失败，驱动部分成功退出码（exit 3）与 --json 汇总。
// 顺序上传、无并发，不需要锁。
type uploadReport struct {
	docs   []uploadedDoc
	failed []reportFailure
}

// uploadReportView 是 upload 多文件 --json 的输出模型。
type uploadReportView struct {
	Documents []uploadedDoc   `json:"documents"`
	Failed    []reportFailure `json:"failed,omitempty"`
}

func (r *uploadReport) view() uploadReportView {
	view := uploadReportView{Documents: r.docs, Failed: r.failed}
	if view.Documents == nil {
		view.Documents = []uploadedDoc{}
	}
	return view
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
	for _, filePath := range files {
		if len(files) > 1 {
			fmt.Fprintf(headerW, "\n===== %s =====\n", filePath)
		}
		result, err := uploadOneFile(ctx, client, filePath)
		if err != nil {
			// source 目标已删/不存在属用户可自助解决的错误，走 exitError 通道不上报 Sentry
			var sge *core.SourceGoneError
			if len(files) == 1 {
				if errors.As(err, &sge) {
					return exitWithMessage(err.Error(), 1)
				}
				return err
			}
			log.Printf("警告: %s 上传失败: %v", filePath, err)
			report.failed = append(report.failed, reportFailure{Ref: filePath, Error: err.Error()})
			if firstErr == nil {
				if errors.As(err, &sge) {
					firstErr = exitWithMessage(err.Error(), 1)
				} else {
					firstErr = err
				}
			}
			continue
		}
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
	})
}
