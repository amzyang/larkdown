package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
)

// ClientOptions 客户端配置选项
type ClientOptions struct {
	Debug bool
}

// timedLogger 带时间戳的 lark Logger 实现
type timedLogger struct{}

func (l *timedLogger) Log(ctx context.Context, level lark.LogLevel, msg string, args ...interface{}) {
	log.Printf("[%s] "+msg, append([]interface{}{level.String()}, args...)...)
}

// timingMiddleware 返回记录 API 调用耗时的中间件
func timingMiddleware() lark.ApiMiddleware {
	return func(next lark.ApiEndpoint) lark.ApiEndpoint {
		return func(ctx context.Context, req *lark.RawRequestReq, resp interface{}) (*lark.Response, error) {
			start := time.Now()
			response, err := next(ctx, req, resp)
			log.Printf("[DEBUG] [timing] %s#%s %s took %s", req.Scope, req.API, req.Method, time.Since(start))
			return response, err
		}
	}
}

type Client struct {
	larkClient      *lark.Lark
	userAccessToken string
	imageCache      *ImageCache
	debug           bool
	cookie          cookieState
}

// newLarkClient 创建底层 lark.Lark 实例（公共初始化逻辑）
func newLarkClient(appID, appSecret string, debug bool) *lark.Lark {
	rl := NewRateLimiter(debug)
	middlewares := []lark.ApiMiddleware{rl.Middleware()}
	if debug {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
		middlewares = []lark.ApiMiddleware{timingMiddleware(), rl.Middleware()}
	}
	larkOpts := []lark.ClientOptionFunc{
		lark.WithAppCredential(appID, appSecret),
		lark.WithTimeout(60 * time.Second),
		lark.WithApiMiddleware(middlewares...),
	}
	if debug {
		larkOpts = append(larkOpts, lark.WithLogger(&timedLogger{}, lark.LogLevelTrace))
	}
	return lark.New(larkOpts...)
}

func NewClient(appID, appSecret string, opts *ClientOptions) *Client {
	debug := opts != nil && opts.Debug
	return &Client{
		larkClient: newLarkClient(appID, appSecret, debug),
		imageCache: NewImageCache(),
		debug:      debug,
	}
}

// NewClientWithUserToken 使用 user_access_token 创建客户端
func NewClientWithUserToken(appID, appSecret, userAccessToken string, opts *ClientOptions) *Client {
	debug := opts != nil && opts.Debug
	return &Client{
		larkClient:      newLarkClient(appID, appSecret, debug),
		userAccessToken: userAccessToken,
		imageCache:      NewImageCache(),
		debug:           debug,
	}
}

const defaultFeishuDomain = "feishu.cn"

// Domain 返回当前租户域名
func (c *Client) Domain() string {
	return defaultFeishuDomain
}

// methodOptions 返回当前请求应使用的认证选项
func (c *Client) methodOptions() []lark.MethodOptionFunc {
	if c.userAccessToken != "" {
		return []lark.MethodOptionFunc{lark.WithUserAccessToken(c.userAccessToken)}
	}
	return nil
}

// HasUserToken 是否已配置 user_access_token
func (c *Client) HasUserToken() bool {
	return c.userAccessToken != ""
}

// docsAIUpdateReq 是 docs_ai/v1 更新文档的请求体（对齐 lark CLI buildUpdateBody）。
// chyroc/lark 用反射遍历 body 字段，故用 struct。
type docsAIUpdateReq struct {
	Format      string `json:"format"`
	Command     string `json:"command"`
	RevisionID  int64  `json:"revision_id"`
	BlockID     string `json:"block_id"`
	SrcBlockIDs string `json:"src_block_ids"`
}

type docsAIUpdateResp struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
}

// BlockMoveAfter 把已有块移动到锚点块之后（真服务端 move，保留块 identity 含白板 token）。
//
// 走 PUT /open-apis/docs_ai/v1/documents/{token}（已验证可公开使用；公开 docx v1 无块移动能力），
// 与 lark CLI 的 block_move_after 同源。anchorBlockID 为锚点：documentID 表示文档开头，
// "-1" 表示末尾，其余为某块 block_id（移动到其后）。SDK 无封装，故用 RawRequest，
// 自动复用 token 注入 + 限流 + 超时。
func (c *Client) BlockMoveAfter(ctx context.Context, documentToken, anchorBlockID string, srcBlockIDs []string) error {
	if len(srcBlockIDs) == 0 {
		return nil
	}
	resp := new(docsAIUpdateResp)
	_, err := c.larkClient.RawRequest(ctx, &lark.RawRequestReq{
		Scope:  "DocsAI",
		API:    "BlockMoveAfter",
		Method: "PUT",
		URL:    "https://open.feishu.cn/open-apis/docs_ai/v1/documents/" + documentToken,
		Body: &docsAIUpdateReq{
			Format:      "xml",
			Command:     "block_move_after",
			RevisionID:  -1,
			BlockID:     anchorBlockID,
			SrcBlockIDs: strings.Join(srcBlockIDs, ","),
		},
		NeedUserAccessToken:   c.userAccessToken != "",
		NeedTenantAccessToken: c.userAccessToken == "",
		MethodOption:          c.userMethodOption(),
	}, resp)
	return err
}

// isImageContent 检测响应是否包含图片数据。
// 先检查 HTTP Content-Type，如果是 application/octet-stream 则用 magic bytes 嗅探。
func isImageContent(ct string, body []byte) bool {
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	if ct == "application/octet-stream" || ct == "" {
		detected := http.DetectContentType(body)
		return strings.HasPrefix(detected, "image/")
	}
	return false
}

