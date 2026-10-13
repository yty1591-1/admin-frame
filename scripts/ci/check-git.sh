#!/usr/bin/env bash
# GIT_STANDARDS：PR 模板与 commitlint 配置存在性。
# 用法: bash scripts/ci/check-git.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> GIT-06: PR templates must exist"
for f in .github/PULL_REQUEST_TEMPLATE.md .gitee/PULL_REQUEST_TEMPLATE.md; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f} (GIT-06)" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> GIT-06: PR templates must include Git 自检 section"
for f in .github/PULL_REQUEST_TEMPLATE.md .gitee/PULL_REQUEST_TEMPLATE.md; do
  [[ -f "${f}" ]] || continue
  if ! grep -q 'Git 自检\|GIT_STANDARDS' "${f}"; then
    echo "ERROR: ${f} missing Git 自检 / GIT_STANDARDS section (GIT-06)" >&2
    FAIL=1
  else
    echo "    OK: ${f} has Git checklist"
  fi
done

echo "==> GIT-05: commitlint config present"
if [[ -f commitlint.config.cjs ]] || [[ -f commitlint.config.js ]] || [[ -f .commitlintrc.yaml ]]; then
  echo "    OK: commitlint config found"
else
  echo "ERROR: missing commitlint.config.cjs (GIT-05)" >&2
  FAIL=1
fi

echo "==> GIT: standards doc present"
if [[ -f docs/GIT_STANDARDS.md ]]; then
  echo "    OK: docs/GIT_STANDARDS.md"
else
  echo "ERROR: missing docs/GIT_STANDARDS.md" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "git check failed (GIT_STANDARDS)" >&2
  exit 1
fi
echo "OK: git"
