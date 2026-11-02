#!/usr/bin/env bash
# DOC-01：docs/ 与 README.md 的相对 Markdown 链接有效性。
# 用法: bash scripts/ci/check-doclinks.sh [目录或文件...]
# 说明：不并入 make ci（改文档结构时手动跑；CI 中如需启用，在 ci job 里加一行即可）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "ERROR: 需要 python3 或 python 运行文档链接检查" >&2
  exit 1
fi

exec "${PY}" scripts/ci/doclink_check.py "$@"
