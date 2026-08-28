package core

import (
	"fmt"
	"strings"

	diff3 "github.com/epiclabs-io/diff3"
)

// MergeOutcome 是一次三方合并的结果。Conflicts 为 true 时 Body 含 git 风格冲突标记。
type MergeOutcome struct {
	Body      string
	Conflicts bool
}

// MergeMarkdown 对 markdown 正文做行级三方合并（diff3）：base 为上次同步点的
// 公共祖先，local 为本地编辑版本，remote 为远端当前渲染产物。
//
// 三份输入先各自过 NormalizeMarkdown（md→blocks→md）归一：本地手写拼写（* 号列表、
// setext 标题、围栏别名等）与渲染产物之间的格式噪音被消除——未编辑区域两侧字节级
// 相同、归一后仍相同，不会引入假冲突；代价是合并输出为归一化形态（与 download
// 覆写产物同形，围栏拼写可由调用方用 PreserveLocalFenceInfo 回填）。
// 冲突段落写入 "<<<<<<< local / ======= / >>>>>>> remote" 标记。
func MergeMarkdown(base, local, remote, mdDir string) (MergeOutcome, error) {
	nb, err := NormalizeMarkdown(base, mdDir)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("归一化 base 快照失败: %w", err)
	}
	nl, err := NormalizeMarkdown(local, mdDir)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("归一化本地文件失败: %w", err)
	}
	nr, err := NormalizeMarkdown(remote, mdDir)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("归一化远端产物失败: %w", err)
	}

	regions := diff3.Diff3Merge(splitMergeLines(nl), splitMergeLines(nb), splitMergeLines(nr), true)
	var lines []string
	conflicts := false
	for _, r := range regions {
		if r.Conflict == nil {
			lines = append(lines, r.Ok...)
			continue
		}
		conflicts = true
		lines = append(lines, conflictMarkerLocal)
		lines = append(lines, r.Conflict.A...)
		lines = append(lines, conflictMarkerSep)
		lines = append(lines, r.Conflict.B...)
		lines = append(lines, conflictMarkerRemote)
	}
	return MergeOutcome{Body: strings.Join(lines, "\n") + "\n", Conflicts: conflicts}, nil
}

func splitMergeLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
