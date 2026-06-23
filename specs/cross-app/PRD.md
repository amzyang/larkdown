# 增强跨应用兼容性

## 背景与目标

larkdown 涉及 user id 的场景目前 id 类型不统一:@人 mention 已钉死 `union_id`(见
[mention-cite-roundtrip] 工作),但**评论**路径仍用默认的 `open_id`。`open_id` 是
「用户 × 应用」维度的标识,换一个 app 下载/解析就对不上;`union_id` 是「用户 × 开发商」
维度,同一开发商下的多个 app 共享,跨 app 稳定。

**目标**:确立一条全局策略——**凡是飞书 API 支持 `user_id_type` 的取值,本工具一律使用
`union_id`**。把这条策略落到当前仅剩的两处 `open_id`(评论作者、评论内 person 元素),
并为未来可能出现的写回路径预留同一原则。

## 范围

### In scope

- 抽出单一全局 user-id 类型常量(`union_id`),mention 与评论共用。
- 评论作者 / 回复作者:请求侧与姓名解析侧同步切到 `union_id`。
- 评论内容里的 `person` 元素:纳入姓名解析,渲染为 `@显示名`(当前是从不解析的原始
  `@<open_id>`)。
- 把「任何支持 `user_id_type` 的写路径一律 `union_id`」作为**文档化原则**保留。

### Out of scope(非目标)

- **不**新增 CLI 标志或 config 字段:`union_id` 硬编码,零配置(与 mention 现状一致)。
- **不**实现评论写回(仅文档化原则,不写新写回代码)。
- **不**解析多维表格 / 电子表格的人员单元格(当前是整表导出,未来若解析再按本策略 union_id 化)。
- **不**在 frontmatter 引入 owner/author 等用户字段。
- mention 路径已完成,除常量迁移外不重写其逻辑。

## 现状盘点(user-id 三面)

| 场景 | 当前 id 类型 | 是否解析姓名 | 方向 | 本 PRD 动作 |
|------|------------|------------|------|------------|
| @人 mention(docx 块内) | `union_id`(三侧已钉死) | 是 | 下载 + 上传 round-trip | 仅迁移常量归属 |
| 评论作者 / 回复作者 | `open_id`(默认,`ResolveUserNames(…, nil)`) | 是 | 仅下载 | 切 `union_id`(两侧同步) |
| 评论内 `person` 元素 | `open_id`(默认) | **否**(渲染原始 `@<id>`) | 仅下载 | 解析姓名 + 渲染 `@显示名` |

盘点确认:文档元信息 / frontmatter **无** user id 字段;评论**无写回路径**(只读渲染为
Markdown 附录)。当前 user-id 面**仅此三处**。

## 设计决策

### 1. 单一全局常量,落在 `core/identity.go`

新建 `core/identity.go` 承载用户身份策略:

- 全局常量 `userIDType = lark.IDTypeUnionID`(替代原 `core/mention.go` 的
  `mentionUserIDType`)。
- 内部 `userIDTypePtr() *lark.IDType`(替代 `mentionUserIDTypePtr()`)。
- 导出 `UserIDType() *lark.IDType`(替代 `MentionUserIDType()`),供 cmd 层调用。

`core/mention.go` 只保留 mention 的渲染 / 解析逻辑,身份类型策略移出。

**调用点同步改名**:

- `cmd/download.go`、`cmd/diff.go`:`core.MentionUserIDType()` → `core.UserIDType()`。
- `core/client.go` 4 处(`GetDocxBlockListOfDocument` ×2、`CreateDocxDocumentBlockDescendant`、
  `BatchUpdateDocxDocumentBlock`):`mentionUserIDTypePtr()` → `userIDTypePtr()`,注释由
  「@人 mention 用 union_id」改为「全局用户身份策略:统一 union_id」。

理由:身份类型策略横跨 mention 与评论两个功能,语义上不再属于 mention;单点定义保证三面
钉死同一类型。硬编码而非配置——符合零配置、避免过度配置的取向。

### 2. 评论作者:两侧同步翻转

两侧必须同时切,否则请求返回的作者 id 与解析批次 id 类型不匹配,解析失败:

- **请求侧**:`core/comment.go` 的 `GetDriveCommentList` 与 `GetDriveCommentReplyList`
  请求补 `UserIDType: UserIDType()`(当前未设 → 默认 `open_id`)。返回的作者 `UserID`
  即为 `union_id`。
- **解析侧**:`ResolveUserNames(ctx, userIDs, UserIDType())`(当前传 `nil`)。

### 3. 评论内 person 元素:两段式解析渲染(不改数据结构)

`person` 元素在 SDK 里只带 `UserID`(无姓名字段),必须经 `BatchGetUser` 取名。当前
`convertComment` / `fetchMoreReplies` 在**抓取阶段**就把 `Content` 渲染成字符串(person
已烘成 `@<id>`),早于姓名解析,导致无法填名。

改为两段式(`GetDocumentComments` 内部),**保持 `Comment` / `Reply` 结构不变**(仍存
渲染好的 `Content string`):

1. **Phase 1 — 抓取 + 收集**:分页累积原始 item(临时持有原始 content 元素),沿途收集
   全部 user id —— 作者 id + person 元素 id(复用 `commentElement.GetPersonUserID()`)。
2. **Phase 2 — 解析**:`ResolveUserNames(allIDs, UserIDType())` 一次性取回姓名 map。
3. **Phase 3 — 转换 + 渲染**:用姓名 map 把原始 item 转为 `Comment` / `Reply`,渲染
   `Content` 时 person → `@显示名`。

