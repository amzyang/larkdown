package core

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/chyroc/lark"
)

// 媒体实体（图片/附件/视频）round-trip 保留的统一抽象。
//
// 飞书无独立视频类型——上传的视频即 File 附件（靠 view_type 卡片/预览展示），故实体类型
// 只有 Image(27) 与 File(23) 两类。File 在远程可能被 View(33) 块包裹（view 是 file 的父块）。
//
// 与白板（Board 43）同属「可保留实体」：diff 按 token 认出、原地保留、复用远程已存在的块，
// 上传后按 markdown 顺序移回原位（block_move_after）。区别：白板 token 只读、删除不可逆，
// 保留是刚需；媒体实体可删可重传，保留是优化 + 避免素材重传/孤儿 media/token 抖动。

// entityRef 媒体实体的远程身份。
// innerBlockID 始终指向真实 image/file 块（View 包裹时为内层块），replace_image/replace_file 需要它。
type entityRef struct {
	token        string
	innerBlockID string
	isFile       bool
}

// entityRefFromBlock 从远程根级块解析媒体实体身份，穿透 View(33) 包裹。
// 非媒体实体（或缺 token）返回 ok=false。
func entityRefFromBlock(block *lark.DocxBlock, blockMap map[string]*lark.DocxBlock) (entityRef, bool) {
	if block == nil {
		return entityRef{}, false
	}
	switch block.BlockType {
	case lark.DocxBlockTypeImage:
		if block.Image != nil && block.Image.Token != "" {
			return entityRef{token: block.Image.Token, innerBlockID: block.BlockID}, true
		}
	case lark.DocxBlockTypeFile:
		if block.File != nil && block.File.Token != "" {
			return entityRef{token: block.File.Token, innerBlockID: block.BlockID, isFile: true}, true
		}
	case lark.DocxBlockTypeView:
		// View 包裹：内层 children 含真实 file/image 块
		for _, childID := range block.Children {
			if ref, ok := entityRefFromBlock(blockMap[childID], blockMap); ok {
				return ref, true
			}
		}
	}
	return entityRef{}, false
}

// remoteEntityTokens 收集远程根级块中所有媒体实体 token（穿透 view）。
func remoteEntityTokens(rootBlocks []*lark.DocxBlock, blockMap map[string]*lark.DocxBlock) map[string]bool {
	tokens := map[string]bool{}
	for _, b := range rootBlocks {
		if ref, ok := entityRefFromBlock(b, blockMap); ok {
			tokens[ref.token] = true
		}
	}
	return tokens
}

// isRoundTripEntity 判断本地块是否为可保留的 round-trip 实体：带 token 的白板/图片/文件。
// 这类块上传时复用远程已存在的块、不重建。图片/文件的 token 由 resolveEntityTokens 在上传前解析写入。
func isRoundTripEntity(b *lark.DocxBlock) bool {
	return isRoundTripBoard(b) || localEntityToken(b) != ""
}

// localEntityToken 返回本地 image/file 块已解析的 token；非实体或未解析返回 ""。
func localEntityToken(b *lark.DocxBlock) string {
	if b == nil {
		return ""
	}
	switch b.BlockType {
	case lark.DocxBlockTypeImage:
		if b.Image != nil {
			return b.Image.Token
		}
	case lark.DocxBlockTypeFile:
		if b.File != nil {
			return b.File.Token
		}
	}
	return ""
}

// resolveEntityTokens 为本地图片/文件块解析 round-trip token。
// 下载产物文件名形如 <token>_<原名>；用本文档远程 token 集合做最长 "<token>_" 前缀匹配
// （token 自身含下划线，不能简单切分），命中即把 token 写回本地块，供签名/保留逻辑识别。
// 编辑后的同名文件仍命中原 token（身份不变）；内容是否变化交给 md5 pass。
func resolveEntityTokens(result *ConvertResult, remoteTokens map[string]bool) {
	if result == nil || len(remoteTokens) == 0 {
		return
	}
	for i, idx := range result.ImageIndices {
		if b := result.TopBlocks[idx]; b != nil && b.Image != nil {
			if t := matchTokenByPrefix(filepath.Base(result.ImagePaths[i]), remoteTokens); t != "" {
				b.Image.Token = t
			}
		}
	}
	for i, idx := range result.FileIndices {
		if b := result.TopBlocks[idx]; b != nil && b.File != nil {
			if t := matchTokenByPrefix(filepath.Base(result.FilePaths[i]), remoteTokens); t != "" {
				b.File.Token = t
			}
		}
	}
}

