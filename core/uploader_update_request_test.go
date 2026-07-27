package core

import (
	"encoding/json"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func testTodoBlock(id string, done bool, content string) *lark.DocxBlock {
	return &lark.DocxBlock{
		BlockID:   id,
		BlockType: lark.DocxBlockTypeTodo,
		Todo: &lark.DocxBlockText{
			Style:    &lark.DocxTextStyle{Done: done},
			Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: content}}},
		},
	}
}

// todo 取消勾选（remote done=true → local done=false）：update_text 请求必须显式携带
// "done":false——omitempty 吞掉 false 会让 fields=[2] 声明的字段缺值，飞书报 1770001 invalid param。
func TestBuildUpdateRequestTodoUncheck(t *testing.T) {
	u := &Uploader{}
	req, imgTask, fileTask := u.buildUpdateRequest(
		testTodoBlock("blk1", true, "task"),
		testTodoBlock("blk1", false, "task"),
		0, &ConvertResult{},
	)
	assert.Nil(t, imgTask)
	assert.Nil(t, fileTask)
	assert.NotNil(t, req.UpdateText)

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"done":false`)
	assert.Contains(t, string(data), `"fields":[2]`)
}

// 勾选方向（false→true）保持原有行为。
func TestBuildUpdateRequestTodoCheck(t *testing.T) {
	u := &Uploader{}
	req, _, _ := u.buildUpdateRequest(
		testTodoBlock("blk1", false, "task"),
		testTodoBlock("blk1", true, "task"),
		0, &ConvertResult{},
	)
	assert.NotNil(t, req.UpdateText)

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"done":true`)
	assert.Contains(t, string(data), `"fields":[2]`)
}

// done 未变、仅文本变：走 update_text_elements，不带 style。
func TestBuildUpdateRequestTodoTextOnly(t *testing.T) {
	u := &Uploader{}
	req, _, _ := u.buildUpdateRequest(
		testTodoBlock("blk1", true, "old"),
		testTodoBlock("blk1", true, "new"),
		0, &ConvertResult{},
	)
	assert.Nil(t, req.UpdateText)
	assert.NotNil(t, req.UpdateTextElements)
}

// code 块：update_text 携带 language 且 fields=[4]。
func TestBuildUpdateRequestCodeLanguage(t *testing.T) {
	codeBlock := func(lang lark.DocxCodeLanguage, content string) *lark.DocxBlock {
		return &lark.DocxBlock{
			BlockID:   "blk1",
			BlockType: lark.DocxBlockTypeCode,
			Code: &lark.DocxBlockText{
				Style:    &lark.DocxTextStyle{Language: lang, Wrap: true},
				Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: content}}},
			},
		}
	}
	u := &Uploader{}
	req, _, _ := u.buildUpdateRequest(
		codeBlock(lark.DocxCodeLanguagePython, "x = 1"),
		codeBlock(lark.DocxCodeLanguageGo, "x := 1"),
		0, &ConvertResult{},
	)
	assert.NotNil(t, req.UpdateText)

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"language":22`)
	assert.Contains(t, string(data), `"fields":[4]`)
}
