package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 飞书 mention_doc.URL 以整串 url_encode（%20 表示空格）存储。
// 「下载 UnescapeURL → 上传 EscapeURL」链路对飞书编码应字节级稳定。
func TestEscapeUnescapeURLRoundTrip(t *testing.T) {
	feishuEncoded := []string{
		"https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTokDocx001",
		"https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTOK%3Ffrom%3Dcopy_link%23heading123", // query + fragment
		"https%3A%2F%2Fx.feishu.cn%2Fwiki%2FABC%3Ftable%3Dtbl1%26view%3DvewXYZ",  // 多个 query 参数
		"https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTOK%20space",                         // %20 空格
		"https%3A%2F%2Fx.feishu.cn%2Fdocx%2F%E4%B8%AD%E6%96%87",                  // 中文 UTF-8
	}
	for _, enc := range feishuEncoded {
		decoded := UnescapeURL(enc)
		assert.Equal(t, enc, EscapeURL(decoded), "round-trip 应字节稳定: %s", enc)
	}
}

// 空格用 %20 而非 '+'，字面 '+' 编码为 %2B，二者不混淆。
func TestEscapeURLSpaceUsesPercent20(t *testing.T) {
	assert.Equal(t, "a%20b", EscapeURL("a b"))
	assert.Equal(t, "a%2Bb", EscapeURL("a+b"))
	assert.Equal(t, "a b", UnescapeURL("a%20b"))
}

// 整串 percent-encode，与飞书官方示例 https%3A%2F%2F... 同构。
func TestEscapeURLStyleMatchesFeishu(t *testing.T) {
	assert.Equal(t, "https%3A%2F%2Fx.feishu.cn%2Fdocx%2FT1",
		EscapeURL("https://x.feishu.cn/docx/T1"))
}
