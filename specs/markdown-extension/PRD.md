# round-trip 支持扩展飞书文档类型

> lark cli markdown 协议参考：@/Users/zouyang/Vcs/lark/cli/skills/lark-doc/references/lark-doc-md.md#L68-71
> XML 标签词表参考：@/Users/zouyang/Vcs/lark/cli/skills/lark-doc/references/lark-doc-xml.md

## 1. 背景与目标

飞书文档里有一批**非原生 Markdown 语法**的内容结构（@文档/@人、文字颜色、高亮框、勾选框、多维表格、画板、思维导图、电子表格、网格布局、按钮、日期提醒、行内文件、同步块等，见 lark-doc-md.md#L68-71）。larkdown 当前对这些结构的双向转换是**有损**的，无法 round-trip：

| 结构 | 下载侧（parser.go）现状 | 上传侧（md2blocks）现状 | round-trip |
|------|------------------------|------------------------|-----------|
| @人 | `@显示名`，丢失 user-id | 无法还原成 mention | ❌ |
| @文档 | `[标题](url)`，退化为普通链接 | 上传后是普通链接，非文档引用 | ❌ |
| 文字色/背景色 | **不输出** | 已能解析 `<span color>`/`<mark>` | ❌ 半成品（不对称） |
| callout | `> [!TIP]`（色→类型，丢 emoji/边框） | 能解析 callout | ⚠️ 有损 |
| grid | 拍平为顺序块，结构丢失 | — | ❌ |

本项目的总目标是：**设计一套通用的扩展语法协议**，让飞书原生内容结构能够「下载 → Markdown → 上传」无损 round-trip。

**本期（v1）只落地 `引用(@文档/@人)`**，其余结构维持现状，留待后续版本按同一协议扩展。

## 2. 协议架构（通用设计）

### 2.1 语法基线：混合方案

- **缺失/有损类型**：新增 **lark-doc-xml.md 风格的 XML 标签**（HTML 子集）承载飞书原生语义。本期即 `<cite>`。
- **已有的 Markdown 习惯表达保持不动**：`> [!TIP]`（callout）、`- [x]`（todo）、`<u>`（下划线）等不改写，兼顾可读性与现有 golden file。
- 新增标签的**语义、属性命名尽量对齐 lark-doc-xml.md**，便于跨工具理解；仅在 round-trip 必需时做最小扩展（见 §4.2）。

### 2.2 扩展点（为后续类型预留）

下载侧（`parser.go`）与上传侧（`md2blocks.go` / `md2blocks_html.go`）各建立一条**「扩展行内组件」识别/产出通路**：

- **下载**：`ParseDocxTextElement` 按元素类型分派到对应的 XML 标签产出器（本期：`mention_user` / `mention_doc` → `<cite>`）。
- **上传**：在现有行内 HTML 解析通路（`collectInlineElements` / `md2blocks_html.go`）中识别扩展标签，产出对应的 DocxTextElement（本期：`<cite>` → `MentionUser` / `MentionDoc`）。

后续类型（颜色 span、callout、grid 等）复用同一分派骨架，无需重写管线。本期交付 `<cite>` 子集 + 这套可复用的 plumbing。

### 2.3 启用方式：保真默认开启，废弃「网页粘贴」通道

- **扩展保真语法默认开启**，不提供逃生舱：旧的纯文本格式（`@名字` / `[标题](url)`）**完全废弃**，统一更新 golden file。
- **「复制粘贴回飞书 Wiki 网页」这条 round-trip 通道的设计目标正式废弃**。`<cite>` 等 XML 标签粘贴到网页会显示为字面文本——这是已知且接受的代价。**CLI `larkdown upload` 成为唯一的 round-trip 通道。**
  - 影响：`parser.go` 里专为「网页粘贴零幽灵段落」服务的 tight-list 块分隔逻辑（`isSameFamilyList` 等）失去设计前提，**后续可简化/移除**；本期不强制改动，仅记录此非目标转变（见 §10）。

## 3. v1 范围与非目标

### 3.1 范围

- **@人**（`mention_user`）round-trip。
- **@文档**（`mention_doc`）round-trip，**覆盖全部 obj_type**（doc/sheet/bitable/mindnote/file/slide/wiki/docx）。
  - 说明：@文档指向 sheet/bitable/mindnote/slide 这类「重对象」时，它只是个**带 token 的行内引用**，与「嵌入块」无关，实现成本一致，因此统一按 token round-trip。

### 3.2 非目标（v1 明确不做）

- **嵌入块本身的 round-trip**：bitable/board/sheet/mindmap 作为独立嵌入块（非 inline mention）的保真。
- **文字色/背景色的下载输出**：上传侧已支持解析，但下载侧补齐输出本期不做（仍是半成品）。
- **容器保真**：callout/grid/同步块等的完整属性/结构保真。
- **跨租户的 mention round-trip 保证**：`user-id` 用 union_id（开发商维度），同一开发商下跨 app 稳定；但跨租户/跨开发商时 @人 mention 仍可能失效，仅靠降级兜底（见 §7）。
- **评论区（`--comments`）的 @ 渲染**：评论是只读展示、不参与上传 round-trip，保持现状的 `@名字` 渲染，**不**统一成 `<cite>`。