func (c *Client) DownloadImage(ctx context.Context, imgToken, outDir string) (string, error) {
	// 1. 检查缓存
	if cachePath, origFilename := c.imageCache.Get(imgToken); cachePath != "" {
		destPath := filepath.Join(outDir, imgToken+"_"+origFilename)
		if err := c.imageCache.CopyTo(cachePath, destPath); err == nil {
			return destPath, nil
		}
		// 复制失败，降级到下载
	}

	// 2. Cookie 优先：该文档之前已有 cookie 成功
	if c.shouldPreferCookie(ctx) {
		data, filename, err := c.cookieFallbackDownloadImage(ctx, imgToken)
		if err == nil {
			filePath := filepath.Join(outDir, imgToken+"_"+filename)
			os.MkdirAll(filepath.Dir(filePath), 0o755)
			os.WriteFile(filePath, data, 0o666)
			return filePath, nil
		}
		// cookie 失败，降级走 API
	}

	// 3. 从 API 下载
	resp, response, err := c.larkClient.Drive.DownloadDriveMedia(ctx, &lark.DownloadDriveMediaReq{
		FileToken: imgToken,
	}, c.methodOptions()...)
	if err != nil {
		if !IsPermissionError(err) {
			return imgToken, err
		}
		// 4. Cookie fallback
		log.Printf("API 权限错误，尝试 cookie 回退: %s", imgToken)
		data, filename, fallbackErr := c.cookieFallbackDownloadImage(ctx, imgToken)
		if fallbackErr != nil {
			log.Printf("cookie 回退失败: %v", fallbackErr)
			return imgToken, err // 返回原始 API 错误
		}
		c.markCookieFallbackSuccess(ctx)
		filePath := filepath.Join(outDir, imgToken+"_"+filename)
		if mkErr := os.MkdirAll(filepath.Dir(filePath), 0o755); mkErr != nil {
			return imgToken, mkErr
		}
		if wErr := os.WriteFile(filePath, data, 0o666); wErr != nil {
			return imgToken, wErr
		}
		return filePath, nil
	}
	// 读取数据用于缓存和写入
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return imgToken, err
	}
	if ct := response.Header.Get("Content-Type"); !isImageContent(ct, data) {
		return imgToken, fmt.Errorf("图片下载失败 (%s): Content-Type=%s, body=%s", imgToken, ct, data)
	}

	// 3. 写入缓存（忽略错误）
	_ = c.imageCache.Put(imgToken, data, resp.Filename)

	// 4. 写入目标文件
	filename := fmt.Sprintf("%s/%s_%s", outDir, imgToken, resp.Filename)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return imgToken, err
	}
	if err := os.WriteFile(filename, data, 0o666); err != nil {
		return imgToken, err
	}

	return filename, nil
}

// DownloadMedia 下载文档内附件（file block）到本地
func (c *Client) DownloadMedia(ctx context.Context, fileToken, outDir string) (string, error) {
	// 1. 检查缓存
	if cachePath, origFilename := c.imageCache.GetFile(fileToken); cachePath != "" {
		destPath := filepath.Join(outDir, fileToken+"_"+origFilename)
		if err := c.imageCache.CopyTo(cachePath, destPath); err == nil {
			return destPath, nil
		}
	}

	// 2. Cookie 优先：该文档之前已有 cookie 成功
	if c.shouldPreferCookie(ctx) {
		data, filename, err := c.cookieFallbackDownloadMedia(ctx, fileToken)
		if err == nil {
			filePath := filepath.Join(outDir, fileToken+"_"+filename)
			os.MkdirAll(filepath.Dir(filePath), 0o755)
			os.WriteFile(filePath, data, 0o666)
			return filePath, nil
		}
	}

	// 3. 从 API 下载
	resp, _, err := c.larkClient.Drive.DownloadDriveMedia(ctx, &lark.DownloadDriveMediaReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		if !IsPermissionError(err) {
			return fileToken, err
		}
		log.Printf("API 权限错误，尝试 cookie 回退: %s", fileToken)
		data, filename, fallbackErr := c.cookieFallbackDownloadMedia(ctx, fileToken)
		if fallbackErr != nil {
			log.Printf("cookie 回退失败: %v", fallbackErr)
			return fileToken, err
		}
		c.markCookieFallbackSuccess(ctx)
		filePath := filepath.Join(outDir, fileToken+"_"+filename)
		if mkErr := os.MkdirAll(filepath.Dir(filePath), 0o755); mkErr != nil {
			return fileToken, mkErr
		}
		if wErr := os.WriteFile(filePath, data, 0o666); wErr != nil {
			return fileToken, wErr
		}
		return filePath, nil
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return fileToken, err
	}

	// 4. 写入缓存（忽略错误）
	_ = c.imageCache.PutFile(fileToken, data, resp.Filename)

	// 5. 写入目标文件
	filename := filepath.Join(outDir, fileToken+"_"+resp.Filename)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return fileToken, err
	}
	if err := os.WriteFile(filename, data, 0o666); err != nil {
		return fileToken, err
	}
	return filename, nil
}

// DownloadMediaRaw 下载文档内附件并返回原始字节（Web 服务用）
func (c *Client) DownloadMediaRaw(ctx context.Context, fileToken string) (string, []byte, error) {
	// 1. 检查缓存
	if cachePath, origFilename := c.imageCache.GetFile(fileToken); cachePath != "" {
		data, err := os.ReadFile(cachePath)
		if err == nil {
			return fileToken + "_" + origFilename, data, nil
		}
	}

	// 2. Cookie 优先：该文档之前已有 cookie 成功
	if c.shouldPreferCookie(ctx) {
		data, filename, err := c.cookieFallbackDownloadMedia(ctx, fileToken)
		if err == nil {
			return fileToken + "_" + filename, data, nil
		}
	}

	// 3. 从 API 下载
	resp, _, err := c.larkClient.Drive.DownloadDriveMedia(ctx, &lark.DownloadDriveMediaReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		if !IsPermissionError(err) {
			return "", nil, err
		}
		log.Printf("API 权限错误，尝试 cookie 回退: %s", fileToken)
		data, filename, fallbackErr := c.cookieFallbackDownloadMedia(ctx, fileToken)
		if fallbackErr != nil {
			log.Printf("cookie 回退失败: %v", fallbackErr)
			return "", nil, err
		}
		c.markCookieFallbackSuccess(ctx)
		return fileToken + "_" + filename, data, nil
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return "", nil, err
	}

	// 4. 写入缓存（忽略错误）
	_ = c.imageCache.PutFile(fileToken, data, resp.Filename)

	return fileToken + "_" + resp.Filename, data, nil
}

