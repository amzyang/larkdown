# TODO

- [ ] 支持 sheet
  - [x] sheet export 导出权限, 多维表格权限
  - [x] 支持合并单元格
  - [x] markdown 最小化 diff
  - [ ] 支持使用外部文件引入
    - [ ] 使用外部文件 (eg: xlsx -> md)转 markdown 的方案，不自己使用 sheet 相关接口去拼接 markdown
- [ ] 支持 增量更新、跳过已下载文件
  - [x] image
- [ ] 支持 follow (比如文档中有引用其他文档的链接，自动下载)
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

- [x] built-in fish shell completion (v3.8.0 有 bug，有 pr)，放弃了，urfave/cli 的 fish shell completion 质量太差
- [x] sync with upstream lark sdk
- [x] diff without download
- [ ] 上传时支持 anchor link 映射为飞书文档内 outline 锚点
  - eg: `[§6. SSE 协议规范](#6-sse-协议规范)` → 飞书文档内链接到对应标题块
  - 当前行为：anchor link 被渲染为纯文本（因飞书 API 不接受 `#...` URL）
- [ ] upload --incr --dryrun 用于诊断调试
  - [ ] 需要 normalize
- [ ] mirror subcommand
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
