#!/usr/bin/env bash
# 校验前端构建产物含版本与提交 SHA（FE-B-01）。
#
# 产物首页通过 viteMetadataPlugin 注入 x-app-version / x-app-git-sha /
# x-app-build-time 三个 meta；CI 要求 SHA 为 40 位十六进制（tarball 构建没有
# .git 时走 GITHUB_SHA/CI_COMMIT_SHA，缺失则构建元数据此处失败）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fail=0

for app in web-admin web-biz; do
  index="${ROOT}/frontend/apps/${app}/dist/index.html"
  if [[ ! -f "$index" ]]; then
    echo "ERROR: 缺少 ${index}（先执行 pnpm build:${app#web-}）" >&2
    fail=1
    continue
  fi

  sha_meta="$(grep -o '<meta[^>]*name="x-app-git-sha"[^>]*>' "$index" || true)"
  if [[ -z "$sha_meta" ]]; then
    echo "ERROR: ${app} dist/index.html 缺少 x-app-git-sha meta" >&2
    fail=1
  elif ! grep -Eq 'content="[0-9a-f]{40}"' <<<"$sha_meta"; then
    echo "ERROR: ${app} dist/index.html 的提交 SHA 不是 40 位十六进制：${sha_meta}" >&2
    fail=1
  else
    sha="$(sed -E 's/.*content="([0-9a-f]{40})".*/\1/' <<<"$sha_meta")"
    echo "OK: ${app} git-sha=${sha}"
  fi

  for meta in x-app-version x-app-build-time; do
    if ! grep -q "name=\"${meta}\"" "$index"; then
      echo "ERROR: ${app} dist/index.html 缺少 ${meta} meta" >&2
      fail=1
    fi
  done
done

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "构建元数据校验通过"
