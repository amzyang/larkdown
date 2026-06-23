package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMarkdown(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // 空字符串表示 input 经 normalize 后应与自身 round-trip 一致
	}{
		{
			name:  "table padding normalization",
			input: "| 命令       | 别名 | 说明                 |\n| ---------- | ---- | -------------------- |\n| `config`   | -    | 配置                 |\n| `download` | `dl` | 下载                 |\n",
			want:  "| 命令 | 别名 | 说明 |\n| --- | --- | --- |\n| `config` | - | 配置 |\n| `download` | `dl` | 下载 |\n\n",
		},
		{
			name:  "code fence sh to bash",
			input: "```sh\necho hello\n```\n",
			want:  "```bash\necho hello\n```\n\n",
		},
		{
			name:  "idempotent after normalize",
			input: "```bash\necho hello\n```\n",
			want:  "```bash\necho hello\n```\n\n",
		},
		{
			name:  "plain text paragraph",
			input: "Hello world\n",
			want:  "Hello world\n\n",
		},
		{
			name:  "bullet list",
			input: "- item 1\n- item 2\n- item 3\n",
			want:  "- item 1\n\n- item 2\n\n- item 3\n",
		},
		{
			name:  "ordered list",
			input: "1. first\n2. second\n3. third\n",
			want:  "1. first\n\n2. second\n\n3. third\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeMarkdown(tt.input)
			require.NoError(t, err)
			got = strings.TrimRight(got, "\n") + "\n"
			want := strings.TrimRight(tt.want, "\n") + "\n"
			assert.Equal(t, want, got)
		})
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	// normalize 两次应产生相同结果
	input := "| A       | B    |\n| ------- | ---- |\n| hello   | world|\n\n```sh\ncode\n```\n\n- item 1\n- item 2\n"
	first, err := NormalizeMarkdown(input)
	require.NoError(t, err)
	second, err := NormalizeMarkdown(first)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}