func (c *Client) DownloadImageRaw(ctx context.Context, imgToken, imgDir string) (string, []byte, error) {
	// 1. 检查缓存
	if cachePath, origFilename := c.imageCache.Get(imgToken); cachePath != "" {
		data, err := os.ReadFile(cachePath)
		if err == nil {
			filename := filepath.Join(imgDir, imgToken+"_"+origFilename)
			return filename, data, nil
		}
		// 读取失败，降级到下载
	}

	// 2. Cookie 优先：该文档之前已有 cookie 成功
	if c.shouldPreferCookie(ctx) {
		data, filename, err := c.cookieFallbackDownloadImage(ctx, imgToken)
		if err == nil {
			filePath := filepath.Join(imgDir, imgToken+"_"+filename)
			return filePath, data, nil
		}
	}

	// 3. 从 API 下载
	resp, response, err := c.larkClient.Drive.DownloadDriveMedia(ctx, &lark.DownloadDriveMediaReq{
		FileToken: imgToken,
	}, c.methodOptions()...)
	if err != nil {
		if !IsPermissionError(err) {
			return imgToken, nil, err
		}
		log.Printf("API 权限错误，尝试 cookie 回退: %s", imgToken)
		data, filename, fallbackErr := c.cookieFallbackDownloadImage(ctx, imgToken)
		if fallbackErr != nil {
			log.Printf("cookie 回退失败: %v", fallbackErr)
			return imgToken, nil, err
		}
		c.markCookieFallbackSuccess(ctx)
		filePath := filepath.Join(imgDir, imgToken+"_"+filename)
		return filePath, data, nil
	}
	data, err := io.ReadAll(resp.File)
	if err != nil {
		return imgToken, nil, err
	}
	if ct := response.Header.Get("Content-Type"); !isImageContent(ct, data) {
		return imgToken, nil, fmt.Errorf("图片下载失败 (%s): Content-Type=%s, body=%s", imgToken, ct, data)
	}

	// 3. 写入缓存（忽略错误）
	_ = c.imageCache.Put(imgToken, data, resp.Filename)

	// 4. 返回结果
	filename := fmt.Sprintf("%s/%s_%s", imgDir, imgToken, resp.Filename)
	return filename, data, nil
}

// DownloadFile 下载云空间文件到本地 (simple version from HEAD)
func (c *Client) DownloadFile(ctx context.Context, fileToken, outDir string) (string, error) {
	resp, _, err := c.larkClient.Drive.DownloadDriveFile(ctx, &lark.DownloadDriveFileReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		return "", err
	}
	filename := filepath.Join(outDir, resp.Filename)
	err = os.MkdirAll(filepath.Dir(filename), 0o755)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return "", err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.File)
	if err != nil {
		return "", err
	}
	return filename, nil
}

// DownloadFileRaw 下载文件并返回原始字节（Web 服务用）
func (c *Client) DownloadFileRaw(ctx context.Context, fileToken string) (string, []byte, error) {
	resp, _, err := c.larkClient.Drive.DownloadDriveFile(ctx, &lark.DownloadDriveFileReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		return "", nil, err
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.File)
	return resp.Filename, buf.Bytes(), nil
}

// DownloadFileWithMeta downloads any file from Feishu Drive (including mindnote, video, etc.)
// For unsupported file types, it creates a markdown file with a link to the original file
//
// Note: There are two different download APIs in Feishu:
// 1. DownloadDriveMedia - for downloading media resources (images, attachments) inside documents
// 2. DownloadDriveFile - for downloading standalone files in cloud drive
//
// For file objects (mindnote, file, sheet, bitable), we should use DownloadDriveFile
// For media blocks inside documents, we should use DownloadDriveMedia
func (c *Client) DownloadFileWithMeta(ctx context.Context, fileToken, outDir, objType, title string) (string, error) {
	var (
		file     io.Reader
		filename string
		err      error
	)

	// Try DownloadDriveFile first for standalone files (mindnote, video, PDF, etc.)
	// This is the correct API for downloading files from cloud drive
	resp, _, err := c.larkClient.Drive.DownloadDriveFile(ctx, &lark.DownloadDriveFileReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		// If DownloadDriveFile fails, try DownloadDriveMedia as fallback
		// This handles the case where the file is actually a media resource inside a document
		mediaResp, _, mediaErr := c.larkClient.Drive.DownloadDriveMedia(ctx, &lark.DownloadDriveMediaReq{
			FileToken: fileToken,
		}, c.methodOptions()...)
		if mediaErr != nil {
			// Both APIs failed, create a placeholder
			return c.createFilePlaceholder(ctx, fileToken, outDir, objType, title)
		}
		if mediaResp == nil {
			return c.createFilePlaceholder(ctx, fileToken, outDir, objType, title)
		}
		file = mediaResp.File
		filename = mediaResp.Filename
	} else {
		if resp == nil {
			return c.createFilePlaceholder(ctx, fileToken, outDir, objType, title)
		}
		file = resp.File
		filename = resp.Filename
	}

	// Use the original filename from the response
	if filename == "" {
		// Fallback to token if filename is empty
		filename = fileToken
	}

	filePath := filepath.Join(outDir, filename)
	err = os.MkdirAll(filepath.Dir(filePath), 0o755)
	if err != nil {
		return "", err
	}

	fileHandle, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return "", err
	}
	defer fileHandle.Close()

	_, err = io.Copy(fileHandle, file)
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// createFilePlaceholder creates a markdown file with a link to the original file
func (c *Client) createFilePlaceholder(ctx context.Context, fileToken, outDir, objType, title string) (string, error) {
	// Create a markdown file with the same name as the title
	mdFilename := utils.SanitizeFileName(title) + ".md"
	mdPath := filepath.Join(outDir, mdFilename)

	// Ensure the directory exists
	err := os.MkdirAll(outDir, 0o755)
	if err != nil {
		return "", err
	}

	// Get the file type description
	var fileType string
	switch objType {
	case "mindnote":
		fileType = "思维导图"
	case "file":
		fileType = "文件"
	case "sheet":
		fileType = "表格"
	case "bitable":
		fileType = "多维表格"
	default:
		fileType = "文件"
	}

	content := fmt.Sprintf("# %s\n\n**文件类型**: %s\n\n", title, fileType)
	content += fmt.Sprintf("**文件Token**: `%s`\n\n", fileToken)
	content += fmt.Sprintf("**提示**: 这是一个%s文件，无法直接转换为Markdown。\n\n", fileType)
	content += fmt.Sprintf("请访问飞书查看原始文件: [点击打开](https://%s/%s/%s)\n", c.Domain(), objType, fileToken)

	err = os.WriteFile(mdPath, []byte(content), 0o644)
	if err != nil {
		return "", err
	}

	return mdPath, nil
}