改动落点:

- 渲染函数 `renderElements` / `renderReplyContent` / `renderReplyContentFromList` 增加
  `userNames map[string]string` 入参(纯传递,不改 `Comment`/`Reply` 字段)。
- `renderElements` 的 `person` 分支:`fmt.Sprintf("@%s", uid)` →
  `"@" + getUserDisplayName(userNames, uid)`。解析失败回退 `@<raw union_id>`。
- `collectAllUserIDs` 扩展为同时收集 person 元素 id(或新增收集步骤)。
- `fetchMoreReplies` 在 Phase 1 返回原始 reply item,渲染推迟到 Phase 3。

### 4. person id 类型:单批 + 静默回退(文档未确认,验收实测)

> **OpenAPI 查证结论(2026-06)**:官方「分页获取文档评论 / 列出回复」文档对评论 `person`
> 元素的 `user_id` **未声明**其随 `user_id_type` 查询参数变化——字段仅注为「添加用户的
> user_id 以@用户」,无联动说明。反向证据:写入侧 create reply 的 person 示例值为
> `ou_cc19…`(open_id 前缀)。即 person **很可能恒返 open_id**,不随 `user_id_type` 联动。
> 文档不保证,以实测为准。原假设「person 随参数变」**未被证实**。

主路径(沿用既定简洁取向,仍单批):作者与 person 合并为同一个 `BatchGetUser(union_id)` 批次。

- **天然部分兜底**:`BatchGetUser` 响应**同时**以 open_id / user_id / union_id 三种键回填
  姓名 map(见 `ResolveUserNames` 现有实现)。因此**同时又是评论作者**的 person,即便其 id
  是 open_id 也能命中——该用户的 union_id 已在作者批次解析,回填的 map 里带了它的 open_id 键。
- 解析不到的 person(非作者、且 person 确为 open_id):**静默回退** `@<原始 id>`,与作者 /
  mention 解析失败回退一致,不额外打 warning。

**验收实测**(见下):用真实含 @人评论的文档跑 `--comments`,观察 person 是否解析出姓名。
若实测确认 person 恒 open_id 且回退面过大,启用**前缀分批兜底**:按 id 前缀分组
(`ou_`→`open_id`、`on_`→`union_id`)分别 `BatchGetUser`,对 person 实际类型透明、无需 a priori
知晓。本期不预先实现,留作验收后按需启用。

### 5. 预留写回:仅文档化

不抽额外 seam、不写写回代码。单一全局常量 `UserIDType()` 本身即是未来写回直接复用的入口。
在 PRD 与代码注释写明原则:**任何支持 `user_id_type` 的写路径一律 `union_id`**。

## 实现改动清单

| 文件 | 改动 |
|------|------|
| `core/identity.go`(新建) | `userIDType` 常量、`userIDTypePtr()`、导出 `UserIDType()` |
| `core/mention.go` | 移除 `mentionUserIDType` / `mentionUserIDTypePtr()` / `MentionUserIDType()`,改用 identity.go |
| `core/client.go` | 4 处 `mentionUserIDTypePtr()` → `userIDTypePtr()`,注释泛化为全局身份策略 |
| `core/comment.go` | 评论 list / reply_list 请求补 `UserIDType`;`ResolveUserNames` 传 `UserIDType()`;两段式重排;person id 收集;渲染函数加 `userNames` 入参;person → `@显示名` |
| `cmd/download.go`、`cmd/diff.go` | `core.MentionUserIDType()` → `core.UserIDType()` |

## 风险与假设

- **person id 类型**(见决策 4):OpenAPI 文档**未确认** `person.user_id` 随 `user_id_type`
  变化,且写入侧示例为 open_id,故 person **很可能恒 open_id**。靠「三键回填」天然兜底(作者型
  person 命中)+ 静默回退 + 验收实测;必要时启用前缀分批。核心风险但失败后果轻——仅个别 person
  显示为 `@<id>`,不丢内容、不崩溃。
- **权限无需变更**:`BatchGetUser` 在 `contact:contact.base:readonly` 下同时返回
  open_id / user_id / union_id,切 `union_id` 不需要新增 scope。
- **输出可见变化极小**:作者 id 仅在姓名解析失败时作为回退文本出现;正常情况下评论展示的
  是姓名,id 类型切换对常规输出几乎不可见。无缓存 / 持久化产物以 user id 为键,无失效问题。

## 测试与验收

- **不新增离线单元测试**:评论路径沿用现有网络集成测试(本地无权限会跳过,属正常)。
- mention 既有 golden 测试(已在 `union_id`)继续通过,保证常量迁移无回归。
- **验收标准**:
  1. 全部支持 `user_id_type` 的 API 请求均经 identity.go 单点设为 `union_id`(代码审查可验)。
  2. `--comments` 下评论作者正常解析为显示名。
  3. **用真实含 @人评论的文档验证**:评论内 person 元素解析为 `@显示名`(而非 `@<原始 id>`)——
     同时验证决策 4 的 person id 类型假设是否成立。
  4. mention round-trip 行为不变。
  5. `gofmt` + `make build` + `make test` 通过。

## 文档同步

- 更新记忆 [mention-cite-roundtrip]:把 `union_id` 决策从「mention 三侧」泛化为「全局用户身份
  策略,覆盖 mention + 评论」。
- `core/client.go` 4 处注释由 mention 语义改为全局身份策略表述。
- **不**改 CLAUDE.md。

[mention-cite-roundtrip]: 见项目记忆 mention-cite-roundtrip.md
