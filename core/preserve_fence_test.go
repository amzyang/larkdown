package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPreserveLocalFenceInfo(t *testing.T) {
	tests := []struct {
		name  string
		local string
		fresh string
		want  string
	}{
		{
			name:  "等价别名保留（jsonc↔json）",
			local: "# t\n\n```jsonc\n{\"a\": 1}\n```\n",
			fresh: "# t\n\n```json\n{\"a\": 1}\n```\n",
			want:  "# t\n\n```jsonc\n{\"a\": 1}\n```\n",
		},
		{
			name:  "非等价语言不保留",
			local: "```python\nx = 1\n```\n",
			fresh: "```go\nx = 1\n```\n",
			want:  "```go\nx = 1\n```\n",
		},
		{
			name:  "内容变化同枚举仍保留（第二趟 FIFO）",
			local: "```jsonc\n{\"a\": 1}\n```\n",
			fresh: "```json\n{\"a\": 2}\n```\n",
			want:  "```jsonc\n{\"a\": 2}\n```\n",
		},
		{
			name:  "插入新块时内容锚定不错位",
			local: "```golang\nfmt.Println(\"A\")\n```\n\n```go\nfmt.Println(\"B\")\n```\n",
			fresh: "```json\n{}\n```\n\n```go\nfmt.Println(\"A\")\n```\n\n```go\nfmt.Println(\"B\")\n```\n",
			want:  "```json\n{}\n```\n\n```golang\nfmt.Println(\"A\")\n```\n\n```go\nfmt.Println(\"B\")\n```\n",
		},
		{
			name:  "mermaid 不参与配对",
			local: "```mermaid\ngraph TD\n```\n",
			fresh: "```\nhello\n```\n",
			want:  "```\nhello\n```\n",
		},
		{
			name:  "未知别名与裸围栏等价（vue→PlainText）",
			local: "```vue\n<template></template>\n```\n",
			fresh: "```\n<template></template>\n```\n",
			want:  "```vue\n<template></template>\n```\n",
		},
		{
			name:  "本地无围栏时原样返回",
			local: "just text\n",
			fresh: "```json\n{}\n```\n",
			want:  "```json\n{}\n```\n",
		},
		{
			name:  "完整信息串（含属性）保留",
			local: "```jsonc title=\"tsconfig\"\n{}\n```\n",
			fresh: "```json\n{}\n```\n",
			want:  "```jsonc title=\"tsconfig\"\n{}\n```\n",
		},
		{
			name:  "引用块内围栏保留",
			local: "> ```jsonc\n> {\"a\": 1}\n> ```\n",
			fresh: "> ```json\n> {\"a\": 1}\n> ```\n",
			want:  "> ```jsonc\n> {\"a\": 1}\n> ```\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PreserveLocalFenceInfo(tt.local, tt.fresh))
		})
	}
}

func TestPreserveLocalFenceInfoIdempotent(t *testing.T) {
	local := "```jsonc\n{\"a\": 1}\n```\n"
	fresh := "```json\n{\"a\": 1}\n```\n"
	once := PreserveLocalFenceInfo(local, fresh)
	assert.Equal(t, once, PreserveLocalFenceInfo(local, once))
}