func (c *Client) GetDocxContent(ctx context.Context, docToken string) (*lark.DocxDocument, []*lark.DocxBlock, error) {
	resp, _, err := c.larkClient.Drive.GetDocxDocument(ctx, &lark.GetDocxDocumentReq{
		DocumentID: docToken,
	}, c.methodOptions()...)
	if err != nil {
		return nil, nil, err
	}
	docx := &lark.DocxDocument{
		DocumentID: resp.Document.DocumentID,
		RevisionID: resp.Document.RevisionID,
		Title:      resp.Document.Title,
	}
	var blocks []*lark.DocxBlock
	var pageToken *string
	for {
		resp2, _, err := c.larkClient.Drive.GetDocxBlockListOfDocument(ctx, &lark.GetDocxBlockListOfDocumentReq{
			DocumentID: docx.DocumentID,
			PageToken:  pageToken,
			UserIDType: userIDTypePtr(), // 全局用户身份策略：统一 union_id
		}, c.methodOptions()...)
		if err != nil {
			return docx, nil, err
		}
		blocks = append(blocks, resp2.Items...)
		pageToken = &resp2.PageToken
		if !resp2.HasMore {
			break
		}
	}
	return docx, blocks, nil
}

func (c *Client) GetWikiNodeInfo(ctx context.Context, token string) (*lark.GetWikiNodeRespNode, error) {
	resp, _, err := c.larkClient.Drive.GetWikiNode(ctx, &lark.GetWikiNodeReq{
		Token: token,
	}, c.methodOptions()...)
	if err != nil {
		return nil, err
	}
	return resp.Node, nil
}

// looksLikeSpaceID 判断 token 是否看起来像 space_id（纯数字或 "my_library"）
func looksLikeSpaceID(token string) bool {
	if token == "my_library" {
		return true
	}
	for _, c := range token {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(token) > 0
}

// ResolveWikiToken 判断 token 是 space_id 还是 node_token
// 返回: isSpace 表示是否为 space_id, spaceID 为知识库 ID, node 为节点信息（仅当 isSpace=false 时有效）
func (c *Client) ResolveWikiToken(ctx context.Context, token string) (isSpace bool, spaceID string, node *lark.GetWikiNodeRespNode, err error) {
	if looksLikeSpaceID(token) {
		// 纯数字或 my_library，优先作为 space_id
		_, err = c.GetWikiName(ctx, token)
		if err == nil {
			return true, token, nil, nil
		}
		node, err = c.GetWikiNodeInfo(ctx, token)
		if err == nil {
			return false, node.SpaceID, node, nil
		}
	} else {
		// 字母数字混合，优先作为 node_token
		node, err = c.GetWikiNodeInfo(ctx, token)
		if err == nil {
			return false, node.SpaceID, node, nil
		}
		_, err = c.GetWikiName(ctx, token)
		if err == nil {
			return true, token, nil, nil
		}
	}
	return false, "", nil, fmt.Errorf("token 既不是有效的 node_token 也不是 space_id: %w", err)
}

func (c *Client) GetDriveFolderFileList(ctx context.Context, pageToken *string, folderToken *string) ([]*lark.GetDriveFileListRespFile, error) {
	resp, _, err := c.larkClient.Drive.GetDriveFileList(ctx, &lark.GetDriveFileListReq{
		PageSize:    nil,
		PageToken:   pageToken,
		FolderToken: folderToken,
	}, c.methodOptions()...)
	if err != nil {
		return nil, err
	}
	files := resp.Files
	for resp.HasMore {
		resp, _, err = c.larkClient.Drive.GetDriveFileList(ctx, &lark.GetDriveFileListReq{
			PageSize:    nil,
			PageToken:   &resp.NextPageToken,
			FolderToken: folderToken,
		}, c.methodOptions()...)
		if err != nil {
			return nil, err
		}
		files = append(files, resp.Files...)
	}
	return files, nil
}

func (c *Client) GetWikiName(ctx context.Context, spaceID string) (string, error) {
	resp, _, err := c.larkClient.Drive.GetWikiSpace(ctx, &lark.GetWikiSpaceReq{
		SpaceID: spaceID,
	}, c.methodOptions()...)

	if err != nil {
		return "", err
	}

	return resp.Space.Name, nil
}

func (c *Client) GetWikiNodeList(ctx context.Context, spaceID string, parentNodeToken *string) ([]*lark.GetWikiNodeListRespItem, error) {
	resp, _, err := c.larkClient.Drive.GetWikiNodeList(ctx, &lark.GetWikiNodeListReq{
		SpaceID:         spaceID,
		PageSize:        nil,
		PageToken:       nil,
		ParentNodeToken: parentNodeToken,
	}, c.methodOptions()...)
	if err != nil {
		return nil, err
	}

	nodes := resp.Items
	for resp.HasMore {
		pageToken := resp.PageToken
		resp, _, err = c.larkClient.Drive.GetWikiNodeList(ctx, &lark.GetWikiNodeListReq{
			SpaceID:         spaceID,
			PageSize:        nil,
			PageToken:       &pageToken,
			ParentNodeToken: parentNodeToken,
		}, c.methodOptions()...)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, resp.Items...)
	}

	return nodes, nil
}

