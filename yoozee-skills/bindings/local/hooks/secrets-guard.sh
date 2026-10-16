#!/bin/bash
# 密钥守门：两件事。一、命令行里出现像密钥的字符串就拦（密钥只能从 .env 读，不能写进命令、日志、记录）。
# 二、git commit 时暂存区里有 .env 或像密钥的改动就拦。别的命令一律放行。不依赖 jq。
INPUT=$(cat 2>/dev/null)
KEYPAT='sk-ant-[A-Za-z0-9_-]{8,}|sk-proj-[A-Za-z0-9_-]{8,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[bpsa]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|eyJ[A-Za-z0-9_-]{30,}\.[A-Za-z0-9_-]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----'
if printf '%s' "$INPUT" | grep -qE "$KEYPAT"; then
  echo "拦住了：命令里有像密钥的字符串。密钥只放 .env，脚本里用 common.need() 读，不写进命令和文件。" >&2
  exit 2
fi
case "$INPUT" in *"git commit"*) ;; *) exit 0 ;; esac
ROOT="${CLAUDE_PROJECT_DIR:-$PWD}"; cd "$ROOT" || exit 0
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0
STAGED=$(git diff --cached --name-only 2>/dev/null); [ -z "$STAGED" ] && exit 0
if printf '%s\n' "$STAGED" | grep -E '(^|/)\.env(\.|$)' | grep -vqE '\.env\.(example|sample|template)$'; then
  echo "提交被拦住：.env 在暂存区里。把它加进 .gitignore 再提交。" >&2; exit 2
fi
HIT=$(git diff --cached -U0 2>/dev/null | grep -E '^\+' | grep -vE '^\+\+\+' | grep -nE "$KEYPAT" | head -3)
if [ -n "$HIT" ]; then
  echo "提交被拦住：暂存的改动里像是有密钥：" >&2; printf '%s\n' "$HIT" | cut -c1-120 >&2; exit 2
fi
exit 0
