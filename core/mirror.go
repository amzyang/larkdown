package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amzyang/larkdown/utils"
	"gopkg.in/yaml.v3"
)

// MirrorManifestFilename 镜像根目录下的元数据边车文件名。
// 记录同步来源 URL，供 `larkdown mirror`（无参数）在镜像目录内重新同步。
const MirrorManifestFilename = ".larkdown-mirror.yaml"

// MirrorManifest 镜像元数据
type MirrorManifest struct {
	Source string `yaml:"source"`
}

// ReadMirrorManifest 读取镜像目录下的元数据边车；文件不存在返回 (nil, nil)。
func ReadMirrorManifest(dir string) (*MirrorManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, MirrorManifestFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m MirrorManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", MirrorManifestFilename, err)
	}
	return &m, nil
}

// WriteMirrorManifest 写入镜像元数据边车到镜像根目录。
func WriteMirrorManifest(dir string, m *MirrorManifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MirrorManifestFilename), data, 0o644)
}

// GenerateMirrorClaudeMd 生成镜像根目录的 CLAUDE.md 内容，
// 向 Claude Code 说明镜像性质（单向只读）与索引文件（llms.txt / docs_map.md）的用法。
// 内容刻意不含时间戳，保证内容未变时重复同步零 diff。
func GenerateMirrorClaudeMd(rootName, sourceURL string) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# %s（飞书文档镜像）\n\n", rootName))
	buf.WriteString("此目录是飞书文档的**只读镜像**，由 `larkdown mirror` 单向同步（仅下载）生成。\n")
	buf.WriteString("请勿手动编辑或新增文档：本地改动不会回传飞书，且下次同步时会被远端内容覆盖，远端已删除的文档会被清理（移入回收站）。\n\n")
	buf.WriteString(fmt.Sprintf("- 同步来源: <%s>\n", sourceURL))
	buf.WriteString("- 重新同步: 在本目录运行 `larkdown mirror`（来源记录在 `" + MirrorManifestFilename + "`）\n\n")
	buf.WriteString("## 索引文件（自动生成）\n\n")
	buf.WriteString("| 文件 | 用途 |\n| --- | --- |\n")
	buf.WriteString("| `llms.txt` | 全部文档的扁平列表（标题 + 相对路径），适合按标题快速定位文档 |\n")
	buf.WriteString("| `docs_map.md` | 文档地图：目录树 + 每篇文档的标题（heading）层级结构，适合了解全局结构与按章节定位 |\n")
	buf.WriteString("| `CLAUDE.md` | 本说明文件 |\n")
	buf.WriteString("| `" + MirrorManifestFilename + "` | 镜像元数据（同步来源 URL） |\n\n")
	buf.WriteString("## 查阅建议\n\n")
	buf.WriteString("1. 先看 `llms.txt` 按标题定位目标文档，再读对应 `.md` 文件\n")
	buf.WriteString("2. 需要了解知识库整体结构或按章节检索时，查 `docs_map.md`\n")
	buf.WriteString("3. 每个 `.md` 文件末尾的 HTML 注释 frontmatter 记录了 `source:` 原文档 URL；" +
		"可用 `larkdown open <file.md>` 在浏览器打开原文档\n")
	buf.WriteString("4. 图片与附件下载在文档同级的资源目录中，`.md` 内为相对路径引用\n")
	return buf.String()
}

// PruneStaleMirrorFiles 清理镜像目录中远端已不存在的文档：
// 递归遍历 root 下所有 .md 文件，凡 frontmatter 带 source 且其 token 不在 seen 中的，
// 视为远端已删除/移出，移入回收站；随后自底向上删除因此变空的目录（root 自身保留）。
// 无 frontmatter 的 .md（如 CLAUDE.md、docs_map.md、用户自有文件）一律不动。
// 返回被清理文件的路径列表。
func PruneStaleMirrorFiles(root string, seen func(token string) bool) ([]string, error) {
	var removed []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // best-effort：读不了就不动
		}
		fm, _, err := ParseFrontMatter(string(content))
		if err != nil || fm == nil || fm.Source == "" {
			return nil
		}
		token := ExtractTokenFromURL(fm.Source)
		if token == "" || seen(token) {
			return nil
		}
		if err := utils.MoveToTrash(path); err != nil {
			return err
		}
		removed = append(removed, path)
		return nil
	})
	if err != nil {
		return removed, err
	}
	return removed, removeEmptyDirs(root)
}

// removeEmptyDirs 自底向上删除 root 下的空目录（不删 root 自身）。
func removeEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 逆序（先深后浅），子目录删空后父目录才可能为空
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		if len(entries) == 0 {
			os.Remove(dir)
		}
	}
	return nil
}
