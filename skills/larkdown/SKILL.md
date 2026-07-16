---
name: larkdown
argument-hint: "[飞书文档 URL | Markdown 文件路径]"
description: |
  飞书文档与 Markdown 双向转换工具（larkdown CLI）。此 skill 应在以下场景使用：
  (1) 用户提供 feishu.cn / <子域>.feishu.cn 的文档/Wiki/文件夹链接（docx、wiki、folder）
  (2) 提到"飞书"+"下载"/"导出"/"转 Markdown"/"上传"/"同步"/"更新"
  (3) 需要将本地 Markdown 上传到飞书 Wiki
  (4) 询问 larkdown 命令用法、安装或配置
  当用户只是粘贴了一个飞书链接而没有明确说"下载"时，也应使用此 skill。
---

# larkdown - 飞书文档双向转换

使用 `larkdown` CLI 实现飞书文档与 Markdown 的双向转换：下载飞书文档为 Markdown，或上传 Markdown 到飞书 Wiki。

## 操作流程

当用户提供飞书链接或要求操作飞书文档时：

1. **识别 URL 类型**：根据 URL 路径判断是 docx、wiki 还是 folder
2. **确定操作**：下载用 `dl`，上传用 `ul`
3. **执行命令**：URL 始终使用 `""` 包围，建议指定 `-o` 输出目录
4. **返回结果**：告知用户文件保存位置，按需读取内容

> **重要**：命令行中的 URL 参数始终使用 `""` 包围，避免特殊字符（`&`、`?`、`#` 等）导致 shell 解析错误。

## 快速参考

### 下载文档

```bash
# 单个文档
larkdown dl "https://example.feishu.cn/docx/xxx" -o /tmp/feishu-output

# Wiki 页面
larkdown dl "https://example.feishu.cn/wiki/xxx" -o /tmp/feishu-output

# 递归下载 Wiki 子树或整个空间
larkdown dl "https://example.feishu.cn/wiki/xxx" -r -o /tmp/feishu-output

# 递归下载文件夹
larkdown dl "https://example.feishu.cn/drive/folder/xxx" -r -o /tmp/feishu-output

# 重新下载已有文件（从 frontmatter 的 source 字段读取 URL）
larkdown dl /path/to/existing.md
```

### 镜像知识库（单向只下载同步）

```bash
# 首次镜像：输出目录本身即镜像根，自动生成 llms.txt / docs_map.md / CLAUDE.md
larkdown mirror "https://example.feishu.cn/wiki/xxx" -o ./my-wiki

# 重新同步：来源已记录在镜像目录的 .larkdown-mirror.yaml，无需再传 URL
cd my-wiki && larkdown mirror
```

### 上传文档

```bash
# 上传到个人知识库（默认）
larkdown ul doc.md

# 上传到指定空间和父节点
larkdown ul doc.md -s <space_id> -p <parent_token>

# 指定目标文档进行更新（默认增量更新，仅修改变化的 block）
larkdown ul doc.md --source "https://example.feishu.cn/wiki/xxx"

# 全量重建（删除远端所有 block 后重新上传）
larkdown ul doc.md --full
```

### 查找文档（拿 URL 接力下载）

```bash
# 按关键词搜索可见的云文档 + Wiki，拿到 URL 后接力 dl / mirror / ul --source
larkdown search "产品规范"

# agent 机读：--json 输出 results（含 title/url/token/doc_type）+ total/has_more/page_token
larkdown search "产品规范" --json
```

## URL 类型自动检测

larkdown 根据 URL 路径自动识别类型，不需要额外 flag：

| URL 格式                 | 类型      | 行为                                       |
| ------------------------ | --------- | ------------------------------------------ |
| `/docx/{token}`          | 单个文档  | 直接下载                                   |
| `/wiki/{token}`          | Wiki 节点 | 自动解析为文档或空间；加 `-r` 递归下载子树 |
| `/wiki/settings/{token}` | Wiki 空间 | 需要 `-r`，下载整个空间                    |
| `/drive/folder/{token}`  | 文件夹    | 需要 `-r`，递归下载所有文档                |

## 命令参考

### download / dl

下载飞书文档到本地 Markdown。

