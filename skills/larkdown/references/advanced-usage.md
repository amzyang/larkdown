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
2. 在应用设置中启用「设备码授权 / Device Flow」能力（安全设置）——登录走设备码流程，无需配置重定向 URL
3. 打开开发配置/权限管理，批量导入权限：[permissions.json](https://raw.githubusercontent.com/amzyang/larkdown/main/permissions.json)
4. 配置应用凭证：`larkdown config --appId cli_xxxxx --appSecret xxxxx`
5. OAuth 登录：`larkdown auth login`（打印授权 URL + 验证码并尽力打开浏览器，授权后自动保存凭证；旧命令 `larkdown login` 仍作隐藏别名可用）

## 配置文件

> 配置/缓存路径的字面量沿用旧名 `feishu2md`，以零迁移兼容老用户的已有配置。

可能位于以下路径：

- `~/.config/feishu2md/config.json`（Linux / macOS XDG）
- `~/Library/Application Support/feishu2md/config.json`（macOS）

示例内容（认证字段嵌套在 `feishu` 段下；另有 `output` 段控制下载排版，此处省略）：

```json
{
  "feishu": {
    "app_id": "cli_xxxxx",
    "app_secret": "xxxxx",
    "user_access_token": "u-xxxxx",
    "refresh_token": "xxxxx",
    "token_expire_time": 1751430000,
    "refresh_token_expire_time": 1752030000
  }
}
```

其中 `token_expire_time` / `refresh_token_expire_time` 为 access_token / refresh_token 的过期时刻（Unix 秒），由设备码登录与刷新自动写入。文件权限固定 0600（含 secret/token，仅属主可读写）。

## 认证机制

登录走 **OAuth 2.0 设备码流程（device flow）**：`larkdown auth login` 申请设备码、展示授权 URL + 验证码（尽力打开浏览器），用户在任意设备完成授权后轮询换取 `user_access_token` + `refresh_token`，并记录各自过期时刻（`token_expire_time` / `refresh_token_expire_time`）。无需本地回调 server、无需重定向 URL 配置。

每次命令的 token 选择优先级：

1. `user_access_token` 有效（未过期，含 5 分钟缓冲）→ 直接使用
2. access 过期但 `refresh_token` 未过期 → 自动刷新（跨进程加锁防轮换式 refresh_token 被并发打翻，走 v2 `oauth/token` 端点），保存新 token
3. `refresh_token` 也已过期（默认约 7 天）或刷新被判定确定性失效（如 `invalid_grant`）→ 清除本地 token 并提示重新 `larkdown auth login`，本次回退 `tenant_access_token`
4. 无 user token → 直接用 `tenant_access_token`（仅应用身份）

`larkdown auth status` 只读展示当前身份、access 与刷新令牌的有效期（不触发刷新）。使用用户身份能访问用户有权限的所有文档；应用身份则需文档显式授权给应用。

### 无头 / 自动化登录（agent / CI / claude code）

设备码流程默认阻塞轮询最长约 10 分钟，不适合「一次调用只能发一条消息」的 agent。用两段式拆开——第一步立即返回、把授权 URL 交给人；人授权后再跑第二步换令牌：

```bash
larkdown auth login --no-wait --json                     # 立即返回 device_code + 授权 URL，不轮询
larkdown auth login --device-code <device_code> --json   # 用户授权后换取并保存令牌
```

`--json` 让每步输出单行 JSON 事件（`device_authorization` / `authorized`）便于解析；`--no-wait` 与 `--device-code` 互斥。不带 `--json` 时 `--no-wait` 会附上可直接复制的 `larkdown auth login --device-code <code>` 恢复命令。

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
| `search:docs:read`             | 搜索云文档/Wiki（`search` 命令用，仅用户身份；后加入的 scope，老 token 需重新 `auth login`） |

## 增量更新详解

`larkdown ul` 更新已有文档时默认走增量更新（`--full` 可切换为全量重建），其工作原理：

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

### 登录失败 / 申请设备码失败

若 `larkdown auth login` 提示「申请设备码失败」，多为飞书应用未启用「设备码授权 / Device Flow」能力（开放平台 → 安全设置），或所需权限未开通/审批。启用能力、开通权限后重试。设备码流程无需配置重定向 URL。

### 下载失败提示权限不足

检查当前用户是否有权限访问目标文档。使用 `larkdown auth login` 确保以正确的用户身份登录，`larkdown auth status` 可查看当前登录身份。

### Token 过期

access_token 过期会自动刷新（无需干预）。refresh_token 默认约 7 天有效，过期后需重新 `larkdown auth login`；`larkdown auth status` 可查看刷新令牌到期时间。若提示「请重新执行 larkdown auth login」，即 refresh_token 已失效（过期 / 被撤销 / 从旧授权码流程遗留），重新登录即可。

### 图片下载失败

确保应用具有 `docs:document.media:download` 权限。部分私有图片可能需要额外授权。

### 上传后文档格式异常

默认的增量更新可以减少格式问题。全量更新（`--full`）会删除所有 block 重建，可能丢失无法从 Markdown 表达的格式。