// SheetMergeInfo 表示合并单元格信息
type SheetMergeInfo struct {
	StartRowIndex    int64
	StartColumnIndex int64
	RowSpan          int64 // 合并的行数
	ColSpan          int64 // 合并的列数
}

// SheetData 包含电子表格的值、合并信息和网格属性
type SheetData struct {
	Values            [][]string
	Merges            []*SheetMergeInfo
	FrozenRowCount    int64 // 冻结的行数（一般是表头行数）
	FrozenColumnCount int64
	RowCount          int64 // 工作表总行数
	ColumnCount       int64 // 工作表总列数
}

// GetSheetContent 获取电子表格的内容
func (c *Client) GetSheetContent(ctx context.Context, sheetToken string) (*SheetData, error) {
	// sheetToken 的格式是：spreadsheet_token + "_" + sheet_id
	// 例如：B3hasMxsshByaEtZxAwcVfWxnSe_Ml1QzO
	// 需要解析出 spreadsheet_token 和 sheet_id

	// 查找最后一个下划线，分隔 spreadsheet_token 和 sheet_id
	lastUnderscore := strings.LastIndex(sheetToken, "_")
	if lastUnderscore == -1 {
		return nil, fmt.Errorf("invalid sheet token format (missing underscore separator): %s", sheetToken)
	}

	spreadsheetToken := sheetToken[:lastUnderscore]
	sheetID := sheetToken[lastUnderscore+1:]

	valueResp, _, err := c.larkClient.Drive.BatchGetSheetValue(ctx, &lark.BatchGetSheetValueReq{
		SpreadSheetToken: spreadsheetToken,
		Ranges:           []string{sheetID},
	}, c.methodOptions()...)
	if err != nil {
		// 如果失败，返回详细的错误信息
		return nil, fmt.Errorf("failed to get sheet values: %w", err)
	}

	if len(valueResp.ValueRanges) == 0 {
		return nil, fmt.Errorf("no value ranges found")
	}

	// 转换为二维数组
	values := valueResp.ValueRanges[0].Values
	if len(values) == 0 {
		return nil, fmt.Errorf("sheet is empty")
	}

	// 将 [][]SheetContent 转换为 [][]string
	result := make([][]string, len(values))
	for i, row := range values {
		result[i] = make([]string, len(row))
		for j, cell := range row {
			// 根据单元格类型提取值
			if cell.String != nil {
				// 将换行符转换为 <br> 标签，以便在 markdown 表格中正确显示
				result[i][j] = strings.ReplaceAll(*cell.String, "\n", "<br>")
			} else if cell.Int != nil {
				result[i][j] = fmt.Sprintf("%d", *cell.Int)
			} else if cell.Float != nil {
				// 处理浮点数
				result[i][j] = fmt.Sprintf("%g", *cell.Float)
			} else if cell.Link != nil {
				// 超链接：保留 URL 渲染成 markdown 链接，丢弃 URL 等于丢失最关键的信息
				text := strings.ReplaceAll(cell.Link.Text, "\n", "<br>")
				if cell.Link.Link != "" {
					result[i][j] = fmt.Sprintf("[%s](%s)", text, cell.Link.Link)
				} else {
					result[i][j] = text
				}
			} else if cell.Formula != nil {
				// 公式类型，Text 字段存储公式本身
				result[i][j] = cell.Formula.Text
			} else if cell.AtUser != nil {
				// 标记 @ 人，加前缀让读者知道这是提及
				result[i][j] = "@" + cell.AtUser.Text
			} else if cell.AtDoc != nil {
				// @ 文档：Text 是 token，ObjType 是 doc/sheet/slide/bitable/mindnote。
				// 渲染成可点击的 markdown 链接（飞书会自动重定向到当前租户子域）
				token := cell.AtDoc.Text
				objType := cell.AtDoc.ObjType
				if objType == "" {
					objType = "docs"
				}
				result[i][j] = fmt.Sprintf("[📄 %s](https://%s/%s/%s)", token, c.Domain(), objType, token)
			} else if cell.EmbedImage != nil {
				// 嵌入图片：当前不下载，至少给出占位避免静默丢失内容
				name := cell.EmbedImage.Text
				if name == "" {
					name = cell.EmbedImage.FileToken
				}
				result[i][j] = "🖼️ " + name
			} else if cell.Attachment != nil {
				// 附件：当前不下载，至少给出占位
				name := cell.Attachment.Text
				if name == "" {
					name = cell.Attachment.FileToken
				}
				result[i][j] = "📎 " + name
			} else if cell.MultiValue != nil && len(cell.MultiValue.Values) > 0 {
				// 下拉列表，可能有多个值
				cellValues := make([]string, len(cell.MultiValue.Values))
				for k, v := range cell.MultiValue.Values {
					// Values 是 []interface{}，可能是 string, bool, 或 number
					switch val := v.(type) {
					case string:
						cellValues[k] = val
					case bool:
						cellValues[k] = fmt.Sprintf("%t", val)
					case float64:
						cellValues[k] = fmt.Sprintf("%g", val) // 使用 %g 去掉不必要的零
					case int:
						cellValues[k] = fmt.Sprintf("%d", val)
					case int64:
						cellValues[k] = fmt.Sprintf("%d", val)
					default:
						cellValues[k] = fmt.Sprintf("%v", val)
					}
				}
				result[i][j] = strings.Join(cellValues, ", ")
			} else {
				result[i][j] = ""
			}
		}
	}

	// 获取合并单元格信息和网格属性（使用 v3 GetSheet API）
	var merges []*SheetMergeInfo
	var frozenRow, frozenCol, rowCount, colCount int64
	sheetResp, _, err := c.larkClient.Drive.GetSheet(ctx, &lark.GetSheetReq{
		SpreadSheetToken: spreadsheetToken,
		SheetID:          sheetID,
	}, c.methodOptions()...)
	if err == nil && sheetResp != nil && sheetResp.Sheet != nil {
		// 转换合并信息格式
		// 注意：v3 API 返回的 EndRowIndex/EndColumnIndex 是 inclusive（包含）的
		for _, merge := range sheetResp.Sheet.Merges {
			merges = append(merges, &SheetMergeInfo{
				StartRowIndex:    merge.StartRowIndex,
				StartColumnIndex: merge.StartColumnIndex,
				RowSpan:          merge.EndRowIndex - merge.StartRowIndex + 1,
				ColSpan:          merge.EndColumnIndex - merge.StartColumnIndex + 1,
			})
		}
		if grid := sheetResp.Sheet.GridProperties; grid != nil {
			frozenRow = grid.FrozenRowCount
			frozenCol = grid.FrozenColumnCount
			rowCount = grid.RowCount
			colCount = grid.ColumnCount
		}
	}
	// 如果获取合并信息失败，静默降级（merges 为空，网格属性为 0）

	return &SheetData{
		Values:            result,
		Merges:            merges,
		FrozenRowCount:    frozenRow,
		FrozenColumnCount: frozenCol,
		RowCount:          rowCount,
		ColumnCount:       colCount,
	}, nil
}

