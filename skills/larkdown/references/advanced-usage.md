# larkdown 高级用法

## 环境配置

### 安装

```bash
brew tap amzyang/tap
brew install amzyang/tap/larkdown
# 开发版（不建议，可能有稳定性问题）：
# brew install --HEAD amzyang/tap/larkdown
```

### 首次设置

1. 在[飞书开放平台](https://open.feishu.cn/)创建应用，获取 App ID 和 App Secret
2. 在应用设置中添加重定向 URL：`http://localhost:9999/callback`（安全设置 → 重定向 URL）
3. 打开开发配置/权限管理，批量导入权限：[permissions.json](https://raw.githubusercontent.com/amzyang/larkdown/main/permissions.json)
4. 配置应用凭证：`larkdown config --appId cli_xxxxx --appSecret xxxxx`
5. OAuth 登录：`larkdown login`（浏览器会打开飞书授权页面，授权后自动保存凭证）

## 配置文件

> 配置/缓存路径的字面量沿用旧名 `feishu2md`，以零迁移兼容老用户的已有配置。

可能位于以下路径：

- `~/.config/feishu2md/config.json`（Linux / macOS XDG）
- `~/Library/Application Support/feishu2md/config.json`（macOS）

示例内容：

```json
{
  "app_id": "cli_xxxxx",
  "app_secret": "xxxxx",
  "access_token": "xxxxx",
  "refresh_token": "xxxxx",
  "token_expire_time": 1234567890
}
```

## 认证机制

Token 选择优先级：

1. `user_access_token` 有效（未过期，含 5 分钟缓冲）→ 直接使用
2. Token 过期但 `refresh_token` 存在 → 自动刷新，保存新 token
3. 刷新失败或无 token → 回退到 `tenant_access_token`（仅应用身份）

使用用户身份时，能访问用户有权限的所有文档。应用身份则需要文档显式授权给应用。

## 飞书应用权限

**以仓库根目录的 [permissions.json](https://raw.githubusercontent.com/amzyang/larkdown/main/permissions.json) 为权威清单**，在飞书开发者后台批量导入即可，无需手动逐项勾选。下表列出常用 scope 供参考：

| 权限                           | 说明                       |
| ------------------------------ | -------------------------- |
| `docx:document:readonly`       | 读取文档内容               |
| `docx:document:write_only`     | 写入文档内容（上传用）     |
| `docx:document.block:convert`  | 转换文档 block             |
| `wiki:wiki:readonly`           | 读取 Wiki 内容             |
| `wiki:node:create`             | 创建 Wiki 节点（上传用）   |
| `drive:file:readonly`          | 读取文件                   |
| `drive:drive:readonly`         | 读取云文档元数据           |
| `drive:export:readonly`        | 导出文档（电子表格下载用） |
| `docs:document.media:download` | 下载文档媒体资源           |
| `docs:document.media:upload`   | 上传文档媒体资源           |
| `docs:document.comment:read`   | 读取文档评论               |
| `contact:user.base:readonly`   | 读取用户基本信息           |
| `bitable:app:readonly`         | 读取多维表格               |
| `board:whiteboard:node:read`   | 读取白板节点               |
| `board:whiteboard:node:create` | 创建白板节点               |
| `optical_char_recognition:image` | OCR 识别图片文字（`ocr` 命令用）|
| `spark:app:write` / `spark:app:publish` | 创建/发布妙搭应用（`publish` 命令用，详见 permissions.json `user` 段） |

## 增量更新详解

`larkdown ul --incr` 的工作原理：

1. 获取远端现有 block
2. 将本地 Markdown 转换为 docx block
3. 计算远端和本地 block 的内容签名（hash）
4. 签名完全一致 → 跳过更新
5. 基于 LCS 算法计算 diff，分组为变更区域（ChangeRegion）
6. 对每个区域执行：替换、删除（从末尾开始避免索引偏移）、或插入
7. 批量请求上限为 200 个/次

图片和文件需要先上传到云文档，再更新对应 block。

## 内容限制

- 下载支持 Docx、Wiki、电子表格（Sheet）、多维表格（Bitable）
- 不支持 Slides（幻灯片）
- 单次请求最大文档大小：10MB
- 图片下载到本地 `static/` 目录，Markdown 中路径自动替换
- 外部链接图片保持原样

## 调试

使用 `--debug` 全局 flag 可启用 HTTP 请求/响应日志（输出到 stderr，JSONL 格式）：

```bash
larkdown --debug dl "https://example.feishu.cn/docx/xxx" -o /tmp/output
```

## 常见问题

### 下载失败提示权限不足

检查当前用户是否有权限访问目标文档。使用 `larkdown login` 确保以正确的用户身份登录。

### Token 过期

larkdown 会自动刷新 token。如果刷新失败，重新运行 `larkdown login`。

### 图片下载失败

确保应用具有 `docs:document.media:download` 权限。部分私有图片可能需要额外授权。

### 上传后文档格式异常

增量更新（`--incr`）可以减少格式问题。全量更新会删除所有 block 重建，可能丢失无法从 Markdown 表达的格式。
