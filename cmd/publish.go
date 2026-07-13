package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkdown/core"
)

// PublishOpts publish 命令选项
type PublishOpts struct {
	name     string
	appID    string // 显式指定复用的 app_id 或妙搭应用链接
	forceNew bool   // 强制新建应用（忽略 manifest）
	share    string // 访问权限档位：selected | tenant | public（空=新建默认 tenant、更新保持）
}

var publishOpts = PublishOpts{}

func handlePublishCommand(path string) error {
	configPath, err := core.GetConfigFilePath()
	if err != nil {
		return err
	}
	config, err := core.ReadConfigFromFile(configPath)
	if err != nil {
		return err
	}

	sp, err := core.DefaultStatePaths()
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := createClientFromConfig(ctx, config, configPath)
	if err != nil {
		return err
	}
	if !client.HasUserToken() {
		return fmt.Errorf("发布妙搭应用仅支持用户身份（user_access_token），不支持 --as bot；请先执行 larkdown auth login")
	}

	// 解析复用的 app_id：--app-id 优先，其次读 manifest，再次新建
	appID := core.ExtractMiaodaAppID(publishOpts.appID)
	var manifest *core.PublishManifest
	if appID == "" && !publishOpts.forceNew {
		if manifest, err = core.ReadPublishManifest(sp, absPath); err != nil {
			log.Printf("warning: 读取发布记录失败: %v", err)
		}
		if manifest != nil {
			appID = manifest.MiaodaAppID
		}
	}

	// 应用名：--name 优先，其次沿用 manifest 记录，再次从路径推导
	name := publishOpts.name
	if name == "" {
		if manifest != nil && manifest.Name != "" {
			name = manifest.Name
		} else {
			name = deriveAppName(path)
		}
	}

	if appID == "" {
		fmt.Printf("正在打包并新建发布 %s ...\n", path)
	} else {
		fmt.Printf("正在打包并更新发布 %s（app_id=%s）...\n", path, appID)
	}

	result, err := client.PublishHTMLArtifact(ctx, name, path, appID, publishOpts.share)
	if err != nil {
		if appID != "" && core.IsMiaodaAppNotFound(err) {
			return fmt.Errorf("妙搭应用 %s 不存在或无权访问（可能已被删除）；用 --new 重新创建一个新应用: %w", appID, err)
		}
		return err
	}

	// 写回发布记录，供下次重新发布复用
	if werr := core.WritePublishManifest(sp, absPath, &core.PublishManifest{
		MiaodaAppID: result.AppID,
		MiaodaURL:   result.URL,
		MiaodaScope: result.AppliedScope,
		Name:        name,
		PublishedAt: time.Now().Format(time.RFC3339),
	}); werr != nil {
		log.Printf("warning: 写入发布记录失败（下次将无法自动复用 app_id）: %v", werr)
	}

	verb := "已更新"
	if result.IsNew {
		verb = "已发布"
	}
	manageURL := core.MiaodaManageURL(result.AppID)
	fmt.Printf("\n应用「%s」%s\n", name, verb)
	fmt.Printf("  访问链接：%s\n", result.URL)
	fmt.Printf("  编辑管理：%s\n", manageURL)
	// 经 lipgloss.Writer（colorprofile）输出，按终端能力自动降级颜色（管道/CI 去色）。
	lipgloss.Fprintf(lipgloss.Writer, "\n%s\n", renderShareNotice(result.AppliedScope, result.ScopeErr, manageURL, result.IsNew))
	fmt.Printf("\n后续更新此应用（复用 app_id）：\n  larkdown publish %s --app-id %s\n", shellQuote(path), result.AppID)
	return nil
}

// shellQuote 对参数做 POSIX shell 单引号转义，使带空格/特殊字符的路径
// 在提示命令中可被直接复制执行。无特殊字符时原样返回，保持提示简洁。
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\r'\"\\$`&|;<>()*?[]{}!#~=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// deriveAppName 从路径推导默认应用名（去掉 .html 后缀）
func deriveAppName(path string) string {
	base := filepath.Base(strings.TrimRight(path, "/"))
	base = strings.TrimSuffix(base, ".html")
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "larkdown-artifact"
	}
	return base
}