// matchTokenByPrefix 在 tokens 中找 basename 的最长 "<token>_" 前缀匹配（无匹配返回 ""）。
// 取最长以消歧 token 互为前缀的情形。
func matchTokenByPrefix(basename string, tokens map[string]bool) string {
	best := ""
	for t := range tokens {
		if len(t) > len(best) && strings.HasPrefix(basename, t+"_") {
			best = t
		}
	}
	return best
}

// preservableEntityToken 返回远程根级块作为「可保留实体」的 token（白板/图片/文件，穿透 view）；
// 非可保留实体返回 ""。供删除保留判定与位置校正复用。
func preservableEntityToken(b *lark.DocxBlock, blockMap map[string]*lark.DocxBlock) string {
	if isRoundTripBoard(b) {
		return b.Board.Token
	}
	if ref, ok := entityRefFromBlock(b, blockMap); ok {
		return ref.token
	}
	return ""
}

// localRoundTripToken 返回本地块作为 round-trip 实体的 token（白板/图片/文件）；非实体返回 ""。
func localRoundTripToken(b *lark.DocxBlock) string {
	if isRoundTripBoard(b) {
		return b.Board.Token
	}
	return localEntityToken(b)
}

// localEntityTokenSet 收集本地 markdown 中仍引用的 round-trip 实体 token（白板/图片/文件）。
func localEntityTokenSet(result *ConvertResult) map[string]bool {
	tokens := map[string]bool{}
	if result == nil {
		return tokens
	}
	for _, b := range result.TopBlocks {
		if isRoundTripBoard(b) {
			tokens[b.Board.Token] = true
		} else if t := localEntityToken(b); t != "" {
			tokens[t] = true
		}
	}
	return tokens
}

// bytesMD5 返回字节内容 md5（hex）。供「上传字节算基准 md5」与文件读取共用。
func bytesMD5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// fileMD5 返回文件内容 md5；读取失败 ok=false。
func fileMD5(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return bytesMD5(data), true
}

// cachedEntityMD5 返回缓存中该 token 原始字节的 md5；缓存缺失 ok=false。
func cachedEntityMD5(cache *ImageCache, token string, isFile bool) (string, bool) {
	var cachePath string
	if isFile {
		cachePath, _ = cache.GetFile(token)
	} else {
		cachePath, _ = cache.Get(token)
	}
	if cachePath == "" {
		return "", false
	}
	return fileMD5(cachePath)
}

// isRemoteURL 判断资源路径是否为 http(s) 远程地址。
func isRemoteURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// resolveLocalPath 把 markdown 中的相对资源路径按 mdDir 解析为可读路径；绝对路径原样返回。
// 与 resolveImageData 共用，确保「读字节上传」与「读字节算 md5」路径解析口径一致。
func resolveLocalPath(path, mdDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(mdDir, path)
}

// entityContentChanged 判断本地实体内容相对下载时是否已变化。
// 比对本地文件 md5 与缓存（~/.cache/feishu2md/images|files/<token>）中原始字节 md5。
// localPath 为 markdown 中的原始资源路径（相对路径按 mdDir 解析，与上传读字节口径一致）。
// 远程 URL（无本地编辑语义、无法 md5 比对）或缓存缺失 → 默认未变（保留、不重传；编辑检测为 best-effort）。
func entityContentChanged(cache *ImageCache, ref entityRef, localPath, mdDir string) bool {
	if isRemoteURL(localPath) {
		return false
	}
	cm, ok := cachedEntityMD5(cache, ref.token, ref.isFile)
	if !ok {
		return false
	}
	lm, ok := fileMD5(resolveLocalPath(localPath, mdDir))
	if !ok {
		return false
	}
	return cm != lm
}

// mediaContentChanged 用 sidecar 记录的基准 md5 判断本地媒体是否已变（纯函数）。
// 路径映射方案的变更基准来自媒体映射记录（上传时的内容 md5），不依赖下载缓存——
// 新建文档的缓存里没有该 token 的原始字节，用缓存会漏判已变。
func mediaContentChanged(baselineMD5, localMD5 string) bool {
	return baselineMD5 != localMD5
}

// entityLocalPath 返回本地第 idx 个顶级块对应的实体文件路径（image 或 file）。
func entityLocalPath(result *ConvertResult, idx int, isFile bool) string {
	if isFile {
		return findFilePath(result, idx)
	}
	return findImagePath(result, idx)
}
