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

# 生成索引文件（llms.txt + docs_map.md）
larkdown dl "https://example.feishu.cn/wiki/xxx" -r --index -o /tmp/feishu-output

# 重新下载已有文件（从 frontmatter 的 source 字段读取 URL）
larkdown dl /path/to/existing.md
```

### 上传文档

```bash
# 上传到个人知识库（默认）
larkdown ul doc.md

# 上传到指定空间和父节点
larkdown ul doc.md -s <space_id> -p <parent_token>

# 增量更新（仅修改变化的 block）
larkdown ul doc.md --incr

# 指定目标文档进行更新
larkdown ul doc.md --source "https://example.feishu.cn/wiki/xxx"
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
| `<url>`           | 飞书 URL 或本地 .md 文件路径（必需）                        | -      |
| `-o, --output`    | 输出目录                                                    | `./`   |
| `-r, --recursive` | 递归下载子节点                                              | false  |
| `--index`         | 生成 llms.txt 和 docs_map.md 索引                           | false  |
| `-c, --comments`  | 包含文档评论                                                | true   |
| `--no-diff`       | 禁用变更 diff 输出（默认下载时会显示与本地已有文件的 diff） | false  |

当传入本地 .md 文件路径时，larkdown 会从文件 frontmatter 的 `source` 字段读取原始 URL 并重新下载，输出目录默认为该文件所在目录。

### upload / ul

上传本地 Markdown 到飞书 Wiki。

| 参数                    | 说明                               | 默认值     |
| ----------------------- | ---------------------------------- | ---------- |
| `<file.md>`             | 本地 Markdown 文件（必需）         | -          |
| `--source`              | 目标飞书文档 URL（强制更新该文档） | -          |
| `-s, --space`           | Wiki 空间 ID                       | my_library |
| `-p, --parent`          | 父节点 token                       | -          |
| `--incr, --incremental` | 增量更新（基于 LCS diff）          | false      |

`--source` 与 `--space`/`--parent` 互斥。

**上传行为**：

- **新建文档**：从 H1 提取标题，创建 Wiki 节点，上传内容，自动将 Wiki URL 写回文件 frontmatter 的 `source` 字段
- **全量更新**（默认）：删除远端所有 block，重新上传
- **增量更新**（`--incr`）：对比远端和本地 block 的内容签名，仅更新变化部分

### publish

将本地 HTML 文件或整个目录发布为飞书妙搭（Miaoda）在线应用，返回可访问的 URL。需要 user_access_token，请先 `larkdown login`。

首次发布会自动新建应用并在本地写入发布记录；之后对同一路径重新执行 `publish` 会复用该应用做更新，URL 保持不变。

```bash
larkdown publish ./dist                    # 发布目录（含 index.html 等静态资源）
larkdown publish ./report.html             # 发布单个 HTML 文件
larkdown publish ./dist --app-id app_xxx   # 显式复用已有应用做更新（接受 app_xxx 或妙搭应用链接）
larkdown publish ./dist --new              # 强制新建应用（忽略本地发布记录）
```

`--app-id` 与 `--new` 互斥。

### open

从已下载的 Markdown 文件 frontmatter 中提取 `source` URL，在浏览器中打开对应的飞书源文档。支持多文件参数。

```bash
larkdown open docs/文档标题.md
larkdown open a.md b.md c.md
```

### diff

对比本地 Markdown 文件与飞书线上版本的差异。自动规范化格式（表格对齐、代码围栏语言等），只显示真正的内容变更。有差异时以 exit code 1 退出（类似 `git diff --exit-code`），可用于脚本判断。

```bash
larkdown diff docs/文档标题.md
larkdown diff -i docs/文档标题.md  # 反转方向（remote → local）
```

### ocr

使用飞书 AI OCR 识别图片中的文字，按区域分段输出。无参数时默认从 macOS 剪贴板读取图片（适合截图后直接 OCR）。

```bash
larkdown ocr                   # 从剪贴板读取图片（截图后直接用）
larkdown ocr screenshot.png    # 从文件读取
cat image.jpg | larkdown ocr - # 从 stdin 读取
```

### login

OAuth 登录，获取 user_access_token。

```bash
larkdown login [--port 9999]
```

打开浏览器进行飞书授权，成功后自动保存凭证。`--port` 指定本地回调服务器端口（默认 9999）。

### config

查看或设置飞书应用凭证。

```bash
larkdown config                              # 查看当前配置
larkdown config --appId xxx --appSecret xxx  # 设置凭证
```

## 环境要求

- **安装**：详见 [高级用法 - 安装](references/advanced-usage.md#安装)
- **首次使用**：先 `larkdown config` 设置 App ID/Secret，再 `larkdown login` 授权
- **配置文件**：`~/.config/feishu2md/config.json`（路径字面量沿用旧名以兼容老配置）

## 注意事项

- 下载支持 Docx、Wiki、电子表格（Sheet）、多维表格（Bitable）；不支持 Slides（幻灯片）
- Token 过期后自动刷新；刷新失败时回退到应用身份（tenant_access_token）
- 调试时可加 `--debug` 全局 flag 查看 HTTP 请求日志，详见 [高级用法](references/advanced-usage.md#调试)

如需了解权限配置和常见问题，参阅 [高级用法](references/advanced-usage.md)。
更多信息参阅 [larkdown 主页](https://github.com/amzyang/larkdown)。