## 4. 标签规范

统一使用 lark-doc-xml.md 的 `<cite>` 标签，靠 `type` 区分。**标签内文本为显示名/标题**（可读 + 降级兜底），上传时文本仅作展示，真实语义由属性承载。

### 4.1 @人（mention_user）

```html
<cite type="user" user-id="on_xxxxxxxx">张三</cite>
```

- `type="user"`：固定。
- `user-id`：飞书 **union_id**（开发商维度）。下载取块、上传写入、显示名解析三侧统一钉死 `user_id_type=union_id`（见 §8），换 app 下载/上传 @人更不易失效，round-trip 更健壮。
  - 实现：`core/mention.go` 的 `mentionUserIDType = lark.IDTypeUnionID` 单点定义，三侧引用。
- 标签文本：用户**显示名**，下载时经 contact API（`BatchGetUser`，`user_id_type=union_id`）解析（依赖 `contact:contact.base:readonly`）。
  - 解析失败 / 无权限时，**兜底用 user-id 本身作文本**：`<cite type="user" user-id="on_xxx">on_xxx</cite>`，保证标签结构完整、ID 不丢、round-trip 不受权限影响。

### 4.2 @文档（mention_doc）

```html
<cite type="doc" doc-id="TOKEN" obj-type="docx" href="https://x.feishu.cn/docx/TOKEN">文档标题</cite>
```

完整保留 **token + obj_type + url + title** 四项（沿用 lark 的 `type`/`doc-id`，扩展 `obj-type` 与 `href`）：

- `type="doc"`：固定。
- `doc-id`：云文档 token（上传重建 mention 的必需项）。
- `obj-type`：云文档类型字符串（见 §6.3 枚举映射）。决定上传时挂载成哪种引用。
- `href`：云文档链接，**存解码后的可读形式**（下载时 `utils.UnescapeURL`）；上传时再 url-encode。
- 标签文本：文档标题（只读属性，供可读 + 降级兜底）。

### 4.3 转义规则

- **标签本身不转义**；仅标签内文本与属性值按 XML 规则转义：`<`→`&lt;`、`>`→`&gt;`、`&`→`&amp;`；属性值中的 `"`→`&quot;`。
- `<cite>` 的产出**绕过 Markdown 转义**（标签内文本不做 `\[`、`\*` 等 Markdown 转义）。

### 4.4 出现位置

mention 可内联于飞书 API 支持的块：**Text、Heading1~9、Bullet、Ordered、Quote、Todo**。表格单元格、callout 子块等因其内部是 text 块，同样支持。

## 5. 下载侧设计（parser.go）

- `ParseDocxTextElement`（core/parser.go:444）：
  - `e.MentionUser != nil` → 产出 `<cite type="user" ...>`，替换现有 `@%s`（parser.go:449-457）。
  - `e.MentionDoc != nil` → 产出 `<cite type="doc" ...>`，替换现有 `[title](url)`（parser.go:458-461）。
- 继续复用 `userNames` map（parser.go:29 / `SetUserNames`）解析显示名；解析失败按 §4.1 兜底。
- `obj_type` int → 字符串映射见 §6.3。
- `href` 用 `utils.UnescapeURL(e.MentionDoc.URL)` 得到可读 URL。

## 6. 上传侧设计（md2blocks / uploader）

### 6.1 解析

- 在行内 HTML 解析通路识别 `<cite>` 标签（`type="user"` / `type="doc"`），产出 `MentionUser` / `MentionDoc` 文本元素，而非样式 run。
- **裸飞书链接不升级**：`[标题](https://x.feishu.cn/docx/token)` 这类裸链接保持为普通超链接，**仅显式 `<cite>` 标签**才转 mention（行为可预测，不误伤用户故意写的普通链接）。

### 6.2 写入字段映射（descendant_create / batch_update）

- **@人**：`MentionUser.UserID = user-id`。uploader 调用写入 API 时 **`user_id_type` 须设为 `union_id`**，与下载侧存储的 union_id 匹配（三侧一致，见 §8）。
- **@文档**：
  - `MentionDoc.Token = doc-id`
  - `MentionDoc.ObjType = map(obj-type)`（字符串 → int，见 §6.3）
  - `MentionDoc.URL = urlencode(href)`
  - `MentionDoc.Title = 标签文本`（只读，可传可不传）
  - `MentionDoc.FallbackType = "FallbackToLink"`（见 §7）

### 6.3 obj_type 枚举映射

| 字符串 (`obj-type`) | int (API) | lark SDK 常量 |
|---|---|---|
| `doc` | 1 | `DocxMentionObjTypeDoc` |
| `sheet` | 3 | `DocxMentionObjTypeSheet` |
| `bitable` | 8 | `DocxMentionObjTypeBitable` |
| `mindnote` | 11 | `DocxMentionObjTypeMindNote` |
| `file` | 12 | `DocxMentionObjTypeFile` |
| `slide` | 15 | `DocxMentionObjTypeSlide` |
| `wiki` | 16 | `DocxMentionObjTypeWiki` |
| `docx` | 22 | `DocxMentionObjTypeDocx` |

