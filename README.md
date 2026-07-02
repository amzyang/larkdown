# larkdown

飞书文档与 Markdown 双向转换工具，对 AI 友好。支持批量下载飞书文档为 Markdown，也支持上传 Markdown 到飞书 Wiki（含增量更新）。可以以 `cli` 命令行工具形式使用，也可以作为 Agent Skills 集成到 AI Agent 中。

## 如何安装

```bash
brew tap amzyang/tap
brew install amzyang/tap/larkdown
```

> 白板/画板图片留白裁剪依赖 ImageMagick，已作为强制依赖随 `brew install` 自动安装，无需手动处理。

### 安装开发版本

> **不建议**：开发中的版本可能有稳定性问题。

```bash
brew install --HEAD amzyang/tap/larkdown
# upgrade
# brew reinstall amzyang/tap/larkdown
```

## 如何配置

### 创建飞书机器人应用

配置文件需要填写 APP_ID 和 APP_SECRET 信息，请参考 [飞书官方文档](https://open.feishu.cn/document/ukTMukTMukTM/ukDNz4SO0MjL5QzM/get-) 获取。推荐设置为

- 进入飞书[开发者后台](https://open.feishu.cn/app)

- 创建企业自建应用

- 打开凭证与基础信息，获取 APP_ID 和 APP_SECRET

```bash
larkdown config --appId <APP_ID> --appSecret <APP_SECRET>
```

- 打开开发配置/安全设置，启用「设备码授权 / Device Flow」能力（登录走设备码流程，无需配置重定向 URL）

配置机器人应用权限

- 打开开发配置/权限管理，复制并批量导入权限: [permissions.json](https://raw.githubusercontent.com/amzyang/larkdown/main/permissions.json)

### 配置 diff 样式

download 命令默认在下载后显示内容变更的 diff 输出，使用 `monokai` 主题高亮。可通过配置文件修改主题：

编辑 `~/.config/feishu2md/config.json`，设置 `output.diff_style` 字段：

```json
{
  "output": {
    "diff_style": "dracula"
  }
}
```

常用主题：`monokai`（默认）、`dracula`、`github`、`solarized-dark`、`solarized-light`、`vs`、`xcode`

完整主题列表参见 [chroma styles](https://github.com/alecthomas/chroma/tree/master/styles)。使用 `--no-diff` 可完全禁用 diff 输出。

### 登录授权

```bash
larkdown auth login          # OAuth 设备码流程登录获取 user_access_token
larkdown auth status         # 查看当前认证状态（只读，不触发刷新）
larkdown auth logout         # 撤销并清除本地 user_access_token
```

`auth login` 走 OAuth 2.0 设备码流程：打印一个授权 URL 与验证码（并尽力自动打开浏览器），你在任意设备完成授权后命令自动继续。无需本地回调 server、无需重定向 URL 配置，适配 SSH / 容器等无头环境。

> 旧命令 `larkdown login` 仍作为 `larkdown auth login` 的隐藏别名保留，老脚本无需修改。

## 如何使用

### 命令行

#### 命令总览

| 命令       | 别名 | 说明                                     |
| ---------- | ---- | ---------------------------------------- |
| `config`   | -    | 读取或设置配置（AppID、AppSecret）       |
| `download` | `dl` | 下载飞书文档为 Markdown                  |
| `upload`   | `ul` | 上传 Markdown 到飞书 Wiki                |
| `publish`  | -    | 发布本地 HTML 文件/目录为飞书妙搭在线应用 |
| `auth`     | -    | 认证管理：`login` / `status` / `logout`  |
| `login`    | -    | `auth login` 的隐藏兼容别名              |
| `open`     | -    | 在浏览器中打开 Markdown 对应的飞书源文档 |
| `diff`     | -    | 对比本地 Markdown 与飞书线上版本的差异   |
| `ocr`      | -    | 识别图片中的文字（飞书 AI OCR）          |

全局选项：`--debug` 启用 HTTP 日志

#### config 命令

| 选项          | 说明            |
| ------------- | --------------- |
| `--appId`     | 飞书应用 ID     |
| `--appSecret` | 飞书应用 Secret |

#### download 命令

| 选项          | 简写 | 默认值 | 说明                                  |
| ------------- | ---- | ------ | ------------------------------------- |
| `--output`    | `-o` | `./`   | 输出目录                              |
| `--recursive` | `-r` | false  | 递归下载子节点                        |
| `--index`     | -    | false  | 生成 llms.txt 和 docs_map.md 索引文件 |
| `--comments`  | `-c` | true   | 包含文档评论                          |
| `--no-diff`   | -    | false  | 下载时不显示 diff 输出                |

> **提示**：download 命令也支持传入已下载的 `.md` 文件路径，会自动从 frontmatter 中提取源 URL 重新下载最新版本。

#### upload 命令

| 选项       | 简写 | 默认值     | 说明                                     |
| ---------- | ---- | ---------- | ---------------------------------------- |
| `--space`  | `-s` | 我的文档库 | Wiki 知识库 ID                           |
| `--parent` | `-p` | -          | 父节点 token（指定创建位置）             |
| `--full`   | -    | false      | 全量更新（删除远端所有块后重建）         |
| `--dryrun` | -    | false      | 预览增量 diff，不修改远端（与 `--full` 互斥） |

> 更新已有文档默认为**增量更新**（仅修改变化的块）；如需全量重建请使用 `--full`。

#### publish 命令

将本地 HTML 文件或整个目录发布为飞书妙搭（Miaoda）在线应用，返回可访问的 URL。需要 user_access_token，请先执行 `larkdown auth login`。

首次发布会自动新建应用，并在本地写入发布记录（记录 app_id）；之后对同一路径重新执行 `publish` 会自动复用该应用做更新，URL 保持不变。

```bash
larkdown publish ./dist                    # 发布目录（含 index.html 等静态资源）
larkdown publish ./report.html             # 发布单个 HTML 文件
larkdown publish ./dist --app-id app_xxx   # 显式复用已有应用做更新（裸 app_id）
larkdown publish ./dist --app-id https://miaoda.feishu.cn/app/app_xxx  # 也可直接粘贴妙搭应用链接
larkdown publish ./dist --new              # 强制新建应用（忽略本地发布记录）
```

| 选项       | 简写 | 默认值      | 说明                                                              |
| ---------- | ---- | ----------- | ----------------------------------------------------------------- |
| `--name`   | `-n` | 文件/目录名 | 应用展示名                                                        |
| `--app-id` | -    | 本地发布记录 | 复用已有应用做更新（接受 `app_xxx` 或妙搭应用链接）               |
| `--new`    | -    | false       | 强制新建应用（忽略本地发布记录）                                  |

> `--app-id` 与 `--new` 互斥。`--app-id` 同时接受裸 app_id 和完整链接，例如 `https://miaoda.feishu.cn/app/app_xxx` 或 `https://<sub>.aiforce.cloud/app/app_xxx`。

#### login 命令（`auth login`）

走 OAuth 2.0 设备码流程：申请设备码 → 展示授权 URL + 验证码（尽力自动打开浏览器）→ 阻塞轮询直到授权完成。

| 选项            | 默认值 | 说明                                                                 |
| --------------- | ------ | -------------------------------------------------------------------- |
| `--no-wait`     | false  | 仅申请设备码并打印后立即返回、不轮询（配合 `--device-code` 组成两段式登录） |
| `--device-code` | -      | 用前一次 `--no-wait` 得到的设备码恢复轮询、换取并保存令牌             |
| `--json`        | false  | 输出机读 JSON 事件（`device_authorization` / `authorized`）           |
| `--port`        | -      | 已废弃：no-op（设备码流程无本地回调，仅为兼容保留）                    |

**两段式登录（agent / CI / 无头环境）**：默认阻塞轮询最长约 10 分钟，不适合「只能发一次消息」的自动化场景。拆成两步——先返回 URL 交给人授权，人授权后再换令牌：

```bash
larkdown auth login --no-wait --json                     # 立即返回 device_code + 授权 URL
larkdown auth login --device-code <device_code> --json   # 用户授权后换取并保存令牌
```

#### diff 命令

对比本地 Markdown 文件与飞书线上版本的差异。自动规范化格式（表格对齐、代码围栏语言等），只显示真正的内容变更。

```bash
larkdown diff docs/文档标题.md
larkdown diff -i docs/文档标题.md  # 反转方向（remote → local）
```

| 选项       | 简写 | 默认值 | 说明                             |
| ---------- | ---- | ------ | -------------------------------- |
| `--invert` | `-i` | false  | 反转 diff 方向（remote → local） |

有差异时以 exit code 1 退出（类似 `git diff --exit-code`），可用于脚本判断。

#### open 命令

从已下载的 Markdown 文件 frontmatter 中提取 `source` URL，在浏览器中打开对应的飞书文档。支持多文件参数。

```bash
larkdown open docs/文档标题.md
larkdown open a.md b.md c.md
```

#### ocr 命令

使用飞书 AI OCR 识别图片中的文字，按区域分段输出。无参数时默认从 macOS 剪贴板读取图片（适合截图后直接 OCR）。

```bash
larkdown ocr                   # 从剪贴板读取图片（截图后直接用）
larkdown ocr screenshot.png    # 从文件读取
cat image.jpg | larkdown ocr - # 从 stdin 读取
```

### 双向转换工作流

```bash
# 下载飞书文档
larkdown dl "<wiki-url>" -o docs/

# 本地编辑 Markdown...

# 上传回飞书（默认增量更新，仅修改变化的块）
larkdown ul docs/文档标题.md

# 从已下载的 .md 重新下载最新版
larkdown dl docs/文档标题.md
```

### Agent Skills

larkdown 支持作为 Agent Skill 集成到 AI Agent（如 Claude Code）中，通过自然语言交互使用：

- 直接粘贴飞书链接
- 提到"飞书"+"下载"/"上传"/"同步"等关键词触发

#### 安装 Skill

推荐使用 [`npx skills`](https://github.com/vercel-labs/skills)（开放 Agent Skills 工具）从仓库一键安装：

```bash
# 全局安装 larkdown skill 到 Claude Code（推荐）
npx skills add amzyang/larkdown --skill larkdown -g -a claude-code

# 查看仓库内全部可安装的 skill
npx skills add amzyang/larkdown --list

# 也可直接指向 skill 子目录安装
npx skills add https://github.com/amzyang/larkdown/tree/main/skills/larkdown
```

> `npx skills` 会把 skill 安装到 `~/.agents/skills/` 并软链到 `~/.claude/skills/`（全局，带 `-g`）或项目级 `.claude/skills/`（去掉 `-g`）。仓库还包含一个 `release` skill（larkdown 发版流程），用 `--skill release` 可单独安装。

Skill 源码随仓库维护于 [`skills/larkdown/`](skills/larkdown/)（含 `SKILL.md` 与 `references/advanced-usage.md`）。也可手动将其软链或复制到 `~/.claude/skills/larkdown/`，或纳入插件 marketplace 后通过 `claude plugin install` 安装。

## 支持的文档元素

### 下载方向（飞书 → Markdown）

| 元素类型           | 支持状态    | 输出格式      | 备注                                |
| ------------------ | ----------- | ------------- | ----------------------------------- |
| 白板/画板          | ✅ 完全支持 | PNG 图片      | Wiki 文档支持缓存，依赖 ImageMagick（已随安装自动配置） |
| 普通表格           | ✅ 完全支持 | Markdown/HTML | 合并单元格自动转 HTML               |
| 电子表格 (Sheet)   | ✅ 完全支持 | Markdown/HTML | 需要 `drive:export:readonly` 权限   |
| 多维表格 (Bitable) | ✅ 完全支持 | Markdown      | 仅支持基础字段类型                  |
| 评论               | ✅ 完全支持 | Markdown      | 默认启用，`--comments` 控制         |
| 图片               | ✅ 完全支持 | 原格式        | 支持缓存                            |
| 文件附件           | ⚠️ 部分支持 | 下载或链接    | 下载失败时显示元数据                |
| Iframe 嵌入        | ⚠️ 部分支持 | 链接占位符    | Bilibili/YouTube 等                 |
| 流程图/UML         | ⚠️ 元数据   | 占位符        | 暂不支持导出图片                    |
| 小组件 (AddOns)    | ⚠️ 部分支持 | 代码块        | 仅支持 Mermaid                      |

### 上传方向（Markdown → 飞书）

| 元素类型         | 支持状态    | 备注                        |
| ---------------- | ----------- | --------------------------- |
| 段落/标题        | ✅ 完全支持 | H1-H9                       |
| 有序/无序列表    | ✅ 完全支持 | 支持多级嵌套                |
| 任务列表         | ✅ 完全支持 | `- [x]` / `- [ ]`           |
| 代码块           | ✅ 完全支持 | 保留语言标识                |
| 引用块           | ✅ 完全支持 |                             |
| 表格             | ✅ 完全支持 | 含合并单元格                |
| 图片             | ✅ 完全支持 | 本地路径和 HTTP URL         |
| 文件附件         | ✅ 完全支持 | 本地文件链接自动上传        |
| 数学公式         | ✅ 完全支持 | 行内和块级 LaTeX            |
| 分割线           | ✅ 完全支持 |                             |
| 折叠块           | ✅ 完全支持 | `<details>/<summary>`       |
| PlantUML         | ✅ 完全支持 | 转换为飞书白板              |
| HTML inline 样式 | ✅ 完全支持 | `<b>/<i>/<u>/<s>/<mark>` 等 |
| HTML block 内容  | ⚠️ 部分支持 | 解析 DOM 映射到 DocxBlocks  |

---

**注意事项**：

- **白板缓存**：Wiki 文档中的白板使用 `obj_edit_time` 作为缓存版本标识，普通 docx 文档不缓存

- **评论权限**：需要 `drive:drive.comment:read` 和 `contact:user.base:readonly` 权限

- **表格处理**：包含合并单元格的表格自动转换为 HTML 格式以保持结构

## 如何调试

遇到问题时，可以通过以下方案自助排查：

### 1. 检查配置

```bash
larkdown config
```

确认 AppID 和 AppSecret 已正确配置。

### 2. 重新配置权限和登录

版本更新可能引入新的权限要求。如果遇到权限相关错误，建议：

1. 重新导入权限配置：进入飞书开发者后台 -> 权限管理 -> 批量导入 [permissions.json](https://raw.githubusercontent.com/amzyang/larkdown/main/permissions.json)

2. 重新登录 `larkdown auth login` 获取新的授权：

```bash
larkdown auth login
```

### 3. 启用调试日志

添加 `--debug` 选项可输出 HTTP 请求/响应日志（JSONL 格式）：

```bash
larkdown dl "<your-url-here>" --debug &> debug.log
```

### 4. 反馈问题

反馈问题时请附上：

- 执行的完整命令

- 错误信息

- debug.log 文件

## FAQ

### 为什么无法下载知识库文档中的图片？

需要你本人/机器人应用对目标文档有 Edit 权限或者是文档的协作者。授权请见（通过`分享`操作）：

- [https://open.feishu.cn/document/server-docs/docs/faq](https://open.feishu.cn/document/server-docs/docs/faq)

- [https://open.feishu.cn/document/server-docs/docs/drive-v1/faq#7773e647](https://open.feishu.cn/document/server-docs/docs/drive-v1/faq#7773e647)

### 为什么有的链接无法下载？

请确认文档作者是否开启了下载权限。

## 感谢

- 原项目 [Wsine/feishu2md](https://github.com/Wsine/feishu2md)
<!--
source: https://feishu.cn/wiki/VMfkwOLYriUDZukOp3uczdBEn9b?fromScene=spaceOverview
-->
