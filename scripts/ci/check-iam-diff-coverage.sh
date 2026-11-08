#!/usr/bin/env bash
# P1-5：IAM 变更行覆盖率门禁。
#
# 计算 PR/MR 相对基线新增或修改的可执行行中，被 IAM 覆盖率档案命中的比例。
# 基线由 GIT_BASE_REF 指定（CI 传 origin/<base>）；本地未设置或基线不可达时跳过，
# 避免阻断无完整历史的本地 `make ci`。
#
# 用法: bash scripts/ci/check-iam-diff-coverage.sh <coverage-profile>
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

PROFILE="${1:-}"
MIN="${IAM_DIFF_COVERAGE_MIN:-70}"
BASE="${GIT_BASE_REF:-}"

if [[ -z "${PROFILE}" || ! -s "${PROFILE}" ]]; then
  echo "SKIP: diff coverage needs a coverage profile (P1-5)"
  exit 0
fi
if [[ -z "${BASE}" ]]; then
  echo "SKIP: GIT_BASE_REF empty; diff coverage only runs on PR/MR (P1-5)"
  exit 0
fi
if ! git rev-parse --verify --quiet "${BASE}^{commit}" >/dev/null; then
  echo "WARN: base ref '${BASE}' not found; diff coverage skipped (P1-5)"
  exit 0
fi
if ! command -v awk >/dev/null 2>&1; then
  echo "WARN: awk not found; diff coverage skipped (P1-5)"
  exit 0
fi

ranges="$(mktemp)"
cleanup() { rm -f "${ranges}"; }
trap cleanup EXIT

# 只统计 IAM 生产代码新增的非空、非注释行；-U0 下逐行推进新文件行号。
git diff --unified=0 "${BASE}...HEAD" -- internal/iam |
  awk '
    /^\+\+\+ b\// {
      file = substr($0, 7)
      skip = (file !~ /\.go$/ || file ~ /_test\.go$/)
      next
    }
    /^@@ / && !skip && file != "" {
      # @@ -old,oldCount +new,newCount @@
      match($0, /\+[0-9]+(,[0-9]+)?/)
      hunk = substr($0, RSTART + 1, RLENGTH - 1)
      split(hunk, parts, ",")
      newline = parts[1] + 0
      next
    }
    skip || file == "" { next }
    /^\+/ {
      text = substr($0, 2)
      if (text !~ /^[[:space:]]*$/ && text !~ /^[[:space:]]*\/\//) {
        print file "\t" newline
      }
      newline++
    }
  ' >"${ranges}"

# 分母只取「变更行 ∩ 覆盖率档案中的可执行语句行」：
# 覆盖率档案包含未执行语句（count=0），但永不含 package / import / 注释 / 空行，
# 因此用它做可执行行判定可以避免把 `package config` 这类行算成「未覆盖」——
# 否则仅新增包注释就会误报 0.0% 并阻断合并。
read -r total covered < <(awk '
  NR == FNR { split($0, cols, "\t"); changed[cols[1] SUBSEP cols[2]] = 1; next }
  /^mode:/ { next }
  {
    split($1, meta, ":")
    path = meta[1]
    idx = index(path, "/internal/iam/")
    if (idx == 0) { next }
    rel = substr(path, idx + 1)
    split(meta[2], spans, ",")
    split(spans[1], s, ".")
    split(spans[2], e, ".")
    count = $3 + 0
    for (line = s[1] + 0; line <= e[1] + 0; line++) {
      key = rel SUBSEP line
      if (!changed[key]) { next }
      if (!(key in seen)) {
        seen[key] = 1
        total++
      }
      if (count > 0 && !hit[key]) {
        hit[key] = 1
        covered++
      }
    }
  }
  END { print total + 0, covered + 0 }
' "${ranges}" "${PROFILE}")

total="${total:-0}"
covered="${covered:-0}"
if [[ "${total}" -eq 0 ]]; then
  echo "OK: no changed executable IAM lines; diff coverage skipped (P1-5)"
  exit 0
fi

pct="$(awk -v c="${covered}" -v t="${total}" 'BEGIN{printf "%.1f", 100*c/t}')"
echo "    IAM changed lines: ${covered}/${total} covered (${pct}%)"

if ! awk -v p="${pct}" -v m="${MIN}" 'BEGIN{exit !(p+0 >= m+0)}'; then
  echo "ERROR: IAM changed-line coverage ${pct}% < ${MIN}% (P1-5)" >&2
  exit 1
fi
echo "OK: IAM changed-line coverage"
