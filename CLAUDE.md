# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

larkdown 是一个飞书文档与 Markdown 双向转换的 Go CLI 工具。支持下载飞书文档为 Markdown，也支持上传 Markdown 回飞书 Wiki。

> 命名说明：项目原名 `feishu2md`，现已重命名为 **larkdown**（Lark + Markdown）。Go module、二进制、CLI 命令名均为 `larkdown`；但**用户数据路径的字面量刻意保留 `feishu2md`**（`~/.config/feishu2md/`、`~/.cache/feishu2md/`、发布边车文件 `.feishu2md-publish.yaml`），以零迁移兼容老用户的已有配置/缓存/发布记录——改这些字面量会让老数据失联。

## 常用命令

```bash
# 构建
make build          # 构建 CLI 工具 (larkdown)

# 测试
make test           # 运行所有测试 (go test ./...)
go test -v ./core   # 运行特定包的测试
go test -v -run TestParseDocxContent/testdocx.1 ./core  # 运行单个子测试

# 代码格式化
make format         # gofmt -l -w .

# 清理
make clean          # 删除构建产物
```

## 架构

### 数据流

**下载**：飞书 API → `client.go` 获取 DocxBlock JSON → `parser.go` 转换为 Markdown → 写入文件
**上传**：Markdown 文件 → `md2blocks.go` (goldmark AST) 转换为 DocxBlock → `uploader.go` 调用飞书 API 创建/更新文档
**增量更新**：`diff.go` 基于块签名（SHA-256）做 LCS diff，仅修改变化的块

### 下载侧块分隔策略（parser）

`ParseDocxBlockPage` 在拼接相邻顶层块时按上下文选择分隔：

- **同族 list 块**（同 `BlockType` 的 bullet/ordered/todo 相邻）→ 仅块自身末尾 `\n`，输出 tight list
- **其他相邻块**（段落/标题/混合 list 等）→ 追加 `\n` 形成 `\n\n` 段落分隔
- **真实空 text block** 仍保留为 markdown 中的额外空行
- 文档结尾强制以单个 `\n` 收尾（POSIX）

设计目的：让下载产物**复制粘贴回飞书 Wiki 网页**时不产生幽灵空段落。飞书网页粘贴会把 list 之间的空行解析为额外空段落 block；段落之间的单个空行才是自然分隔，不会产生空块。

注意：CLI `larkdown upload` 路径不依赖此排版规则（goldmark 把 tight/loose list 等价处理）；此策略仅服务于"网页粘贴"这条 round-trip 通道。

### 增量更新的块更新策略（uploader/diff）

`incrementalUpdate`（`uploader.go`）对变化的块按「能否原地更新」**三档分流**，**优先保留 block_id**（不破坏协作光标 / 评论锚点）：`ComputeDiff`(LCS) → `PairBlocks` 把同一变更区域的 delete+insert 配对（`diff.go`）→ 按档执行。

**第一档 · 原地 patch（保 block_id，`canReplace`=true → `PairedOpReplace`）：** 飞书 `batch_update` PATCH（`buildUpdateRequest` → `BatchUpdateDocxBlocks`）

- text/heading/bullet/ordered/quote → `update_text_elements`
- code → `update_text`（含语言, Fields=[4]）
- todo → `update_text`（含完成态 Fields=[2]）/ `update_text_elements`
- image/file → 先传素材，再 `replace_image` / `replace_file`

**第二档 · docs_ai block_replace（跨类型 text-like，`canDocsAIReplace`=true → `PairedOpDocsAIReplace`）：** `client.BlockReplace`（docs_ai/v1 `block_replace`，markdown content）

- 跨类型 text-like 互转（text↔heading↔bullet↔ordered↔quote↔todo）原地整块替换，一次 PUT；优于删除重建之处是原子、保留位置
- block_id 可能变（docs_ai 不保证），但这些块本就走删除重建、block_id 也会变；content 由 `localBlockMarkdownContent` 复用 parser 行内渲染生成
- 需 user_access_token（`HasUserToken`）；无则该档禁用、回退第三档（`executeDeleteInsert` 必须用相同 `docsAIEnabled` 调 `PairBlocks`，否则重复删插）

**第三档 · 删除 + 重建（前两档都不满足）：**

