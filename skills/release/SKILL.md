---
name: release
argument-hint: "[版本号 vX.Y.Z | patch | minor | major]"
description: |
  larkdown 发布流程（提交 → 推送 main → 打语义化 tag → 触发并监控 GoReleaser release）。此 skill 应在以下场景使用：
  (1) 用户说"发布"、"发版"、"release"、"发 release"、"打 tag"、"出个新版本"
  (2) 需要把当前 main 的改动发布为新版本（git tag + push tag 触发 GitHub Actions）
  (3) 监控 release workflow（GoReleaser）执行状态、确认产物与 Release 链接
  (4) 询问 larkdown 的发布流程或版本号怎么定
  即使用户只说"发一下"/"tag 一下"而没说完整流程，也应使用此 skill 把整套发布走完。
---

larkdown 通过 **push 一个 `v*` tag** 触发 `.github/workflows/release.yml` 里的 GoReleaser，自动完成多平台构建、创建 GitHub Release、更新 Homebrew tap。发布的本质就是「打对 tag 并推上去」——因此重点是：发布前确保代码可用、版本号选对、tag 推送后盯住 CI 直到 Release 真正生成。

## 发布流程

按顺序执行，每步确认通过再进行下一步。

### 1. 发布前检查
- `just test` 全绿、`just build` 成功。**测试不过不发布**——tag 一旦推送就触发对外 Release 与 Homebrew 更新，事后撤回成本高。
- `git branch --show-current` 确认在 `main`；`git status --short` 看工作树；`git rev-list --count origin/main..HEAD` / `..origin/main` 确认与远端领先/落后。

### 2. 提交并推送改动（若有未提交/未推送改动）
- 未提交改动按项目 conventional commit 风格提交（`feat(scope): 中文描述`，对照 `git log` 既有风格；body 用多个 `-m` 段，避免 heredoc 以兼容 fish）。
- `git push origin main`。
- 若只是补打 tag、无新改动，跳过本步。

### 3. 选版本号（语义化 semver）
- `git tag --sort=-v:refname | head -5` 看最新 tag。
- `git log <上个tag>..HEAD --oneline` 看自上个 tag 以来的提交，据此 bump：
  - 破坏性变更 → **major**；用户可见新能力（`feat`）→ **minor**；仅 `fix`/`refactor`/`chore`/`test` → **patch**。
- 用户可能直接给 `vX.Y.Z` 或 `patch`/`minor`/`major`，据此算出版本号。

### 4. 打 tag 并推送（触发 release）
- annotated tag：`git tag -a vX.Y.Z -m "vX.Y.Z: <一句话亮点>"`
- `git push origin vX.Y.Z` —— 这一步触发 GoReleaser CI。

### 5. 监控 release CI 直到完成
- 稍等几秒，`gh run list --workflow=release.yml --limit 3` 找到本次 run（EVENT=push、对应 vX.Y.Z），取 run id。
- `gh run watch <run-id> --exit-status` 盯到结束（多平台构建约 2 分钟）；可放后台跑，完成再确认。
- 失败时 `gh run view <run-id> --log-failed` 看失败步骤（常见：goreleaser 配置、tag 格式、token 权限）。

### 6. 确认产物并报告
- `gh release view vX.Y.Z --json name,url,isDraft,assets`（或人类可读 `gh release view vX.Y.Z`）确认 Release 已生成、非 draft、产物齐全（各平台 `.tar.gz` + `checksums.txt`）。
- 向用户报告：Release 链接、版本号、产物列表、Homebrew tap 是否更新。

## 注意事项
- **发布是对外操作**：tag 推送即触发公开 Release + Homebrew 更新；务必先确认 `just test` 绿、版本号无误再推 tag。
- 配置位置：GoReleaser 在仓库根 `.goreleaser.*`，CI 在 `.github/workflows/release.yml`（`on: push tags 'v*'`）。
- CI 里 Node.js 弃用等 annotation 属基础设施提示，非发布失败，可忽略。
- tag 用 annotated（`-a`）而非 lightweight，便于 changelog 与 `git describe`。
