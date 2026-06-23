package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// ImageMeta 缓存元数据，使用 JSON 格式便于扩展
type ImageMeta struct {
	Filename string `json:"filename"`
	Version  int    `json:"version"`
}

const currentMetaVersion = 1

// ImageCache 管理图片缓存，避免重复下载
type ImageCache struct {
	cacheDir       string
	filesCacheDir  string
	boardsCacheDir string
}

// NewImageCache 创建图片缓存实例，缓存目录为 ~/.cache/feishu2md/{images,files,whiteboards}/
func NewImageCache() *ImageCache {
	cp, _ := DefaultCachePaths()
	return NewImageCacheWithPaths(cp)
}

// NewImageCacheWithPaths 用注入的 CachePaths 构造（测试传临时目录）。
func NewImageCacheWithPaths(cp CachePaths) *ImageCache {
	return &ImageCache{
		cacheDir:       cp.ImagesDir(),
		filesCacheDir:  cp.FilesDir(),
		boardsCacheDir: cp.WhiteboardsDir(),
	}
}

// Get 检查缓存是否存在，返回 (缓存文件路径, 原始文件名)
// 如果缓存不存在，返回空字符串
func (c *ImageCache) Get(imgToken string) (cachePath string, filename string) {
	dataPath := filepath.Join(c.cacheDir, imgToken)
	metaPath := dataPath + ".meta"

	// 检查数据文件和元数据文件都存在
	if _, err := os.Stat(dataPath); err != nil {
		return "", ""
	}
	if _, err := os.Stat(metaPath); err != nil {
		return "", ""
	}

	// 读取并解析 JSON 元数据
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return "", ""
	}

	var meta ImageMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", ""
	}

	return dataPath, meta.Filename
}

// Put 将图片数据和元数据写入缓存
func (c *ImageCache) Put(imgToken string, data []byte, filename string) error {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return err
	}

	dataPath := filepath.Join(c.cacheDir, imgToken)
	metaPath := dataPath + ".meta"

	// 写入数据文件
	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		return err
	}

	// 写入 JSON 格式元数据
	meta := ImageMeta{Filename: filename, Version: currentMetaVersion}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, metaJSON, 0o644)
}

// CopyTo 复制缓存文件到目标路径
func (c *ImageCache) CopyTo(cachePath, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	src, err := os.Open(cachePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// GetFile 检查附件缓存是否存在，返回 (缓存文件路径, 原始文件名)
func (c *ImageCache) GetFile(fileToken string) (cachePath string, filename string) {
	dataPath := filepath.Join(c.filesCacheDir, fileToken)
	metaPath := dataPath + ".meta"

	if _, err := os.Stat(dataPath); err != nil {
		return "", ""
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return "", ""
	}
	var meta ImageMeta
	if json.Unmarshal(metaData, &meta) != nil {
		return "", ""
	}

	return dataPath, meta.Filename
}

// PutFile 将附件数据和元数据写入缓存
func (c *ImageCache) PutFile(fileToken string, data []byte, filename string) error {
	if err := os.MkdirAll(c.filesCacheDir, 0o755); err != nil {
		return err
	}

	dataPath := filepath.Join(c.filesCacheDir, fileToken)
	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		return err
	}

	meta := ImageMeta{Filename: filename, Version: currentMetaVersion}
	metaData, _ := json.Marshal(meta)
	return os.WriteFile(dataPath+".meta", metaData, 0o644)
}

// WhiteboardMeta 白板缓存元数据
type WhiteboardMeta struct {
	Filename    string `json:"filename"`
	ObjEditTime string `json:"obj_edit_time"`
}

// GetWhiteboard 获取白板缓存，验证 obj_edit_time 是否匹配
// 返回 (缓存文件路径, 原始文件名)，如果缓存不存在或版本不匹配则返回空字符串
func (c *ImageCache) GetWhiteboard(boardToken, objEditTime string) (cachePath, origFilename string) {
	metaPath := filepath.Join(c.boardsCacheDir, boardToken+".meta")
	dataPath := filepath.Join(c.boardsCacheDir, boardToken)

	// 读取 meta
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return "", ""
	}
	var meta WhiteboardMeta
	if json.Unmarshal(metaData, &meta) != nil {
		return "", ""
	}

	// 验证 obj_edit_time
	if meta.ObjEditTime != objEditTime {
		return "", "" // 版本不匹配，缓存失效
	}

	// 检查数据文件
	if _, err := os.Stat(dataPath); err != nil {
		return "", ""
	}

	return dataPath, meta.Filename
}

// PutWhiteboard 将白板图片数据写入缓存
func (c *ImageCache) PutWhiteboard(boardToken string, data []byte, filename, objEditTime string) error {
	if err := os.MkdirAll(c.boardsCacheDir, 0o755); err != nil {
		return err
	}

	dataPath := filepath.Join(c.boardsCacheDir, boardToken)
	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		return err
	}

	meta := WhiteboardMeta{Filename: filename, ObjEditTime: objEditTime}
	metaData, _ := json.Marshal(meta)
	return os.WriteFile(filepath.Join(c.boardsCacheDir, boardToken+".meta"), metaData, 0o644)
}