| 参数              | 说明                                                        | 默认值 |
| ----------------- | ----------------------------------------------------------- | ------ |
| `<url>...`        | 一个或多个飞书 URL 或本地 .md 文件路径（必需）              | -      |
| `-o, --output`    | 输出目录                                                    | `./`   |
| `-r, --recursive` | 递归下载子节点                                              | false  |
| `-c, --comments`  | 包含文档评论                                                | true   |
| `--no-comments`   | 排除文档评论（优先于 `--comments`）                         | false  |
| `--no-diff`       | 禁用变更 diff 输出（默认下载时会显示与本地已有文件的 diff） | false  |
| `--follow`        | 同时下载正文引用（@提及/链接）的 docx/wiki 文档到 `_refs/`  | false  |
| `--follow-depth`  | 引用追踪层数（需配合 `--follow`）                           | 1      |
| `--json`          | stdout 输出机读 JSON 汇总 `{documents:[{path,title,skipped}], files, failed}`；进度改道 stderr，隐含 `--no-diff` | false  |

当传入本地 .md 文件路径时，larkdown 会从文件 frontmatter 的 `source` 字段读取原始 URL 并重新下载，输出目录默认为该文件所在目录。

退出码：0 全部成功；1 失败；3 部分成功（有文档或图片/附件下载失败，详情见 `--json` 的 `failed` 数组）。

### mirror

把知识库空间、Wiki 子树或云文档文件夹**单向只下载同步**为本地镜像目录，适合喂给 AI Agent 或做本地知识库快照。与 `dl -r` 的区别：输出目录本身即镜像根（不嵌套知识库名子目录）、固定生成索引（llms.txt / docs_map.md）与 `CLAUDE.md` 说明、同步后清理远端已删除的本地文档（移入回收站）。

| 参数             | 说明                                                       | 默认值 |
| ---------------- | ---------------------------------------------------------- | ------ |
| `[url]`          | 知识库/文件夹 URL；省略时从镜像目录的 `.larkdown-mirror.yaml` 读取来源重同步 | -      |
| `-o, --output`   | 镜像根目录                                                 | `./`   |
| `-c, --comments` | 包含文档评论                                               | true   |
| `-f, --force`    | 强制重新下载未变化的文档                                   | false  |
| `--no-comments`  | 排除文档评论（优先于 `--comments`）                        | false  |
| `--no-prune`     | 保留远端已删除的本地文件（跳过清理）                       | false  |
| `--dry-run`      | 预览将同步/清理的内容，不写任何文件（轻量遍历远端节点列表，不判断已有文档内容是否变化） | false  |
| `--follow`       | 同时下载正文引用（@提及/链接）的镜像外 docx/wiki 文档到 `_refs/`；配置记入边车，重同步自动沿用 | false  |
| `--follow-depth` | 引用追踪层数（需配合 `--follow`）                          | 1      |

镜像目录中会生成：`llms.txt`（扁平文档列表）、`docs_map.md`（目录树 + 标题结构文档地图）、`CLAUDE.md`（面向 Agent 的镜像说明）、`.larkdown-mirror.yaml`（同步来源记录）。开启 `--follow` 时索引中另有「引用文档 (_refs)」一节，记录本地路径与原始 URL 的对应；正文链接保持原始飞书 URL 不改写。

### upload / ul

上传本地 Markdown 到飞书 Wiki。

| 参数                    | 说明                               | 默认值     |
| ----------------------- | ---------------------------------- | ---------- |
| `<file.md>`             | 本地 Markdown 文件（必需）         | -          |
| `--source`              | 目标飞书文档 URL（强制更新该文档） | -          |
| `-s, --space`           | Wiki 空间 ID                       | my_library |
| `-p, --parent`          | 父节点 token                       | -          |
| `--full`                | 全量重建（删除远端所有 block 后重新上传）  | false      |
| `--dry-run`             | 预览增量 diff，不修改远端（与 `--full` 互斥） | false      |
| `--json`                | stdout 输出机读 JSON `{file, is_new, url}`；上传进度改道 stderr（与 `--dry-run` 互斥） | false      |

`--source` 与 `--space`/`--parent` 互斥。旧拼写 `--dryrun` 已移除，请使用 `--dry-run`。

**上传行为**：

- **新建文档**：从 H1 提取标题，创建 Wiki 节点，上传内容，自动将 Wiki URL 写回文件 frontmatter 的 `source` 字段
- **增量更新**（默认）：对比远端和本地 block 的内容签名（LCS diff），仅更新变化部分
- **全量更新**（`--full`）：删除远端所有 block，重新上传

