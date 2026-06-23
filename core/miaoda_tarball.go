package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// 妙搭（Miaoda）html-publish 接口约束（与官方 lark-cli 对齐）
const (
	// maxMiaodaTarballBytes 是打包后 tar.gz 的体积上限（接口约束约 20MB）
	maxMiaodaTarballBytes = 20 * 1024 * 1024
	// maxMiaodaRawBytes 是打包前未压缩内容的上限，防止高压缩比内容撑爆内存
	maxMiaodaRawBytes = 200 * 1024 * 1024
	// miaodaEntryFile 是妙搭应用的入口文件名
	miaodaEntryFile = "index.html"
)

// miaodaSkipNames 是打包时跳过的目录/文件：体积大且与网页产物无关，
// 避免误打包触发体积上限。
var miaodaSkipNames = map[string]bool{
	".git":              true,
	"node_modules":      true,
	".DS_Store":         true,
	publishManifestName: true, // 发布状态边车文件，不应打进产物
}

// miaodaFile 是待打包的单个文件
type miaodaFile struct {
	absPath string
	relPath string // tar 内的相对路径，统一使用 / 分隔
	size    int64
}

// buildMiaodaTarball 把本地文件或目录打包成 tar.gz，返回内存字节。
//   - path 为单文件：统一打包为 index.html（单文件产物入口即该文件）
//   - path 为目录：递归打包，根目录必须含 index.html
func buildMiaodaTarball(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var files []miaodaFile
	if info.IsDir() {
		files, err = collectMiaodaDirFiles(path)
		if err != nil {
			return nil, err
		}
		if !hasIndexHTML(files) {
			return nil, fmt.Errorf("目录 %s 根目录缺少 %s（妙搭以 %s 作为应用入口）", path, miaodaEntryFile, miaodaEntryFile)
		}
	} else {
		files = []miaodaFile{{absPath: path, relPath: miaodaEntryFile, size: info.Size()}}
	}

	var raw int64
	for _, f := range files {
		raw += f.size
	}
	if raw > maxMiaodaRawBytes {
		return nil, fmt.Errorf("内容未压缩体积 %d 字节超过 %d 上限，请精简目录", raw, int64(maxMiaodaRawBytes))
	}

	data, err := packTarGz(files)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMiaodaTarballBytes {
		return nil, fmt.Errorf("打包后 tar.gz 体积 %d 字节超过约 20MB 上限，请精简内容", len(data))
	}
	return data, nil
}

// collectMiaodaDirFiles 递归收集目录下所有文件，跳过 miaodaSkipNames。
func collectMiaodaDirFiles(root string) ([]miaodaFile, error) {
	var files []miaodaFile
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if miaodaSkipNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if miaodaSkipNames[d.Name()] {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, miaodaFile{
			absPath: p,
			relPath: filepath.ToSlash(rel),
			size:    info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func hasIndexHTML(files []miaodaFile) bool {
	for _, f := range files {
		if f.relPath == miaodaEntryFile {
			return true
		}
	}
	return false
}

// packTarGz 将文件列表打包成 tar.gz 字节流。
func packTarGz(files []miaodaFile) ([]byte, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("没有可打包的文件")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := writeTarFile(tw, f); err != nil {
			tw.Close()
			gz.Close()
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeTarFile(tw *tar.Writer, f miaodaFile) error {
	src, err := os.Open(f.absPath)
	if err != nil {
		return err
	}
	defer src.Close()
	hdr := &tar.Header{
		Name:     f.relPath,
		Size:     f.size,
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.Copy(tw, src); err != nil {
		return err
	}
	return nil
}
