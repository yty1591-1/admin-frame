#!/usr/bin/env bash
# TESTING_STANDARDS：domain 单测存在性、覆盖率门槛、B/C 必测场景。
# 用法: bash scripts/ci/check-test-gates.sh
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

COVER_MIN="${TEST_DOMAIN_COVER_MIN:-70}"
PLATFORM_COVER_MIN="${TEST_PLATFORM_COVER_MIN:-70}"
SECURITY_COVER_MIN="${TEST_SECURITY_COVER_MIN:-80}"
# MNT-03 / R-01：身份域编排、路由与 handler 层覆盖率门禁。
# handler 层已补齐 E2E 用例，基线从 15% 上调到 70%，与其他层同口径。
IDENTITY_APP_COVER_MIN="${TEST_IDENTITY_APP_COVER_MIN:-75}"
IDENTITY_ROUTER_COVER_MIN="${TEST_IDENTITY_ROUTER_COVER_MIN:-70}"
IDENTITY_HANDLER_COVER_MIN="${TEST_IDENTITY_HANDLER_COVER_MIN:-70}"
REQUIRED_TESTS=(TestBizRoleTenantIsolation TestBizChangeRequestTenantIsolation)
# 仅扫描 Go 源码目录：仓库根下有 frontend/node_modules（数万文件），
# 整树递归 grep 在 WSL 的 /mnt/d（9p）上单次可达数十分钟。
GO_ROOTS=(cmd internal pkg scripts tests)
FAIL=0

echo "==> TEST-01: domain packages must have *_test.go"
DOMAIN_PKGS=()
while IFS= read -r pkg; do
  [[ -n "${pkg}" ]] && DOMAIN_PKGS+=("${pkg}")
done < <(go list ./internal/... | grep '/domain$' || true)

if [[ "${#DOMAIN_PKGS[@]}" -eq 0 ]]; then
  echo "ERROR: no packages matched ./internal/.../domain (TEST-01)" >&2
  exit 1
fi