- 容器块 table / callout / quote_container（descendant 块）→ 整容器重建，子块 id 全变
- 白板 board（`batch_update` 无 ReplaceBoard、board/v1 无删改节点 API）
- AddOns 插件块（如 mermaid，docs_ai XML 无法表达）
- 跨「容器↔非容器」、跨「text-like↔非 text-like」等其余类型变化

**PlantUML 画板免重建（`board_manifest.go`）：** 本地 ` ```plantuml ` 无 token，签名 `board:plantuml:<源hash>` 与远程 `board:<token>` 命名空间隔离、永不匹配，会每次重建。中心化边车 `<UserConfigDir>/feishu2md/boards/<document_id>.yaml`（`StatePaths`，按文档归一、不随工作目录移动）持久化「源 hash → board token」映射：上传前 `applyBoardTokenMappings` 按源 hash 查映射写回 `Board.Token`（且校验 token 仍在远程），命中后签名与远程 Equal → 跳过重建；新建画板后 `persistBoardMappings` 刷新映射。源改了则 hash 变、查不到 → 仍重建（board API 无法原地改画板内容，硬限制）。

**图片/文件免重传（`media_manifest.go`）：** 手写文件名的本地图片/文件（非 `<token>_原名` 下载产物）无 token，签名 `media:new:<idx>` 与远程 `media:<token>` 命名空间隔离、永不匹配，会每次重传素材（飞书素材不幂等、token 抖动、`block_id` 被删除重建）。中心化边车 `<UserConfigDir>/feishu2md/media/<document_id>.yaml` 持久化「markdown 路径 → {token, md5}」映射：上传前 `applyMediaTokenMappings` 按路径查映射写回 `Image/File.Token`（校验 token 仍在远程），命中后签名与远程 Equal → 进第一档「Equal 内容检测」用边车基准 md5（`mediaChanged`，不依赖下载缓存）判定——内容未变跳过、已变走 `replace_image`/`replace_file` 原地替换**保 block_id**；各上传点 `recordMediaMapping` 累积、`persistMediaMappings` 刷新。路径即身份锚点：改图（路径不变内容变）→ md5 不符 → 原地替换保 id；重命名（路径变）→ 失配 → 当新图重传。与画板互补（画板按源 hash、媒体按路径），同解「本地无 token、签名隔离」；视频即 File 附件，一并覆盖。`--dryrun` 的「媒体内容替换」段会列出将被 `replace` 的媒体（内容真变才出现），用于核验增量是否真识别了改动。

**改进路径**：`docs_ai/v1` 的 `block_replace` 已用于第二档跨类型 text-like 替换；`block_move_after` 用于实体位置校正（`BlockMoveAfter` / `reconcileEntityPositions`）。**容器块（table/callout/quote_container）走 docs_ai 尚未落地**——docs_ai markdown 模式下 callout 须用 XML（markdown 会被当普通引用块）、table 的合并/列宽/背景色无法用 markdown 表达，需先引入 `DescendantGroup→docs_ai XML` 序列化器才能开放（届时跨类型/容器块可统一走 `block_replace`）。`str_replace`（子串级、保 block_id）对同类型文本块无增量价值（已由 batch_update 覆盖）。

### 目录结构

```
cmd/           # CLI 入口 (urfave/cli v3)
  main.go      # 命令注册（config, download, login, upload）
  download.go  # download/dl 子命令：单文档、批量文件夹、Wiki 递归下载
  upload.go    # upload 子命令：Markdown 上传/增量更新到飞书 Wiki（--source 指定目标文档）

core/          # 核心业务逻辑
  client.go    # 飞书 API 客户端封装（lark SDK + 限流 4 req/s + 60s 超时）
  parser.go    # DocxBlock → Markdown 转换器（支持 80+ 语言代码块）
  md2blocks.go # Markdown → DocxBlock 反向转换（goldmark 解析 AST）
  diff.go      # 块级 diff 算法（LCS + SHA-256 签名），用于增量更新
  uploader.go  # Wiki 上传器：新建文档或增量更新（基于 frontmatter 判断）
  docmeta.go   # 文档元信息、目录索引（llms.txt / docs_map.md）、FrontMatter 解析
  cache.go     # 图片缓存（~/.cache/feishu2md/images/）
  config.go    # 配置管理（~/.config/feishu2md/config.json）
  oauth.go     # OAuth 设备码流程（device flow）：申请设备码、轮询换 token、v2 刷新、token 撤销（纯裸 HTTP）
  comment.go   # 文档评论获取与 Markdown 渲染（--comments 选项）