> `doc`(1) 为旧版文档、`docx`(22) 为新版，保持区分。

## 7. 失败与降级

两类 mention 降级路径**不同**（因写入 API 能力不同）：

### 7.1 @文档：用飞书原生 fallback（FallbackToLink）

- 上传时设 `FallbackType = "FallbackToLink"`。文档已删除 / 无权限时，飞书**原生降级为超链接**（文本=`Title`，链接=`URL`），无需我方预检 token。
- 这也是「完整保留 url+title」的价值所在：降级依赖这两项。

### 7.2 @人：捕获错误后本地降级

- `MentionUser` 写入 API **无 fallback 字段**。
- **不做上传前预检**；直接传 `UserID`。若因 user-id 无效（如跨租户 union_id 不匹配）报错，**捕获错误 → 把该 mention 降级为纯文本**（用内嵌的显示名）→ **打印 warning 并继续**，不因单个坏 mention 中断整篇上传。
- 实现：`core/uploader_mention.go` 用「失败后剥离 mention 重试一次」的策略——写入失败且 payload 含 mention_user 时，把所有 mention_user 降级为纯文本后重试；重试成功则 warning + 继续，仍失败则返回原始错误（即问题不在 mention）。无需依赖具体错误码。

## 8. 数据模型与 SDK 事实（实现参考）

来自 fork lark SDK（`~/Vcs/lark/chyroc_lark_go_sdk`）：

**读结构**（`type_docx.go`）：

```go
type DocxTextElementMentionUser struct {
    UserID string // 用户 OpenID
}
type DocxTextElementMentionDoc struct {
    Token   string             // 云文档 token
    ObjType DocxMentionObjType // 1/3/8/11/12/15/16/22
    URL     string             // 云文档链接（url_encode）
    Title   string             // 标题，只读
}
```

**写结构**（`api_docx_document_block_descendant_create.go` 等）：

- `MentionUser`：`UserID`（须匹配 query 参数 `user_id_type`；本项目三侧统一 `union_id`）+ `TextElementStyle`。**无 fallback**。
  - **三侧一致**：下载取块（`GetDocxBlockListOfDocument`）、上传写入（`descendant_create` / `batch_update`）、显示名解析（`BatchGetUser`）的 `user_id_type` 必须相同，否则 ID 不匹配导致 round-trip 失效。
  - **SDK 修复（fork）**：上游 chyroc/lark 的 `descendant_create` 请求把 `mention_user`/`mention_doc` 字段误定义为通用 `*Mention`（字段 key/id/name），与 docx 的 user_id/token/obj_type 不匹配，JSON 往返会**丢字段**。fork 已改为读结构 `*DocxTextElementMentionUser`/`*DocxTextElementMentionDoc`，并给后者补 `FallbackType` 字段。
- `MentionDoc`：`Token` + `ObjType(int)` + `URL(url_encode)` + `Title(只读)` + `FallbackType("FallbackToLink"|"FallbackToText")`。
- mention 支持的块：Text、Heading1~9、Bullet、Ordered、Quote、Todo。
- 写入 API：`descendant_create`、`batch_update`、`block_update` 均含 `mention_user` / `mention_doc` 字段（上传方向可行性已确认）。

## 9. 测试策略

- **扩展现有 golden file 模式**（`core/parser_test.go`）：新增含 @人 / @文档 的 `testdata/*.json` → `testdata/*.md` 下载用例。
- **md2blocks 单测**：md → DocxBlock，断言产出正确的 `MentionUser` / `MentionDoc` 元素（含 obj_type 映射、url-encode、FallbackType）。
- **不引入网络 / 端到端测试**（保持 CI 稳定，与现有 `TestGetWikiNodeList` 等网络测试的处理一致）。
- **迁移现有 golden file**：因下载默认输出从 `@名字`/`[标题](url)` 改为 `<cite>`，所有受影响的 `testdata/*.md` 需统一更新。

## 10. 迁移与影响

- **破坏性变更**：下载默认产物中 @人/@文档 的表现形式改变；依赖旧纯文本格式的下游需适配（已决定不留逃生舱）。
- **CLAUDE.md 待更新**：`下载侧块分隔策略（parser）` 一节描述的「网页粘贴零幽灵段落」设计目标已废弃，需相应修订；tight-list 逻辑标记为「可简化/移除」的技术债。
- **权限**：显示名解析依赖 `contact:contact.base:readonly`（已在必需权限列表中）。

## 11. 未来扩展（非本期）

按 §2.2 的扩展点，后续可逐步落地 lark-doc-xml.md 的其余标签：文字色/背景色（补下载侧 `<span text-color>`/`<mark>` 输出，闭合已半成品的颜色 round-trip）、callout 完整属性、grid 分栏、同步块、行内文件、日期提醒、按钮，以及嵌入块（bitable/board/sheet/mindmap）的 token 引用 round-trip（语义：保留只读引用 token，上传时重新挂载已存在对象，不复制不重建）。
