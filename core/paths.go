package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// appDirName 是配置/缓存/状态根目录名。
// 刻意保留 feishu2md（零迁移兼容老用户的已有配置与缓存），改动会让老数据失联。
const appDirName = "feishu2md"

// StatePaths 定位「不可重建的持久状态」目录（config / board 映射 / publish 记录）。
// 落在 os.UserConfigDir() 下而非缓存目录，避免被系统缓存清理误删。base 可注入以便测试。
type StatePaths struct{ base string }

// DefaultStatePaths 返回 os.UserConfigDir()/feishu2md 根的 StatePaths。
func DefaultStatePaths() (StatePaths, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return StatePaths{}, err
	}
	return StatePaths{base: filepath.Join(dir, appDirName)}, nil
}

// NewStatePaths 用显式 base 构造（测试注入临时目录）。base 即 .../feishu2md 那一层。
func NewStatePaths(base string) StatePaths { return StatePaths{base: base} }

// ConfigFile 返回配置文件路径 <base>/config.json。
func (p StatePaths) ConfigFile() string { return filepath.Join(p.base, "config.json") }

// BoardsDir 返回画板映射目录 <base>/boards。
func (p StatePaths) BoardsDir() string { return filepath.Join(p.base, "boards") }

// PublishDir 返回发布记录目录 <base>/publish。
func (p StatePaths) PublishDir() string { return filepath.Join(p.base, "publish") }

// MediaDir 返回媒体映射目录 <base>/media。
func (p StatePaths) MediaDir() string { return filepath.Join(p.base, "media") }

// BoardManifestFile 按 document_id 定位画板映射文件 <base>/boards/<document_id>.yaml。
// document_id 是飞书文档 token（纯 ASCII），可安全用作文件名。
func (p StatePaths) BoardManifestFile(documentID string) string {
	return filepath.Join(p.BoardsDir(), documentID+".yaml")
}

// MediaManifestFile 按 document_id 定位媒体映射文件 <base>/media/<document_id>.yaml。
func (p StatePaths) MediaManifestFile(documentID string) string {
	return filepath.Join(p.MediaDir(), documentID+".yaml")
}

// PublishManifestFile 按 target 绝对路径的 sha256 定位发布记录文件 <base>/publish/<sha256>.yaml。
func (p StatePaths) PublishManifestFile(absTarget string) string {
	return filepath.Join(p.PublishDir(), publishKey(absTarget)+".yaml")
}

// CachePaths 定位「可重建的缓存」目录（图片 / 附件 / 白板）。落在 os.UserCacheDir() 下。
type CachePaths struct{ base string }

// DefaultCachePaths 返回 os.UserCacheDir()/feishu2md 根的 CachePaths。
func DefaultCachePaths() (CachePaths, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return CachePaths{}, err
	}
	return CachePaths{base: filepath.Join(dir, appDirName)}, nil
}

// NewCachePaths 用显式 base 构造（测试注入临时目录）。
func NewCachePaths(base string) CachePaths { return CachePaths{base: base} }

// ImagesDir 返回图片缓存目录 <base>/images。
func (p CachePaths) ImagesDir() string { return filepath.Join(p.base, "images") }

// FilesDir 返回附件缓存目录 <base>/files。
func (p CachePaths) FilesDir() string { return filepath.Join(p.base, "files") }

// WhiteboardsDir 返回白板缓存目录 <base>/whiteboards。
func (p CachePaths) WhiteboardsDir() string { return filepath.Join(p.base, "whiteboards") }

// DownloadsDir 返回下载版本记录目录 <base>/downloads。
func (p CachePaths) DownloadsDir() string { return filepath.Join(p.base, "downloads") }

// DownloadManifestFile 按 document_id 定位下载版本记录 <base>/downloads/<document_id>.yaml。
func (p CachePaths) DownloadManifestFile(documentID string) string {
	return filepath.Join(p.DownloadsDir(), documentID+".yaml")
}

// publishKey 计算 target 路径的稳定 key：先 filepath.Clean 归一，再取 sha256 全量 hex。
// 用 hash 而非编码原路径，天然消除空格/中文/分隔符等特殊字符问题。
// 注意：调用方负责先转绝对路径（filepath.Abs 依赖 cwd，属不确定来源，留在外壳层）。
func publishKey(absTarget string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(absTarget)))
	return fmt.Sprintf("%x", sum)
}

// writeFileAtomic 原子写文件：在目标同目录写临时文件，再 rename 覆盖（同文件系统 rename 原子）。
// 自动创建父目录。避免并发写或写入中断产生半截/损坏文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op；任何失败路径下清理临时文件
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
