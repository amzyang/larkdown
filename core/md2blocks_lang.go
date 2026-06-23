package core

import "github.com/chyroc/lark"

// MdStr2DocxCodeLang 将 Markdown 代码块语言字符串映射到飞书语言 ID
// 由 DocxCodeLang2MdStr 自动反转生成，附加别名支持
var MdStr2DocxCodeLang map[string]lark.DocxCodeLanguage

func init() {
	MdStr2DocxCodeLang = make(map[string]lark.DocxCodeLanguage, len(DocxCodeLang2MdStr)*2)
	for langID, mdStr := range DocxCodeLang2MdStr {
		if mdStr != "" {
			MdStr2DocxCodeLang[mdStr] = langID
		}
	}
	aliases := map[string]lark.DocxCodeLanguage{
		"sh":          lark.DocxCodeLanguageBash,
		"zsh":         lark.DocxCodeLanguageBash,
		"js":          lark.DocxCodeLanguageJavaScript,
		"ts":          lark.DocxCodeLanguageTypeScript,
		"py":          lark.DocxCodeLanguagePython,
		"rb":          lark.DocxCodeLanguageRuby,
		"rs":          lark.DocxCodeLanguageRust,
		"cs":          lark.DocxCodeLanguageCSharp,
		"c++":         lark.DocxCodeLanguageCPlusPlus,
		"yml":         lark.DocxCodeLanguageYAML,
		"objc":        lark.DocxCodeLanguageObjective,
		"objective-c": lark.DocxCodeLanguageObjective,
		"golang":      lark.DocxCodeLanguageGo,
		"plaintext":   lark.DocxCodeLanguagePlainText,
		"text":        lark.DocxCodeLanguagePlainText,
		"":            lark.DocxCodeLanguagePlainText,
	}
	for alias, langID := range aliases {
		if _, exists := MdStr2DocxCodeLang[alias]; !exists {
			MdStr2DocxCodeLang[alias] = langID
		}
	}
}

// alertTypeToCalloutColor 将 GitHub Alert type 映射回飞书 Callout 颜色
var alertTypeToCalloutColor = map[string]lark.DocxCalloutBackgroundColor{
	"CAUTION":   lark.DocxCalloutBackgroundColorLightRed,
	"WARNING":   lark.DocxCalloutBackgroundColorLightOrange,
	"TIP":       lark.DocxCalloutBackgroundColorLightGreen,
	"NOTE":      lark.DocxCalloutBackgroundColorLightBlue,
	"IMPORTANT": lark.DocxCalloutBackgroundColorLightPurple,
}

// alertTypeToBorderColor 将 GitHub Alert type 映射回飞书 Callout 边框色
var alertTypeToBorderColor = map[string]lark.DocxCalloutBorderColor{
	"CAUTION":   lark.DocxCalloutBorderColorRed,
	"WARNING":   lark.DocxCalloutBorderColorOrange,
	"TIP":       lark.DocxCalloutBorderColorGreen,
	"NOTE":      lark.DocxCalloutBorderColorBlue,
	"IMPORTANT": lark.DocxCalloutBorderColorPurple,
}