### publish

将本地 HTML 文件或整个目录发布为飞书妙搭（Miaoda）在线应用，返回可访问的 URL。需要 user_access_token，请先 `larkdown auth login`。

首次发布会自动新建应用并在本地写入发布记录；之后对同一路径重新执行 `publish` 会复用该应用做更新，URL 保持不变。

```bash
larkdown publish ./dist                    # 发布目录（含 index.html 等静态资源）
larkdown publish ./report.html             # 发布单个 HTML 文件
larkdown publish ./dist --app-id app_xxx   # 显式复用已有应用做更新（接受 app_xxx 或妙搭应用链接）
larkdown publish ./dist --new              # 强制新建应用（忽略本地发布记录）
larkdown publish ./dist --json             # agent 机读：{app_id, url, manage_url, name, is_new, scope}
```

`--app-id` 与 `--new` 互斥。`--share selected|tenant|public` 设置访问权限档位（默认新建 tenant、更新保持）。

### open

从已下载的 Markdown 文件 frontmatter 中提取 `source` URL，在浏览器中打开对应的飞书源文档。支持多文件参数。

```bash
larkdown open docs/文档标题.md
larkdown open a.md b.md c.md
```

### diff

对比本地 Markdown 文件与飞书线上版本的差异。自动规范化格式（表格对齐、代码围栏语言等），只显示真正的内容变更。退出码：0 无差异；1 有差异（类似 `git diff --exit-code`）；2 运行出错（网络/认证等）——脚本可据此区分「有变更」与「查询失败」。

```bash
larkdown diff docs/文档标题.md
larkdown diff -i docs/文档标题.md  # 反转方向（remote → local）
```

### search

按关键词搜索当前用户可见的云文档与 Wiki（一次同时搜两边），输出标题、类型、所有者、更新时间和 URL。**需要用户身份**（`larkdown auth login`），应用凭证（tenant）无法调用。

| 参数           | 说明                                                                                    | 默认值 |
| -------------- | --------------------------------------------------------------------------------------- | ------ |
| `<query>`      | 搜索关键词（必需，≤50 字符）                                                            | -      |
| `--doc-types`  | 类型过滤，逗号分隔：doc/sheet/bitable/mindnote/file/wiki/docx/folder/catalog/slides/shortcut | -      |
| `--space`      | 限定 Wiki 空间 ID（逗号分隔，与 `--folder` 互斥）                                        | -      |
| `--folder`     | 限定云文档文件夹 token（逗号分隔，与 `--space` 互斥）                                    | -      |
| `--only-title` | 仅匹配标题                                                                              | false  |
| `--sort`       | 排序：default / edit_time / edit_time_asc / open_time / create_time                     | -      |
| `--page-size`  | 每页条数（1–20）                                                                        | 20     |
| `--page-token` | 上一页返回的分页标记（翻页用）                                                          | -      |
| `--limit`      | 自动翻页聚合至多 N 条结果（1–1000；0 = 单页行为，agent 建议直接用它代替手动翻页）        | 0      |
| `--json`       | 输出机读 JSON：`{total, has_more, page_token, results:[{title,url,token,doc_type,...}]}` | false  |

```bash
larkdown search "产品规范"                       # 文本输出：[类型] 标题 — 所有者 · 更新时间 + URL
larkdown search "产品规范" --json                # agent 机读（page_token 含特殊字符，翻页时注意引号包裹）
larkdown search "产品规范" --limit 50 --json     # 自动翻页聚合 50 条，免手动 page-token 接力
larkdown search "规范" --space <space_id> --only-title --sort edit_time
```

依赖 OAuth scope `search:docs:read`；该 scope 为后加入且 refresh 不扩权，老 token 首次使用会报权限错误，重新 `larkdown auth login` 即可。

### ocr

使用飞书 AI OCR 识别图片中的文字，按区域分段输出。无参数时默认从 macOS 剪贴板读取图片（适合截图后直接 OCR）。

```bash
larkdown ocr                   # 从剪贴板读取图片（截图后直接用）
larkdown ocr screenshot.png    # 从文件读取
cat image.jpg | larkdown ocr - # 从 stdin 读取
```

### auth

认证管理命令组：`login` / `status` / `logout`。