utils/         # URL 校验与 token 提取、文件名清理

skills/        # Agent Skill 定义（larkdown skill：SKILL.md + references/）

testdata/      # 测试数据：JSON (DocxBlock) + MD (期望输出) golden file 对比
```

### 测试模式

`core/parser_test.go` 使用 golden file 模式：从 `testdata/*.json` 加载 DocxBlock，解析后与 `testdata/*.md` 做 `assert.Equal` 对比。修改 parser 逻辑后需更新对应 `.md` 文件。

## 核心依赖

- `github.com/chyroc/lark` - 飞书 API SDK（使用 `github.com/amzyang/lark` fork，通过 `go.mod` replace 指令引用）
  - Fork 源代码本地检出于 `<lark-fork>`（用下方 GitHub 地址 clone 即可），可直接查看实现
  - Fork GitHub: <https://github.com/amzyang/lark.git>
  - 上游 GitHub: <https://github.com/chyroc/lark.git> （复杂问题可参考 issues, discussions, pull requests）
- `github.com/yuin/goldmark` - Markdown 解析（用于 md2blocks 反向转换）
- `github.com/urfave/cli/v3` - CLI 框架

### 同步 Fork lark SDK

修改 fork 后更新 larkdown 依赖的流程：

> 下文 `<lark-fork>` 指 amzyang/lark fork 的本地检出，`<larkdown>` 指本仓库根目录。

```bash
# 1. 在 fork 仓库提交并推送
cd <lark-fork>
git add <changed-files>
git commit -m "fix(docx): <description>"
git push origin master

# 2. 在 larkdown 中更新 replace 指令
cd <larkdown>
go mod edit -replace github.com/chyroc/lark=github.com/amzyang/lark@<new-pseudo-version>
go mod tidy

# 3. 验证
make build && make test
```

其中 `<new-pseudo-version>` 格式为 `v0.0.0-<timestamp>-<commit-hash-12>`，可通过 `go list -m github.com/amzyang/lark@<commit>` 获取，或直接用 commit hash：

```bash
go mod edit -replace "github.com/chyroc/lark=github.com/amzyang/lark@$(cd <lark-fork> && git rev-parse HEAD)"
go mod tidy
```

同步上游变更到 fork：

```bash
cd <lark-fork>
git remote add upstream https://github.com/chyroc/lark.git  # 仅首次
git fetch upstream
git merge upstream/master
# 解决冲突后 push，再按上述流程更新 larkdown
```

## 开发注意事项

### 认证方式

支持两种认证方式：

| 方式                | 说明                               | 使用场景                 |
| ------------------- | ---------------------------------- | ------------------------ |
| tenant_access_token | 应用级认证，使用 AppID + AppSecret | 默认方式，适合批量自动化 |
| user_access_token   | 用户级认证，通过 OAuth 设备码流程登录获取 | 访问用户个人有权限的文档 |

**使用流程**：

```bash
# 1. 配置应用凭证
larkdown config --appId <id> --appSecret <secret>

# 2. (可选) OAuth 设备码流程登录获取 user_access_token（旧命令 larkdown login 仍作隐藏别名可用）
#    打印 verification URL + user code（并尽力自动打开浏览器），授权后阻塞轮询直到完成；无需本地回调 server
larkdown auth login
#    两段式（agent/CI/无头友好）：先 --no-wait 立即返回 device_code+URL，人授权后再 --device-code 换令牌；--json 输出机读事件
#    larkdown auth login --no-wait --json  然后  larkdown auth login --device-code <code> --json

# 3. 下载文档（优先使用 user_access_token，过期自动刷新，失败降级到应用凭证）
larkdown download <url>

# 诊断当前认证状态（只读，不触发刷新）
larkdown auth status

# 撤销并清除本地 user_access_token，回落应用凭证
larkdown auth logout
```

### OAuth 配置要求

`larkdown auth login` 走 OAuth 2.0 设备码流程（device flow），**无需配置重定向 URL、无需本地回调 server**（原 `http://localhost:9999/callback` 不再需要）：

- 进入应用 -> 安全设置，确认已**启用「设备码授权 / Device Flow」能力**（否则申请设备码会直接报错）
- 确认「必需的飞书权限」已开通/审批（设备码申请携带的 scope 需与之匹配）
- 兼容性：隐藏别名 `larkdown login` 与已废弃的 `--port` flag 仍保留（`--port` 为 no-op，不打断老脚本）

### 必需的飞书权限

- `docx:document:readonly` - 读取文档
- `docs:document.media:download` - 下载图片
- `drive:file:readonly` - 列出文件夹内容
- `wiki:wiki:readonly` - 读取知识库
- `bitable:app:readonly` - 读取多维表格
- `drive:export:readonly` - 导出电子表格
- `board:whiteboard:node:read` - 读取白板节点（下载白板图片需要）
- `drive:drive.comment:read` - 读取文档评论（--comments 选项需要）
- `contact:contact.base:readonly` - 获取用户基本信息（评论显示真实姓名需要）

### E2E 测试

本机已配置好飞书应用凭证、OAuth 授权与上文「必需的飞书权限」，可直接用真实二进制对真实飞书文档跑端到端验证，**无需 mock、无需额外配置**：

```bash
make build                                                   # 产出仓库根目录的 ./larkdown
./larkdown download <url>                                    # 下载真实文档，验证解析/排版
./larkdown upload <file.md> --source <url>                   # 上传到指定文档，验证反向转换
./larkdown upload <file.md> --source <url> --dryrun          # 预览增量 diff，不落库
./larkdown upload <file.md> --source <url> --full            # 全量重建（删除远端所有块后重新上传）
```

- 测试资源放在 `testdata/`：可复用已有 fixture（`roundtrip.*.json`、`upload.*.md` 等），也可按需在 `testdata/` 下新建临时文档/资源做 round-trip 验证。
- E2E 打的是真实 API，受 `core/client.go` 限流（4 req/s）约束；`--dryrun` 可在不修改远端文档的前提下预览增量行为（更新已有文档默认即增量，`--full` 切换为全量重建）。
- 验证「上传/编辑后页面的实际渲染效果」可用 `/chrome-devtools-mcp:chrome-devtools` skill（或 chrome devtools mcp）打开飞书文档 URL，直接检查真实页面（排版、块结构、图片/白板等），而非仅看本地 Markdown 或 API 返回。
- 需要脚本化或复杂浏览器操作（多步交互、批量校验、自动化 round-trip）时，用 `agent-browser` CLI（`agent-browser skills get core --full` 查用法）写脚本驱动飞书页面。

### CI 检查

- gofmt 格式检查
- 全量测试
- CLI 构建

### 下载跳过（未变化文档）

重复下载时按中心化边车 `~/.cache/feishu2md/downloads/<document_id>.yaml`（`download_manifest.go`）记录的「输出目录 → 产物路径 + 远程版本」跳过未变化文档，仅需一次轻量 `GetDocxDocument`。版本标记（`DownloadVersion`）：Wiki 用 `obj_edit_time.revision_id`（revision_id 覆盖内容编辑与评论、obj_edit_time 覆盖白板编辑），普通 docx 仅 `revision_id`（纯白板编辑感知不到）。`download --force` 强制重新下载；素材下载不完整时不落记录，下次自动重试。属可重建缓存（走 `CachePaths`，与 boards/media 的 `StatePaths` 相对），删除仅导致下次重新下载。

### 白板缓存

Wiki 文档中的白板图片支持本地缓存（`~/.cache/feishu2md/whiteboards/`）。

**重要**：编辑白板不会更新文档的 `revision_id`，但会更新 Wiki 节点的 `obj_edit_time`。因此白板缓存使用 `obj_edit_time` 作为版本标识，而非 `revision_id`。

普通 docx 文档（非 Wiki）不缓存白板，每次重新下载。

### 并发处理

- 批量文件夹下载：无限制并发
- Wiki 下载：信号量限制最多 10 个并发（`core/client.go` 限流 4 req/s）

### 已弃用

- 旧版文档 (Docs) 格式不再支持，仅支持新版 DocX 格式
