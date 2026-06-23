#!/usr/bin/env bash
#
# e2e_board.sh — 白板 board round-trip 端到端测试（打真实飞书 API）
#
# 前置：
#   1. larkdown config --appId <id> --appSecret <secret>
#   2. larkdown login            # 获取 user_access_token（白板下载 / docs_ai move 需要）
#   3. 一个【含白板、且你有写权限】的飞书文档 URL
#
# 用法：
#   scripts/e2e_board.sh <doc-url>            # 只读校验（下载 + --incr --dryrun，不写回）
#   scripts/e2e_board.sh <doc-url> --write    # 完整 e2e（含真实上传写回 + 复查 token 保留）
#
# 退出码：0 全部通过；非 0 表示某步失败。
set -euo pipefail

URL="${1:-}"
MODE="${2:-readonly}"
if [[ -z "$URL" ]]; then
  echo "用法: $0 <doc-url> [--write]" >&2
  exit 2
fi

BIN="./larkdown"
[[ -x "$BIN" ]] || BIN="larkdown"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
hr()   { echo "── $1 ──"; }

# 取第一个 <whiteboard token="..."> 的 token
extract_token() { grep -oE '<whiteboard token="[^"]+"' "$1" | head -1 | sed -E 's/.*token="([^"]+)".*/\1/'; }

hr "1. 下载（download）"
"$BIN" download "$URL" -o "$WORK/dl1" --comments=false >/tmp/e2e_dl1.log 2>&1 || { cat /tmp/e2e_dl1.log; bad "下载失败"; exit 1; }
MD1="$(find "$WORK/dl1" -name '*.md' | head -1)"
[[ -n "$MD1" ]] && ok "下载产出 $MD1" || { bad "未找到下载的 .md"; exit 1; }

hr "2. 校验下载产物含 <whiteboard token=...>"
if grep -q '<whiteboard token="' "$MD1"; then
  TOKEN1="$(extract_token "$MD1")"
  ok "含白板标记，token=$TOKEN1"
else
  bad "下载产物不含 <whiteboard> 标记——该文档可能没有白板，请换一个含白板的文档"
  echo "    （白板相关行：）"; grep -n -i 'whiteboard\|画板' "$MD1" || true
  exit 1
fi

hr "3. 校验白板缩略图被引用、且为下载产物（非上传源）"
if grep -q '<img src="[^"]*whiteboard_' "$MD1"; then ok "内层 <img> 缩略图存在"; else echo "  ℹ️ 无内层 <img>（可能图片下载失败，token 仍可 round-trip）"; fi

hr "4. 增量 dry-run（--incr --dryrun，只读，不写回）"
"$BIN" upload --source "$URL" --incr --dryrun "$MD1" >/tmp/e2e_dry.log 2>&1 || { cat /tmp/e2e_dry.log; bad "dry-run 失败"; exit 1; }
cat /tmp/e2e_dry.log
# round-trip 未改动：白板应不出现在 delete/insert 计划中
if grep -qiE 'board|画板|whiteboard' /tmp/e2e_dry.log && grep -qiE 'delete|删除|insert|插入|新建' /tmp/e2e_dry.log; then
  echo "  ⚠️ dry-run 提到 board 与 删除/插入，请人工确认是否针对白板块"
fi
ok "dry-run 完成（详见上方计划；白板未变更应为 no-op）"

if [[ "$MODE" != "--write" ]]; then
  hr "只读校验完成"
  echo "PASS=$PASS FAIL=$FAIL"
  echo "（要跑完整写回 e2e：$0 \"$URL\" --write）"
  [[ "$FAIL" -eq 0 ]] || exit 1
  exit 0
fi

hr "5. 真实增量上传（写回，--incr）"
"$BIN" upload --source "$URL" --incr "$MD1" >/tmp/e2e_up.log 2>&1 || { cat /tmp/e2e_up.log; bad "上传失败"; exit 1; }
cat /tmp/e2e_up.log
ok "上传完成"
# 校验日志未对白板做删除/重建
if grep -qiE '删除.*白板|board.*delet|重建.*白板' /tmp/e2e_up.log; then bad "上传日志疑似删除/重建了白板"; else ok "上传日志无白板删除/重建"; fi
# 校正端点是否生效 / 回退
if grep -q '已校正.*白板' /tmp/e2e_up.log; then ok "block_move_after 位置校正生效（docs_ai/v1 可用）"; fi
if grep -q '无法自动校正白板位置' /tmp/e2e_up.log; then echo "  ℹ️ move 端点不可用，已优雅回退（白板已保留，位置可能不对齐）"; fi

hr "6. 复查：重新下载，token 应保持不变（白板被复用而非重建）"
"$BIN" download "$URL" -o "$WORK/dl2" --comments=false >/tmp/e2e_dl2.log 2>&1 || { cat /tmp/e2e_dl2.log; bad "复查下载失败"; exit 1; }
MD2="$(find "$WORK/dl2" -name '*.md' | head -1)"
TOKEN2="$(extract_token "$MD2")"
if [[ "$TOKEN1" == "$TOKEN2" && -n "$TOKEN2" ]]; then
  ok "白板 token 不变（$TOKEN2）——round-trip 复用原白板，未删除重建"
else
  bad "白板 token 变化：上传前=$TOKEN1 上传后=$TOKEN2（白板被重建/丢失）"
fi

hr "完成"
echo "PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] || exit 1
