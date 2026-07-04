# TODO

- [ ] 支持 sheet
  - [x] sheet export 导出权限, 多维表格权限
  - [x] 支持合并单元格
  - [x] markdown 最小化 diff
  - [ ] 支持使用外部文件引入
    - [ ] 使用外部文件 (eg: xlsx -> md)转 markdown 的方案，不自己使用 sheet 相关接口去拼接 markdown
- [x] 支持 增量更新、跳过已下载文件
  - [x] image
  - [x] 文档本体：中心化边车 `<UserCacheDir>/feishu2md/downloads/<document_id>.yaml`（`download_manifest.go`，可重建缓存故走 `CachePaths` 而非 boards/media 的 `StatePaths`）记录「输出目录 → 产物路径 + 远程版本」，版本取 Wiki `obj_edit_time.revision_id` / 普通 docx `revision_id`（见 `DownloadVersion`）。重复下载时记录与远程一致且产物仍在 → 跳过拉块与素材（仅一次轻量 GetDocxDocument）；`--force` 强制重新下载。用户文件零污染（frontmatter 仍只有 `source`）。已知限制：普通 docx 的纯白板编辑不更新 revision_id，感知不到（Wiki 不受影响，obj_edit_time 覆盖白板）
- [x] 支持 follow (比如文档中有引用其他文档的链接，自动下载)
  - download/mirror 加 `--follow` + `--follow-depth`（默认 1）：BFS 下载正文 @mention 与行内链接引用的 docx/wiki 文档到 `_refs/`，token 去重防环、树内互引不重复进 `_refs`（seen 同时记 NodeToken 与 ObjToken——飞书把 wiki @mention 解析为 ObjToken）；正文零改写（round-trip 安全）；mirror 索引加「引用文档 (_refs)」映射节、`.larkdown-mirror.yaml` 记录 follow 配置（无参重同步沿用；refs 随下载版本边车持久化，skip-unchanged 路径直接回放而非从产物反解，保证 prune 不误删 `_refs/`；旧记录缺 refs 时 --follow 重下一次补录）；follow 失败 warn 继续、不阻止 prune。已知限制：mirror 树外的同一 wiki 文档被 node-token 与 ObjToken 两种 URL 同时引用时会重复下载一次（同名覆盖，无正确性影响）
- [x] 记录/展示 diff?
- [x] 支持白板
- [x] debug mode
- [x] sentry integration
- [x] support add_ons
  - [x] [mermaid]<https://feishu.cn/wiki/HWmqwJJwhiql1DkrcKDctyyGn7e>
  - [x] beautiful mermaid
- [x] TextRun 支持嵌套样式 ParseDocxTextElementTextRun
- [x] download 的时候，document id 一样，但本地文件名不一样的情况，如何处理？（比如用户改了飞书中的标题）

## performance

### whiteboard

- [x] cache by obj_edit_time

## bisync

- [x] 反向同步到飞书
  - [x] 图片
  - [x] 表格
  - [x] mermaid
  - [x] html
    - [x] img
      - [x] img 失败展示飞书默认的失败图片
  - [x] 解决被限流问题

## docs

- [x] oauth login callback 授权
- [x] 权限导入
- [x] brew 安装/make build 安装

## ux

- [x] fish completion
- [x] markdown format `**bold**` 格式兼容性

## dev

- [x] fork lark sdk
- [x] fix tests

## upload

- [x] 支持类似上传网页那种 artifact
- [x] <https://feishu.cn/wiki/PYkQws2jlivUAkkcDwLc5uZunhg> 上传的表格样式问题，没有充分利用宽度
- [x] 支持设置 inline code 无需 linkify
      ![no-linkify](no-linkify.png)
- [x] code block 默认设置 Wrap: true（自动换行）
- [x] 优化 details/summary 折叠块的实现: <https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document-block/create>, 使用 text folded 来实现
- [x] 折叠块中的代码片段未同步下载
- [x] <https://feishu.cn/wiki/LPsVw0xCmivJ88kMgpkc0eDvnnd>: mp4(attachment) 没有缓存、下载时控制台乱码
- [x] repo cleanup
- [x] ~emoji map~
- [x] ~text formatting: alignment~
- [x] @person
- [x] 支持从已有文件中获取 source

- [x] built-in fish shell completion (v3.8.0 有 bug，有 pr)，放弃了，urfave/cli 的 fish shell completion 质量太差（后已整体迁移到 spf13/cobra，fish 补全改为动态生成，手写脚本已删除）
- [x] sync with upstream lark sdk
- [x] diff without download
- [ ] 上传时支持 anchor link 映射为飞书文档内 outline 锚点
  - eg: `[§6. SSE 协议规范](#6-sse-协议规范)` → 飞书文档内链接到对应标题块
  - 当前行为：anchor link 被渲染为纯文本（因飞书 API 不接受 `#...` URL）
- [x] upload --incr --dryrun 用于诊断调试
  - [x] 需要 normalize
    - 结论：块级 diff 本身已规范化（签名基于 `canonicalTextContent`，空白外提/链接 URL 归一，`TestRoundTripSignatures` 保证下载产物 round-trip 全 Equal），无需再过 `NormalizeMarkdown`。真正的归一缺口在报告与执行不一致：执行侧 `collectDeletions` 会跳过「空文本块删除」与「实体仍被引用（重排）」，dryrun 却照报 DELETE。已抽出 `classifyDeletion` 供两侧共用，dryrun 现输出 SKIP-DEL / PRESERVE 并在 Summary 单列，与实际执行同口径
- [x] mirror subcommand
  - 单向只下载同步：`larkdown mirror <wiki/folder-url> -o <dir>`，输出目录即镜像根；固定生成 llms.txt + docs_map.md 索引与面向 Agent 的 CLAUDE.md 说明；`.larkdown-mirror.yaml` 记录来源支持无参重同步；同步后清理远端已删除的本地文档（移入回收站，`--no-prune` 关闭；部分失败时跳过清理防误删）
- [x] bug: doesnt support image inside blockquote
- [x] callout: 内嵌其它元素
- [x] table: 内嵌其它元素
- [x] 支持其它域名（如 larkoffice.com, larksuite.com）
  - [x] URL 解析：已支持任意域名（正则不限制域名）
  - [x] `Domain()` 硬编码 `feishu.cn`：回链会自动重定向，不影响功能；cookie 可正常使用
  - [x] outline/heading 带序号 (Numbered/Bullet Listing)的在下载时候未保留 <https://feishu.cn/wiki/FwBdw3hI5iXFDykC0WNc2FC0nNc?fromScene=spaceOverview>

## download

- [x] nested todos 丢失 (<https://feishu.cn/wiki/CKDTwUVZki31TYkXBS3cWnXTncb>)
- [x] publish 同步输出 miaodao 地址 <https://miaoda.feishu.cn/app/app_4ka3u3yg6bgag>
- [x] 增量更新，针对只改部分内容的 block，是先删除，再上传新的 block，还是直接 patch block 内容？
  - 结论：已是「原地 patch」（保 block_id）。同类型 text/code/image/file 经 batch_update / replace_image / replace_file 更新；仅容器块（table/callout/quote_container）、白板、跨类型变化（如 text→heading）才删除重建。patch 为 block 级（非子串级），原生 docx v1 不支持子串 patch、也不能改块类型（cli 的 docs_ai/v1 可做子串级/跨类型替换，已验证可公开使用、可大胆采用，内容更新尚未引入，可作改进路径）。详见 CLAUDE.md「增量更新的块更新策略（uploader/diff）」。
