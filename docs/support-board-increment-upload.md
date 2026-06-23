# board 白板支持 round-trip

> lark cli markdown 协议参考：@/Users/zouyang/Vcs/lark/cli/skills/lark-doc/references/lark-doc-md.md

参考 lark xml extension，设计上传下载的 board 协议，目前下载只保留了图片，上传就断了。

希望下载时保留图片, board token etc，上传不上传该图片，但是复用原文件已有的 board token etc

增量上传和全量上传存在一个问题，对于已经存在的引用资源，全量上传是删除之后覆盖，比如说内容中有白板，那这个白板就被删除了，等一会没法恢复。考虑如何处理该问题，有一种思路是说，删除的时候不删除完，放在暂存，或者说是有一个备份。嗯，可以大胆一点，搜索网络和 GitHub issue，看有没有什么充满想象的解决方案。