```bash
larkdown auth login                 # OAuth 设备码流程登录获取 user_access_token
larkdown auth status                # 查看认证状态（只读，不触发刷新）
larkdown auth status --json         # agent 机读：{config_path, app_id, state, token_expire_at, user}
                                    # state: logged_in | token_expired_refreshable | login_expired | not_logged_in | not_configured
larkdown auth logout                # 撤销并清除本地 user_access_token
```

`auth login` 走 OAuth 2.0 设备码流程：打印授权 URL + 验证码（尽力自动打开浏览器），用户在任意设备完成授权后阻塞轮询直到成功并自动保存凭证——无需本地回调 server、无需重定向 URL 配置，适配无头环境。`auth status` 打印配置路径、app_id 与当前认证方式（user token 有效时展示用户身份与刷新令牌有效期）。`auth logout` best-effort 撤销远端 token 并清空本地凭证，之后需重新 login（或用 `--as bot` 显式走应用凭证）。`--port` flag 已废弃为 no-op（仅兼容老脚本）。

**两段式登录（agent / CI / 无头环境）**：设备码流程默认阻塞轮询最长约 10 分钟，不适合「只能发一次消息」的 agent。用下面两步拆开——第一步立即返回、把 URL 交给人；人授权后再跑第二步换令牌：

```bash
# 1) 申请设备码并立即返回（不轮询），把 verification_uri_complete 展示给用户去授权
larkdown auth login --no-wait --json      # 输出 {"event":"device_authorization","device_code":...,"verification_uri_complete":...}
# 2) 用户授权后，用上一步的 device_code 恢复轮询、换取并保存令牌
larkdown auth login --device-code <device_code> --json   # 成功输出 {"event":"authorized",...}
```

`--no-wait` 与 `--device-code` 互斥；`--json` 让每步输出单行 JSON 事件（`device_authorization` / `authorized`）便于程序解析。不带 `--json` 则输出人类可读文本（`--no-wait` 会附上可直接复制的 `larkdown auth login --device-code <code>` 恢复命令）。

> 旧命令 `larkdown login` 仍作为 `larkdown auth login` 的隐藏别名保留。

### config

查看或设置飞书应用凭证。

```bash
larkdown config                              # 查看当前配置
larkdown config --appId xxx --appSecret xxx  # 手动设置凭证
larkdown config init                         # 一键自动创建 PersonalAgent 个人应用（浏览器授权后凭证自动落盘）
```

`config init` 无需预置任何凭证；已有凭证时需 `--force` 覆盖（best-effort 撤销并清空旧应用登录令牌）。agent 场景优先两段式（两段是独立进程：首段带了 `--force`，第二段 `--device-code` 同样要带 `--force`）：

```bash
# 1) 发起注册，立即返回 device_code 与授权链接（单行 JSON 事件 app_registration）
larkdown config init --no-wait --json
# 2) 用户授权后，用 device_code 恢复轮询、换取并保存应用凭证（事件 app_registered）
larkdown config init --device-code <device_code> --json
```

## 环境要求

- **安装**：详见 [高级用法 - 安装](references/advanced-usage.md#安装)
- **首次使用**：先 `larkdown config init` 一键创建应用（或 `larkdown config` 手动设置 App ID/Secret），再 `larkdown auth login` 授权
- **配置文件**：`~/.config/feishu2md/config.json`（路径字面量沿用旧名以兼容老配置）

## 注意事项

- 下载支持 Docx、Wiki、电子表格（Sheet）、多维表格（Bitable）；不支持 Slides（幻灯片）
- `search` 仅支持用户身份；scope `search:docs:read` 为后加入，老 token 需重新 `larkdown auth login` 才能使用
- 所有命令默认以**用户身份**（user_access_token）调用；未登录/登录失效直接报错提示 `larkdown auth login`，**不静默降级**——应用身份（tenant_access_token）仅在显式加全局 flag `--as bot` 时使用
- 登录走 OAuth 2.0 设备码流程；access_token 过期自动刷新（v2 端点 + 跨进程锁），refresh_token 默认约 7 天，过期后需重新 `larkdown auth login`
- 调试时可加 `--debug` 全局 flag 查看 HTTP 请求日志，详见 [高级用法](references/advanced-usage.md#调试)

如需了解权限配置和常见问题，参阅 [高级用法](references/advanced-usage.md)。
更多信息参阅 [larkdown 主页](https://github.com/amzyang/larkdown)。
