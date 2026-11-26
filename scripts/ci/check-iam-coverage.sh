#!/usr/bin/env bash
# P1-5：IAM 分层覆盖率门禁。
#
# 与按包自覆盖不同，这里用 -coverpkg 合并 IAM 全部包的语句，
# 再按目录切分覆盖率档案，因此 transport/rest 的 handler 能被
# transport/admin/biz 与集成测试间接覆盖，persistence 也能计入
# tests/integration/iam 的真实 MySQL 用例。
#
# 设置了 IAM_TEST_MYSQL_DSN 时强制要求 persistence 与模块总覆盖率；
# 未设置时跳过这两项（本地无 MySQL 仍可跑其余门禁），CI 必须设置。
#
# 用法: bash scripts/ci/check-iam-coverage.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found in PATH" >&2
  exit 1
fi

# 每项格式：包相对路径:最低覆盖率。阈值在审计基线上留出小幅回退空间，
# 防止单次重构直接击穿门禁；目标值见 docs/archive/plans/iam-p1-5-7-engineering-plan.md。
GATES=(
  "internal/iam/domain:80"
  "internal/iam/app:75"
  "internal/iam/catalog:60"
  "internal/iam/infrastructure/cache:75"
  "internal/iam/infrastructure/persistence:50"
  "internal/iam/menuview:90"
  "internal/iam/observability:45"
  "internal/iam/transport/rest:60"
  "internal/iam/transport/admin:90"
  "internal/iam/transport/biz:90"
)
TOTAL_MIN="69"
INTEGRATION_REQUIRED=(
  "internal/iam/infrastructure/persistence"
)

profile="$(mktemp)"
tmp="$(mktemp)"
cleanup() { rm -f "${profile}" "${tmp}"; }
trap cleanup EXIT

echo "==> IAM coverage (P1-5): go test -coverpkg=./internal/iam/..."
if ! go test -count=1 -p 1 \
  -coverpkg=./internal/iam/... \
  -coverprofile="${profile}" \
  ./internal/iam/... ./tests/integration/iam/... >/dev/null; then
  echo "ERROR: IAM tests failed (P1-5)" >&2
  exit 1
fi
if [[ ! -s "${profile}" ]] || ! grep -q '^mode:' "${profile}"; then
  echo "ERROR: empty IAM coverage profile (P1-5)" >&2
  exit 1
fi

if [[ -z "${IAM_TEST_MYSQL_DSN:-}" ]]; then
  echo "WARN: IAM_TEST_MYSQL_DSN is empty; integration-backed gates are skipped locally" >&2
fi

FAIL=0
check_pkg() {
  local pkg="$1" min="$2"
  local need_integration=0
  for item in "${INTEGRATION_REQUIRED[@]}"; do
    if [[ "${item}" == "${pkg}" ]]; then
      need_integration=1
      break
    fi
  done
  if [[ "${need_integration}" -eq 1 && -z "${IAM_TEST_MYSQL_DSN:-}" ]]; then
    echo "    SKIP: ${pkg} (requires IAM_TEST_MYSQL_DSN)"
    return
  fi

  # 保持 profile 的 mode 行，只保留目标包的档案行。
  {
    head -1 "${profile}"
    grep "/${pkg}/" "${profile}" || true
  } >"${tmp}"
  if [[ "$(grep -cv '^mode:' "${tmp}" || true)" -eq 0 ]]; then
    echo "ERROR: no coverage data for ${pkg} (P1-5)" >&2
    FAIL=1
    return
  fi

  local pct
  pct="$(go tool cover -func="${tmp}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  if [[ -z "${pct}" ]]; then
    echo "ERROR: cannot parse coverage for ${pkg} (P1-5)" >&2
    FAIL=1
    return
  fi
  if ! awk -v p="${pct}" -v m="${min}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: ${pkg} coverage ${pct}% < ${min}% (P1-5)" >&2
    FAIL=1
  else
    echo "    OK: ${pkg} ${pct}% >= ${min}%"
  fi
}

for gate in "${GATES[@]}"; do
  check_pkg "${gate%:*}" "${gate##*:}"
done

if [[ -n "${IAM_TEST_MYSQL_DSN:-}" ]]; then
  total="$(go tool cover -func="${profile}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  if [[ -z "${total}" ]] || ! awk -v p="${total}" -v m="${TOTAL_MIN}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: internal/iam total coverage ${total}% < ${TOTAL_MIN}% (P1-5)" >&2
    FAIL=1
  else
    echo "    OK: internal/iam total ${total}% >= ${TOTAL_MIN}%"
  fi
fi

# 变更行覆盖率：仅在 PR/MR 且提供 GIT_BASE_REF 时执行。
if ! bash scripts/ci/check-iam-diff-coverage.sh "${profile}"; then
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "IAM coverage gates failed (P1-5)" >&2
  exit 1
fi
echo "OK: IAM coverage gates"
