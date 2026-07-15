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
		"sh":            lark.DocxCodeLanguageBash,
		"ash":           lark.DocxCodeLanguageBash,
		"csh":           lark.DocxCodeLanguageBash,
		"tcsh":          lark.DocxCodeLanguageBash,
		"zsh":           lark.DocxCodeLanguageBash,
		"fish":          lark.DocxCodeLanguageBash,
		"console":       lark.DocxCodeLanguageShell,
		"shell-session": lark.DocxCodeLanguageShell,
		"js":            lark.DocxCodeLanguageJavaScript,
		"jsx":           lark.DocxCodeLanguageJavaScript,
		"mjs":           lark.DocxCodeLanguageJavaScript,
		"cjs":           lark.DocxCodeLanguageJavaScript,
		"ts":            lark.DocxCodeLanguageTypeScript,
		"tsx":           lark.DocxCodeLanguageTypeScript,
		"jsonc":         lark.DocxCodeLanguageJSON,
		"json5":         lark.DocxCodeLanguageJSON,
		"jsonl":         lark.DocxCodeLanguageJSON,
		"ndjson":        lark.DocxCodeLanguageJSON,
		"mysql":         lark.DocxCodeLanguageSQL,
		"postgresql":    lark.DocxCodeLanguageSQL,
		"pgsql":         lark.DocxCodeLanguageSQL,
		"plsql":         lark.DocxCodeLanguageSQL,
		"tsql":          lark.DocxCodeLanguageSQL,
		"sqlite":        lark.DocxCodeLanguageSQL,
		"py":            lark.DocxCodeLanguagePython,
		"rb":            lark.DocxCodeLanguageRuby,
		"rs":            lark.DocxCodeLanguageRust,
		"cs":            lark.DocxCodeLanguageCSharp,
		"c#":            lark.DocxCodeLanguageCSharp,
		"c++":           lark.DocxCodeLanguageCPlusPlus,
		"cc":            lark.DocxCodeLanguageCPlusPlus,
		"hh":            lark.DocxCodeLanguageCPlusPlus,
		"h":             lark.DocxCodeLanguageC,
		"yml":           lark.DocxCodeLanguageYAML,
		"objc":          lark.DocxCodeLanguageObjective,
		"objective-c":   lark.DocxCodeLanguageObjective,
		"oc":            lark.DocxCodeLanguageObjective,
		"mm":            lark.DocxCodeLanguageObjective,
		"golang":        lark.DocxCodeLanguageGo,
		"coffee":        lark.DocxCodeLanguageCoffeeScript,
		"cson":          lark.DocxCodeLanguageCoffeeScript,
		"iced":          lark.DocxCodeLanguageCoffeeScript,
		"pas":           lark.DocxCodeLanguageDelphi,
		"pascal":        lark.DocxCodeLanguageDelphi,
		"dfm":           lark.DocxCodeLanguageDelphi,
		"dpr":           lark.DocxCodeLanguageDelphi,
		"f90":           lark.DocxCodeLanguageFortran,
		"f95":           lark.DocxCodeLanguageFortran,
		"jinja":         lark.DocxCodeLanguageDjango,
		"https":         lark.DocxCodeLanguageHTTP,
		"jsp":           lark.DocxCodeLanguageJava,
		"math":          lark.DocxCodeLanguageLateX,
		"mak":           lark.DocxCodeLanguageMakefile,
		"mk":            lark.DocxCodeLanguageMakefile,
		"pl":            lark.DocxCodeLanguagePerl,
		"pm":            lark.DocxCodeLanguagePerl,
		"ps":            lark.DocxCodeLanguagePower,
		"vbs":           lark.DocxCodeLanguageVBScript,
		"visualbasic":   lark.DocxCodeLanguageVisual,
		"ini":           lark.DocxCodeLanguageTOML,
		"patch":         lark.DocxCodeLanguageDiff,
		"sol":           lark.DocxCodeLanguageSolidity,
		"cmake.in":      lark.DocxCodeLanguageCMake,
		"proto":         lark.DocxCodeLanguageProtoBuf,
		"docker":        lark.DocxCodeLanguageDockerfile,
		"make":          lark.DocxCodeLanguageMakefile,
		"md":            lark.DocxCodeLanguageMarkdown,
		"tex":           lark.DocxCodeLanguageLateX,
		"ps1":           lark.DocxCodeLanguagePower,
		"pwsh":          lark.DocxCodeLanguagePower,
		"kt":            lark.DocxCodeLanguageKotlin,
		"kts":           lark.DocxCodeLanguageKotlin,
		"hs":            lark.DocxCodeLanguageHaskell,
		"asm":           lark.DocxCodeLanguageAssembly,
		"htm":           lark.DocxCodeLanguageHTML,
		"svg":           lark.DocxCodeLanguageXML,
		"sass":          lark.DocxCodeLanguageSCSS,
		"vb":            lark.DocxCodeLanguageVisual,
		"plain":         lark.DocxCodeLanguagePlainText,
		"plaintext":     lark.DocxCodeLanguagePlainText,
		"text":          lark.DocxCodeLanguagePlainText,
		"txt":           lark.DocxCodeLanguagePlainText,
		"":              lark.DocxCodeLanguagePlainText,
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