for pkg in "${DOMAIN_PKGS[@]}"; do
  dir="$(go list -f '{{.Dir}}' "${pkg}")"
  found=0
  for f in "${dir}"/*_test.go; do
    if [[ -f "${f}" ]]; then
      found=1
      break
    fi
  done
  if [[ "${found}" -eq 0 ]]; then
    echo "ERROR: missing *_test.go in ${pkg} (${dir}) (TEST-01)" >&2
    FAIL=1
  else
    echo "    OK: ${pkg}"
  fi
done

echo "==> TEST-02: domain coverage >= ${COVER_MIN}% (per package)"
for pkg in "${DOMAIN_PKGS[@]}"; do
  tmp="$(mktemp)"
  if ! go test -count=1 -coverprofile="${tmp}" "${pkg}" >/dev/null; then
    echo "ERROR: go test failed for ${pkg} (TEST-02)" >&2
    rm -f "${tmp}"
    FAIL=1
    continue
  fi
  if [[ ! -s "${tmp}" ]] || ! grep -q '^mode:' "${tmp}"; then
    echo "    SKIP cover (empty profile): ${pkg}"
    rm -f "${tmp}"
    continue
  fi
  data_lines="$(grep -cv '^mode:' "${tmp}" || true)"
  if [[ "${data_lines}" -eq 0 ]]; then
    echo "    SKIP cover (no statements): ${pkg}"
    rm -f "${tmp}"
    continue
  fi
  pct="$(go tool cover -func="${tmp}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  rm -f "${tmp}"
  if [[ -z "${pct}" ]]; then
    echo "ERROR: cannot parse coverage for ${pkg}" >&2
    FAIL=1
    continue
  fi
  if ! awk -v p="${pct}" -v m="${COVER_MIN}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: ${pkg} coverage ${pct}% < ${COVER_MIN}% (TEST-02)" >&2
    FAIL=1
  else
    echo "    OK: ${pkg} ${pct}%"
  fi
done

echo "==> TEST-02b: internal/platform coverage >= ${PLATFORM_COVER_MIN}% (per package)"
tmp="$(mktemp)"
if ! go test -count=1 -coverprofile="${tmp}" ./internal/platform >/dev/null; then
  echo "ERROR: go test failed for ./internal/platform (TEST-02b)" >&2
  rm -f "${tmp}"
  FAIL=1
else
  pct="$(go tool cover -func="${tmp}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  rm -f "${tmp}"
  if [[ -z "${pct}" ]] || ! awk -v p="${pct}" -v m="${PLATFORM_COVER_MIN}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: ./internal/platform coverage ${pct}% < ${PLATFORM_COVER_MIN}% (TEST-02b)" >&2
    FAIL=1
  else
    echo "    OK: internal/platform ${pct}%"
  fi
fi

echo "==> TEST-02c: internal/security coverage >= ${SECURITY_COVER_MIN}% (per package)"
tmp="$(mktemp)"
if ! go test -count=1 -coverprofile="${tmp}" ./internal/security >/dev/null; then
  echo "ERROR: go test failed for ./internal/security (TEST-02c)" >&2
  rm -f "${tmp}"
  FAIL=1
else
  pct="$(go tool cover -func="${tmp}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  rm -f "${tmp}"
  if [[ -z "${pct}" ]] || ! awk -v p="${pct}" -v m="${SECURITY_COVER_MIN}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: ./internal/security coverage ${pct}% < ${SECURITY_COVER_MIN}% (TEST-02c)" >&2
    FAIL=1
  else
    echo "    OK: internal/security ${pct}%"
  fi
fi

# MNT-03：身份域门禁。app 编排与 admin/biz 路由层要求 70%+；
# rest / c 的 handler E2E 覆盖率仍在补齐，先按当前基线设下限，避免门禁失效。
check_identity_cover() {
  local pkg="$1" min="$2" label="$3"
  local tmp
  tmp="$(mktemp)"
  if ! go test -count=1 -coverprofile="${tmp}" "${pkg}" >/dev/null; then
    echo "ERROR: go test failed for ${pkg} (${label})" >&2
    rm -f "${tmp}"
    FAIL=1
    return
  fi
  local pct
  pct="$(go tool cover -func="${tmp}" | awk '/^total:/{gsub(/%/,"",$3); print $3; exit}')"
  rm -f "${tmp}"
  if [[ -z "${pct}" ]] || ! awk -v p="${pct}" -v m="${min}" 'BEGIN{exit !(p+0 >= m+0)}'; then
    echo "ERROR: ${pkg} coverage ${pct}% < ${min}% (${label})" >&2
    FAIL=1
  else
    echo "    OK: ${pkg} ${pct}%"
  fi
}

echo "==> TEST-06: identity app coverage >= ${IDENTITY_APP_COVER_MIN}%"
check_identity_cover "./internal/identity/app" "${IDENTITY_APP_COVER_MIN}" "TEST-06"

echo "==> TEST-06b: identity router coverage >= ${IDENTITY_ROUTER_COVER_MIN}%"
check_identity_cover "./internal/identity/transport/admin" "${IDENTITY_ROUTER_COVER_MIN}" "TEST-06b"
check_identity_cover "./internal/identity/transport/biz" "${IDENTITY_ROUTER_COVER_MIN}" "TEST-06b"

echo "==> TEST-06c: identity rest/c handler coverage >= ${IDENTITY_HANDLER_COVER_MIN}%"
check_identity_cover "./internal/identity/transport/rest" "${IDENTITY_HANDLER_COVER_MIN}" "TEST-06c"
check_identity_cover "./internal/identity/transport/c" "${IDENTITY_HANDLER_COVER_MIN}" "TEST-06c"

echo "==> TEST-03: required isolation / ownership tests must exist"
for name in "${REQUIRED_TESTS[@]}"; do
  if ! grep -RIn --include='*_test.go' -E "^func ${name}\\(t \\*testing\\.T\\)" "${GO_ROOTS[@]}" >/dev/null; then
    echo "ERROR: required test ${name} not found (TEST-03)" >&2
    FAIL=1
  else
    echo "    found: ${name}"
  fi
done

run_pattern="^($(IFS='|'; echo "${REQUIRED_TESTS[*]}"))$"
race_args=()
if [[ "$(go env CGO_ENABLED)" == "1" ]] && { command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; }; then
  race_args=(-race)
  echo "==> TEST-03: go test -race -run '${run_pattern}'"
else
  echo "==> TEST-03: go test -run '${run_pattern}' (WARN: -race skipped; no CGO toolchain — CI Linux still enforces -race)"
fi
if ! go test -count=1 "${race_args[@]}" "./..." -run "${run_pattern}"; then
  echo "ERROR: required tests failed (TEST-03)" >&2
  FAIL=1
fi

# TEST-05：test job / step 邻近不得注入 secrets.（生产凭据）。
echo "==> TEST-05: test CI steps must not use secrets.*"
check_test_secrets() {
  local file="$1"
  local start_re="$2"
  if [[ ! -f "${file}" ]]; then
    return 0
  fi
  if awk -v start_re="${start_re}" '
    $0 ~ start_re { in_test=1; next }
    in_test && /secrets\./ { print FILENAME ":" NR ": " $0; bad=1 }
    in_test && /^  [a-zA-Z0-9_]+:/ && $0 !~ /^  test:/ { in_test=0 }
    in_test && /^      - step:/ { in_test=0 }
    END { if (bad) exit 1; exit 0 }
  ' "${file}"; then
    echo "    OK: ${file}"
    return 0
  fi
  echo "ERROR: ${file}: test job/step uses secrets.* (TEST-05)" >&2
  return 1
}

if ! check_test_secrets ".github/workflows/ci.yml" "^  test:"; then
  FAIL=1
fi
for f in .workflow/PRPipeline.yml .workflow/MasterPipeline.yml .workflow/BranchPipeline.yml; do
  if [[ -f "${f}" ]]; then
    if ! check_test_secrets "${f}" "name: test$"; then
      FAIL=1
    fi
  fi
done

# P1-5：IAM 分层覆盖率（含 MySQL 集成测试的间接覆盖）。
echo "==> TEST-07: IAM layered coverage gates (P1-5)"
if ! bash scripts/ci/check-iam-coverage.sh; then
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test gates failed (TESTING_STANDARDS)" >&2
  exit 1
fi
echo "OK: test gates"
