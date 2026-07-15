package main

import (
	"context"
	"errors"
	"fmt"

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
}

var uploadOpts = UploadOpts{}

func handleUploadCommand(filePath string) error {
	// 加载配置
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}

	// 创建上传器
	uploader, err := core.NewUploader(client)
	if err != nil {
		return err
	}

	// 执行上传
	result, err := uploader.Upload(ctx, filePath, core.UploadOptions{
		Source:          uploadOpts.source,
		SpaceID:         uploadOpts.spaceID,
		ParentNodeToken: uploadOpts.parentNodeToken,
		Incremental:     uploadOpts.incremental,
		DryRun:          uploadOpts.dryRun,
		Verbose:         uploadOpts.verbose,
	})
	if err != nil {
		// source 目标已删/不存在属用户可自助解决的错误，走 exitError 通道不上报 Sentry
		var sge *core.SourceGoneError
		if errors.As(err, &sge) {
			return exitWithMessage(err.Error(), 1)
		}
		return err
	}

	// 输出结果
	if uploadOpts.dryRun {
		// dryrun 模式不显示成功提示
	} else if result.IsNew {
		fmt.Printf("\n成功创建文档: %s\n", filePath)
	} else {
		fmt.Printf("\n成功更新文档: %s\n", filePath)
	}
	if !uploadOpts.dryRun {
		fmt.Printf("Wiki URL: %s\n", result.FrontMatter.Source)
	}

	return nil
}
