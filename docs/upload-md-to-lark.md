# 同步本地 md 到飞书知识库

## 初步方案思路

针对一个全新的本地 .md, 如果通过 yaml frontmatter 无法解析出 source, 此时走新建逻辑，如果可以解析出 source，则走更新逻辑。更新逻辑是完全替换飞书上文档的内容块。

* 飞书文档中不需要保留本地 md 中的 frontmatter 信息。
* 标题使用 md 中的第一级标题 `#` 作为飞书文档的标题。
* 本次只考虑上传纯 md 文件，不考虑 md 中嵌入的本地图片等资源。

## 可供参考的文档

* [Markdown/HTML 内容转换为文档块: https://open.feishu.cn/document/ukTMukTMukTM/uUDN04SN0QjL1QDN/document-docx/docx-v1/document/convert](markdown-html-to-blocks.md)
* [导入文件概述: https://open.feishu.cn/document/server-docs/docs/drive-v1/import_task/import-user-guide](import-files.md)

---

## 完善方案设计

### 需求明确

将本地 `.md` 文件同步到飞书 Wiki 知识库：
- **新建**：无 frontmatter source → 在 Wiki 中创建新节点和文档
- **更新**：有 frontmatter source → 完全替换文档内容（保持标题不变）
- 飞书文档不保留 frontmatter
- 使用 md 中的一级标题 `#` 作为文档标题（仅新建时）
- 同步后刷新本地 md 的 frontmatter
- 仅支持单文件上传，不处理本地图片

### 核心流程

```
解析本地 md (ParseFrontMatter)
          │
          ├─ 无 source ────────────────────────────────────────┐
          │                                                     │
          │  提取一级标题 → CreateWikiNode → Convert MD → CreateDocxBlock
          │                   (创建节点+文档)                     │
          │                                                     ↓
          │                                              写回 frontmatter
          │                                    (source, document_id, node_token)
          │
          └─ 有 source ────────────────────────────────────────┐
                                                                │
             解析 node_token → GetWikiNode → 获取 obj_token (document_id)
                                                                │
                                                                ↓
                    获取块列表 → BatchDelete 删除所有块 → Convert MD → CreateDocxBlock
                                                                │
                                                                ↓
                                                      更新 frontmatter (revision_id)
```

### FrontMatter 结构

```markdown
# 文档内容...

<!--
source: https://feishu.cn/wiki/xxxxxx
revision_id: 123
-->
```

### 涉及的飞书 API

| API | 用途 | SDK 支持 |
|-----|------|----------|
| `POST /docx/v1/documents/blocks/convert` | Markdown → 文档块 | ❌ 需封装 |
| `POST /wiki/v2/spaces/:space_id/nodes` | 创建 Wiki 节点 | ✅ `CreateWikiNode` |
| `GET /wiki/v2/spaces/get_node` | 获取 Wiki 节点信息 | ✅ `GetWikiNode` |
| `GET /docx/v1/documents/:id` | 获取文档信息 | ✅ `GetDocxDocument` |
| `GET /docx/v1/documents/:id/blocks` | 获取块列表 | ✅ 已封装 |
| `DELETE .../batch_delete` | 批量删除块 | ✅ `BatchDeleteDocxBlock` |
| `POST .../children` | 创建块 | ✅ `CreateDocxBlock` |

### 权限需求

```
docx:document.block:convert   # 转换文本内容为文档块
wiki:wiki:edit                # 编辑知识库（创建节点）
wiki:wiki:readonly            # 读取知识库节点信息
docx:document:edit            # 编辑文档
```

### 实现计划

#### 1. 扩展 FrontMatter 结构 (`core/docmeta.go`)

```go
type FrontMatter struct {
    Source     string `yaml:"source"`
    DocumentID string `yaml:"document_id"`
    NodeToken  string `yaml:"node_token,omitempty"`  // 新增
    SpaceID    string `yaml:"space_id,omitempty"`    // 新增
    RevisionID int64  `yaml:"revision_id,omitempty"`
    Title      string `yaml:"title,omitempty"`
}

// ParseFrontMatter 解析 md 文件的 frontmatter
func ParseFrontMatter(content string) (*FrontMatter, string, error)

// ExtractTitle 从 markdown 正文提取一级标题
func ExtractTitle(body string) string
```

#### 2. Markdown 转块 API 封装 (`core/client.go`)

```go
// ConvertMarkdownToBlocks 调用飞书 API 将 Markdown 转换为文档块
func (c *Client) ConvertMarkdownToBlocks(ctx context.Context, content string) (*ConvertBlocksResult, error)

type ConvertBlocksResult struct {
    FirstLevelBlockIDs []string
    Blocks             []*lark.DocxBlock
}
```

注意事项：
- 表格块需去除 `merge_info` 字段后才能插入
- 单次 `CreateDocxBlock` 最多 50 个块，超过需分批

#### 3. Wiki 上传逻辑 (`core/uploader.go`)

```go
type Uploader struct {
    client *Client
}

type UploadOptions struct {
    SpaceID         string // Wiki 空间 ID（可选，默认使用 my_library）
    ParentNodeToken string // 父节点 token（可选，空则为根节点）
}

// Upload 上传或更新 md 文件到 Wiki
func (u *Uploader) Upload(ctx context.Context, filePath string, opts UploadOptions) (*FrontMatter, error)
```

**新建流程**：
1. 读取文件 → ParseFrontMatter
2. ExtractTitle 获取标题
3. CreateWikiNode (obj_type=docx) → 获得 node_token, obj_token
4. ConvertMarkdownToBlocks
5. CreateDocxBlock 插入块（分批处理）
6. 返回新 FrontMatter

**更新流程**：
1. 读取文件 → ParseFrontMatter (有 source)
2. 从 FrontMatter 获取 node_token/document_id，或从 source URL 解析
3. GetWikiNode 确认节点存在，获取最新 document_id
4. 获取文档块列表
5. BatchDeleteDocxBlock 删除所有子块
6. ConvertMarkdownToBlocks
7. CreateDocxBlock 插入新块
8. 返回更新后的 FrontMatter

#### 4. CLI 命令 (`cmd/upload.go`)

```bash
# 新建到"我的文档库"（默认）
larkdown upload <file.md>

# 新建到指定 Wiki 知识库
larkdown upload <file.md> --space <space_id> [--parent <parent_node_token>]

# 更新已有文档（通过 frontmatter 中的 source 识别）
larkdown upload <file.md>

# 更新指定已有文档（--source 覆盖 frontmatter 中的 source）
larkdown upload --source <feishu_url> <file.md>
```

参数说明：
- `--source`: 目标飞书文档 URL（可选，与 `--space`/`--parent` 互斥）
- `--space`: Wiki 空间 ID（可选，默认使用"我的文档库"）
- `--parent`: 父节点 token（可选，新建时指定位置）

> **技术说明**：飞书 API 支持使用 `my_library` 作为特殊的 space_id，直接访问用户的个人文档库。每个用户有且仅有一个"我的文档库"，无需先查询 space_id。

#### 5. URL 解析工具 (`utils/url.go`)

新增函数解析 Wiki URL：
```go
// ParseWikiURL 解析 Wiki URL，提取 space_id 和 node_token
// 输入: https://xxx.feishu.cn/wiki/xxxx 或 https://xxx.feishu.cn/wiki/space/xxxx/xxxx
func ParseWikiURL(url string) (spaceID, nodeToken string, err error)
```

### 关键文件清单

| 文件 | 修改内容 |
|------|----------|
| `core/docmeta.go` | 扩展 FrontMatter，新增 ParseFrontMatter, ExtractTitle |
| `core/client.go` | 新增 ConvertMarkdownToBlocks |
| `core/uploader.go` | **新建** Wiki 上传核心逻辑 |
| `cmd/upload.go` | **新建** upload 命令 |
| `cmd/main.go` | 注册 upload 命令 |
| `utils/url.go` | 新增 ParseWikiURL |

### 验证方案

1. **单元测试**
   - `ParseFrontMatter` 解析正确性（有/无 frontmatter）
   - `ExtractTitle` 提取一级标题（有/无标题）
   - `ParseWikiURL` 解析各种 Wiki URL 格式

2. **集成测试**
   ```bash
   # 新建测试（默认上传到"我的文档库"）
   cat > /tmp/test.md << 'EOF'
   # 测试文档

   这是一段内容。

   - 列表项 1
   - 列表项 2
   EOF

   larkdown upload /tmp/test.md

   # 验证 frontmatter 已写入（文件末尾 HTML 注释格式）
   tail -5 /tmp/test.md

   # 更新测试
   cat >> /tmp/test.md << 'EOF'

   ## 新增章节

   更新后的内容。
   EOF

   larkdown upload /tmp/test.md

   # 验证 revision_id 已变化
   tail -5 /tmp/test.md
   ```

3. **边界情况**
   - 无一级标题 → 使用文件名作为标题
   - frontmatter 解析失败 → 当作新建处理
   - 块数量超 50 → 分批插入验证
   - Wiki 节点被删除 → 提示错误