// SheetMeta 工作表元信息
type SheetMeta struct {
	SheetID string
	Title   string
	Index   int64
	Hidden  bool
}

// ListSpreadsheetSheets 列出 spreadsheet 下所有工作表（包含隐藏的，由调用方决定是否过滤）
func (c *Client) ListSpreadsheetSheets(ctx context.Context, spreadsheetToken string) ([]SheetMeta, error) {
	resp, _, err := c.larkClient.Drive.GetSheetList(ctx, &lark.GetSheetListReq{
		SpreadSheetToken: spreadsheetToken,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("GetSheetList: %w", err)
	}
	metas := make([]SheetMeta, 0, len(resp.Sheets))
	for _, s := range resp.Sheets {
		metas = append(metas, SheetMeta{
			SheetID: s.SheetID,
			Title:   s.Title,
			Index:   s.Index,
			Hidden:  s.Hidden,
		})
	}
	return metas, nil
}

// ExportSheet 导出电子表格为 xlsx 文件
func (c *Client) ExportSheet(ctx context.Context, sheetToken, outDir, title string) (string, error) {
	// 1. 创建导出任务
	createResp, _, err := c.larkClient.Drive.CreateDriveExportTask(ctx, &lark.CreateDriveExportTaskReq{
		FileExtension: "xlsx",
		Token:         sheetToken,
		Type:          "sheet",
	}, c.methodOptions()...)
	if err != nil {
		return "", fmt.Errorf("创建导出任务失败: %w", err)
	}

	// 导出任务状态
	const (
		exportJobSuccess    = 0
		exportJobInit       = 1
		exportJobProcessing = 2
	)

	// 2. 轮询任务状态 (最多等待 60 秒)
	var fileToken string
	for i := 0; i < 30; i++ {
		getResp, _, err := c.larkClient.Drive.GetDriveExportTask(ctx, &lark.GetDriveExportTaskReq{
			Ticket: createResp.Ticket,
			Token:  sheetToken,
		}, c.methodOptions()...)
		if err != nil {
			return "", fmt.Errorf("查询导出任务状态失败: %w", err)
		}

		if getResp.Result.JobStatus == exportJobSuccess {
			fileToken = getResp.Result.FileToken
			break
		} else if getResp.Result.JobStatus == exportJobInit || getResp.Result.JobStatus == exportJobProcessing {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		} else {
			// 其他状态表示失败
			return "", fmt.Errorf("导出任务失败，状态码: %d, 错误信息: %s",
				getResp.Result.JobStatus, getResp.Result.JobErrorMsg)
		}
	}

	if fileToken == "" {
		return "", fmt.Errorf("导出任务超时")
	}

	// 3. 下载导出文件
	downloadResp, _, err := c.larkClient.Drive.DownloadDriveExportTask(ctx, &lark.DownloadDriveExportTaskReq{
		FileToken: fileToken,
	}, c.methodOptions()...)
	if err != nil {
		return "", fmt.Errorf("下载导出文件失败: %w", err)
	}

	// 4. 保存到文件
	filename := utils.SanitizeFileName(title) + ".xlsx"
	filePath := filepath.Join(outDir, filename)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, downloadResp.File); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	return filePath, nil
}

// GetBitableContent 获取多维表格的内容
func (c *Client) GetBitableContent(ctx context.Context, bitableToken string) ([][]string, error) {
	// bitableToken 的格式是：app_token + "_" + table_id
	// 例如：CZJHb9XisaEsWosyB1pcAk2WnRg_tblxxxxx
	// 需要解析出 app_token 和 table_id

	// 查找最后一个下划线，分隔 app_token 和 table_id
	lastUnderscore := strings.LastIndex(bitableToken, "_")
	if lastUnderscore == -1 {
		return nil, fmt.Errorf("invalid bitable token format (missing underscore separator): %s", bitableToken)
	}

	appToken := bitableToken[:lastUnderscore]
	tableID := bitableToken[lastUnderscore+1:]

	// 1. 获取表格的字段信息
	fieldResp, _, err := c.larkClient.Bitable.GetBitableFieldList(ctx, &lark.GetBitableFieldListReq{
		AppToken: appToken,
		TableID:  tableID,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to get bitable fields: %w", err)
	}

	// 2. 获取表格的记录
	recordResp, _, err := c.larkClient.Bitable.GetBitableRecordList(ctx, &lark.GetBitableRecordListReq{
		AppToken: appToken,
		TableID:  tableID,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to get bitable records: %w", err)
	}

	// 3. 构建表格数据
	// 第一行是字段名
	var result [][]string

	// 添加表头（字段名）
	if len(fieldResp.Items) > 0 {
		var header []string
		for _, field := range fieldResp.Items {
			header = append(header, field.FieldName)
		}
		result = append(result, header)
	}

	// 添加数据行
	if len(recordResp.Items) > 0 {
		for _, record := range recordResp.Items {
			var row []string
			for _, field := range fieldResp.Items {
				// 从记录中获取字段值（飞书 API 返回的 Fields map 使用 FieldName 作为 key）
				if value, ok := record.Fields[field.FieldName]; ok {
					// 将值转换为字符串
					row = append(row, fmt.Sprintf("%v", value))
				} else {
					row = append(row, "")
				}
			}
			result = append(result, row)
		}
	}

	return result, nil
}

// DownloadWhiteboardImage 下载白板为图片
// 返回: (文件路径, 图片数据, 文件名, 错误)
func (c *Client) DownloadWhiteboardImage(ctx context.Context, whiteboardID, outDir string) (filePath string, data []byte, filename string, err error) {
	resp, response, err := c.larkClient.Drive.DownloadBoardWhiteboardAsImage(ctx, &lark.DownloadBoardWhiteboardAsImageReq{
		WhiteboardID: whiteboardID,
	}, c.methodOptions()...)
	if err != nil {
		return "", nil, "", fmt.Errorf("下载白板图片失败: %w", err)
	}

	// 读取响应体
	data, err = io.ReadAll(resp.File)
	if err != nil {
		return "", nil, "", fmt.Errorf("读取响应失败: %w", err)
	}

	// 从 Content-Type 解析图片格式，octet-stream 时用 magic bytes 嗅探
	contentType := response.Header.Get("Content-Type")
	if !isImageContent(contentType, data) {
		return "", nil, "", fmt.Errorf("白板图片下载失败 (%s): Content-Type=%s, body=%s", whiteboardID, contentType, data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(data)
	}
	var ext string
	switch {
	case strings.Contains(contentType, "image/png"):
		ext = ".png"
	case strings.Contains(contentType, "image/jpeg"):
		ext = ".jpg"
	case strings.Contains(contentType, "image/gif"):
		ext = ".gif"
	case strings.Contains(contentType, "image/svg+xml"):
		ext = ".svg"
	default:
		ext = ".png" // 默认 PNG
	}

	// 保存到文件
	filename = fmt.Sprintf("whiteboard_%s%s", whiteboardID, ext)
	filePath = filepath.Join(outDir, filename)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, "", fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return "", nil, "", fmt.Errorf("写入文件失败: %w", err)
	}

	// 使用 mogrify -trim 裁剪图片空白边距
	if err := exec.CommandContext(ctx, "mogrify", "-trim", filePath).Run(); err != nil {
		log.Printf("警告: 裁剪白板图片空白边距失败 %s: %v (可通过 brew install imagemagick 安装)", filePath, err)
	} else {
		// 裁剪成功，重新读取裁剪后的文件数据（用于缓存）
		if trimmedData, readErr := os.ReadFile(filePath); readErr == nil {
			data = trimmedData
		}
	}

	return filePath, data, filename, nil
}

// ConvertBlocksResult Markdown 转换为文档块的结果
type ConvertBlocksResult struct {
	FirstLevelBlockIDs []string          // 一级块 ID 列表
	Blocks             []*lark.DocxBlock // 所有块
}

// ConvertMarkdownToBlocks 调用飞书 API 将 Markdown 转换为文档块
// API: POST /open-apis/docx/v1/documents/blocks/convert
func (c *Client) ConvertMarkdownToBlocks(ctx context.Context, content string) (*ConvertBlocksResult, error) {
	resp, _, err := c.larkClient.Drive.CreateDocxDocumentConvert(ctx, &lark.CreateDocxDocumentConvertReq{
		ContentType: "markdown",
		Content:     content,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("转换失败: %w", err)
	}
	return &ConvertBlocksResult{
		FirstLevelBlockIDs: resp.FirstLevelBlockIDs,
		Blocks:             resp.Blocks,
	}, nil
}

// CreateWikiNode 在 Wiki 知识库中创建节点（文档）
func (c *Client) CreateWikiNode(ctx context.Context, spaceID string, parentNodeToken *string, title string) (*lark.CreateWikiNodeRespNode, error) {
	titlePtr := title
	resp, _, err := c.larkClient.Drive.CreateWikiNode(ctx, &lark.CreateWikiNodeReq{
		SpaceID:         spaceID,
		ObjType:         "docx",
		ParentNodeToken: parentNodeToken,
		NodeType:        "origin",
		Title:           &titlePtr,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("创建 Wiki 节点失败: %w", err)
	}
	return resp.Node, nil
}

// GetDocxDocument 获取文档基本信息（包括 revision_id）
func (c *Client) GetDocxDocument(ctx context.Context, documentID string) (*lark.DocxDocument, error) {
	resp, _, err := c.larkClient.Drive.GetDocxDocument(ctx, &lark.GetDocxDocumentReq{
		DocumentID: documentID,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("获取文档信息失败: %w", err)
	}
	return &lark.DocxDocument{
		DocumentID: resp.Document.DocumentID,
		RevisionID: resp.Document.RevisionID,
		Title:      resp.Document.Title,
	}, nil
}

// GetDocxBlockChildren 获取文档根节点的子块列表
func (c *Client) GetDocxBlockChildren(ctx context.Context, documentID string) ([]*lark.DocxBlock, error) {
	var blocks []*lark.DocxBlock
	var pageToken *string
	for {
		resp, _, err := c.larkClient.Drive.GetDocxBlockListOfDocument(ctx, &lark.GetDocxBlockListOfDocumentReq{
			DocumentID: documentID,
			PageToken:  pageToken,
			UserIDType: userIDTypePtr(), // 全局用户身份策略：统一 union_id
		}, c.methodOptions()...)
		if err != nil {
			return nil, fmt.Errorf("获取块列表失败: %w", err)
		}
		blocks = append(blocks, resp.Items...)
		pageToken = &resp.PageToken
		if !resp.HasMore {
			break
		}
	}
	return blocks, nil
}

// BatchDeleteDocxBlocks 批量删除文档块
// 删除文档根节点下指定范围的子块。
func (c *Client) BatchDeleteDocxBlocks(ctx context.Context, documentID string, startIndex, endIndex int64) (*lark.BatchDeleteDocxBlockResp, error) {
	revisionID := int64(-1)
	resp, _, err := c.larkClient.Drive.BatchDeleteDocxBlock(ctx, &lark.BatchDeleteDocxBlockReq{
		DocumentID:         documentID,
		BlockID:            documentID,
		DocumentRevisionID: &revisionID,
		StartIndex:         startIndex,
		EndIndex:           endIndex,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("批量删除块失败: %w", err)
	}
	return resp, nil
}

// docxBlocksToReqDescendants 借助 JSON tag 兼容性把 fork DocxBlock 序列化后
// 反序列化为上游 ReqDescendant 类型，避免维护 30+ block 子类型的显式映射。
func docxBlocksToReqDescendants(blocks []*lark.DocxBlock) ([]*lark.CreateDocxDocumentBlockDescendantReqDescendant, error) {
	data, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	var dst []*lark.CreateDocxDocumentBlockDescendantReqDescendant
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// CreateDocxDescendant 使用 descendant API 创建嵌套块结构
// 用于 Table, Callout, QuoteContainer, 嵌套列表等
func (c *Client) CreateDocxDescendant(
	ctx context.Context,
	documentID, parentBlockID string,
	childrenIDs []string,
	descendants []*lark.DocxBlock,
	index int,
) (*lark.CreateDocxDocumentBlockDescendantResp, error) {
	reqDescendants, err := docxBlocksToReqDescendants(descendants)
	if err != nil {
		return nil, fmt.Errorf("转换 descendant 块失败: %w", err)
	}
	revisionID := int64(-1)
	indexInt64 := int64(index)
	resp, _, err := c.larkClient.Drive.CreateDocxDocumentBlockDescendant(ctx, &lark.CreateDocxDocumentBlockDescendantReq{
		DocumentID:         documentID,
		BlockID:            parentBlockID,
		DocumentRevisionID: &revisionID,
		UserIDType:         userIDTypePtr(), // 全局用户身份策略：统一 union_id
		ChildrenID:         childrenIDs,
		Descendants:        reqDescendants,
		Index:              &indexInt64,
	}, c.methodOptions()...)
	if err != nil {
		return nil, fmt.Errorf("创建嵌套块失败: %w", err)
	}
	return resp, nil
}

// MergeTableCells 合并表格单元格。
func (c *Client) MergeTableCells(ctx context.Context, documentID, blockID string, region MergeRegion) error {
	revisionID := int64(-1)
	_, _, err := c.larkClient.Drive.UpdateDocxBlock(ctx, &lark.UpdateDocxBlockReq{
		DocumentID:         documentID,
		BlockID:            blockID,
		DocumentRevisionID: &revisionID,
		MergeTableCells: &lark.UpdateDocxBlockReqMergeTableCells{
			RowStartIndex:    region.RowStartIndex,
			RowEndIndex:      region.RowEndIndex,
			ColumnStartIndex: region.ColumnStartIndex,
			ColumnEndIndex:   region.ColumnEndIndex,
		},
	}, c.methodOptions()...)
	if err != nil {
		return fmt.Errorf("合并表格单元格失败: %w", err)
	}
	return nil
}

// BatchUpdateDocxBlocks 批量更新文档块
// 每次最多 200 个块，每个 block_id 只能出现一次。
func (c *Client) BatchUpdateDocxBlocks(ctx context.Context, documentID string, requests []*lark.BatchUpdateDocxDocumentBlockReqRequest) error {
	revisionID := int64(-1)
	_, _, err := c.larkClient.Drive.BatchUpdateDocxDocumentBlock(ctx, &lark.BatchUpdateDocxDocumentBlockReq{
		DocumentID:         documentID,
		DocumentRevisionID: &revisionID,
		UserIDType:         userIDTypePtr(), // 全局用户身份策略：统一 union_id
		Requests:           requests,
	}, c.methodOptions()...)
	if err != nil {
		return fmt.Errorf("批量更新块失败: %w", err)
	}
	return nil
}

// 画板样式/语法常量
const (
	whiteboardStyleClassic   = 2 // 经典样式，可二次编辑
	whiteboardSyntaxPlantUML = 1 // PlantUML 语法
)

// CreateWhiteboardPlantUML 向画板中添加 PlantUML 节点
func (c *Client) CreateWhiteboardPlantUML(ctx context.Context, boardToken, plantUMLCode string) error {
	styleType := int64(whiteboardStyleClassic)
	syntaxType := int64(whiteboardSyntaxPlantUML)
	_, _, err := c.larkClient.Drive.CreateBoardWhiteboardNodePlantuml(ctx, &lark.CreateBoardWhiteboardNodePlantumlReq{
		WhiteboardID: boardToken,
		PlantUmlCode: plantUMLCode,
		StyleType:    &styleType,
		SyntaxType:   &syntaxType,
	}, c.methodOptions()...)
	if err != nil {
		return fmt.Errorf("创建 PlantUML 节点失败: %w", err)
	}
	return nil
}

// RecognizeBasicImage 调用飞书 AI OCR 识别图片中的文字
func (c *Client) RecognizeBasicImage(ctx context.Context, req *lark.RecognizeBasicImageReq) (*lark.RecognizeBasicImageResp, *lark.Response, error) {
	return c.larkClient.AI.RecognizeBasicImage(ctx, req, c.methodOptions()...)
}
