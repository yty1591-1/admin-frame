#!/usr/bin/env bash
# PLAT-12 / ARCHITECTURE §6.1：分层依赖门禁。
#
# 规则：
#   1. transport 只依赖 app/domain/shared/pkg，不得 import internal/platform；
#   2. platform 不依赖业务域（internal/<domain>/{domain,app,infrastructure}）；
#   3. security 不依赖 platform，也不依赖业务域；
#   4. shared 不依赖 platform 与业务域（避免契约层反向依赖）；
#   5. iam/domain 不依赖 app/transport/infrastructure/platform；
#   6. iam/transport 不依赖 iam/infrastructure；
#   7. platform 单包非测试文件数与单文件行数（递归含全部子包）不超过拆分基线
#      （LAYER-07/08，PLAT-15）；
#   8. platform 不得新增业务语义端口（Tenant / Quota / Merchant 相关 interface，
#      LAYER-09，PLAT-15 / ARCHITECTURE §5.6「不承载业务语义」）；
#   9. pkg 不得 import internal/*（LAYER-10：可复用库层不得反向依赖应用内实现）。
#
# 基线只减不增：每次完成拆分后必须同步下调基线值，禁止以扩容基线的方式放行。
#
# 业务域目录动态发现（internal/*，排除 platform/security/shared），
# 新增业务域时无需改脚本。
#
# 用法: bash scripts/ci/check-layering.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

fail_with() {
  echo "ERROR: $1" >&2
  FAIL=1
}

# PLAT-15 拆分基线（ARCHITECTURE §5.6）。
#
# 阶段 1/2 已拆出 config / observability / httpserver / authn / async / storage
# 子包；阶段 3 将守卫端口下沉到业务域，根包仅剩装配与兼容转发文件（5 个）。
# 基线只减不增，禁止上调。
PLATFORM_DIR="internal/platform"
PLATFORM_MAX_FILES=5
PLATFORM_MAX_FILE_LINES=500
# 已登记的超限文件（拆分完成后必须从本数组删除）。
LAYER_LINE_BASELINE=()
# 已登记的业务语义端口（下沉到业务域后必须从本数组删除）。
LAYER_SEMANTIC_BASELINE=()

DOMAINS_RE=""
for d in internal/*/; do
  [[ -d "${d}" ]] || continue
  name="$(basename "${d}")"
  case "${name}" in
    platform|security|shared) continue ;;
  esac
  if [[ -z "${DOMAINS_RE}" ]]; then
    DOMAINS_RE="${name}"
  else
    DOMAINS_RE="${DOMAINS_RE}|${name}"
  fi
done

if [[ -z "${DOMAINS_RE}" ]]; then
  fail_with "no business domain found under internal/ (PLAT-12)"
fi

echo "==> LAYER-01: transport must not import internal/platform"
if grep -RIn --include='*.go' --exclude='*_test.go' \
  '"github.com/admin-frame/admin-frame/internal/platform"' \
  internal/*/transport 2>/dev/null; then
  fail_with "transport must depend on shared contracts, not platform (LAYER-01)"
else
  echo "    OK: transport has no platform import"
fi

echo "==> LAYER-02: platform must not import business domains"
if grep -RIn --include='*.go' --exclude='*_test.go' -E \
  '"github.com/admin-frame/admin-frame/internal/(${DOMAINS_RE})/(app|domain|infrastructure)' \
  internal/platform 2>/dev/null; then
  fail_with "platform must not depend on business domains (LAYER-02)"
else
  echo "    OK: platform has no business domain import"
fi

echo "==> LAYER-03: security must not import platform or business domains"
if grep -RIn --include='*.go' --exclude='*_test.go' -E \
  '"github.com/admin-frame/admin-frame/internal/(platform|${DOMAINS_RE})' \
  internal/security 2>/dev/null; then
  fail_with "security must stay independent of platform and domains (LAYER-03)"
else
  echo "    OK: security is independent"
fi

echo "==> LAYER-04: shared must not import platform or business domains"
if grep -RIn --include='*.go' --exclude='*_test.go' -E \
  '"github.com/admin-frame/admin-frame/internal/(platform|${DOMAINS_RE})' \
  internal/shared 2>/dev/null; then
  fail_with "shared must not depend on platform or domains (LAYER-04)"
else
  echo "    OK: shared is independent"
fi

echo "==> LAYER-05: iam/domain must not import app/transport/infrastructure/platform"
if grep -RIn --include='*.go' --exclude='*_test.go' -E \
  '"github.com/admin-frame/admin-frame/internal/(platform|iam/(app|transport|infrastructure))' \
  internal/iam/domain 2>/dev/null; then
  fail_with "iam/domain must only depend on shared contracts (LAYER-05)"
else
  echo "    OK: iam/domain is isolated"
fi

echo "==> LAYER-06: iam/transport must not import iam/infrastructure"
if grep -RIn --include='*.go' --exclude='*_test.go' \
  '"github.com/admin-frame/admin-frame/internal/iam/infrastructure' \
  internal/iam/transport 2>/dev/null; then
  fail_with "iam/transport must go through app interfaces, not infrastructure (LAYER-06)"
else
  echo "    OK: iam/transport has no infrastructure import"
fi

echo "==> LAYER-07: platform package size (PLAT-15)"
platform_files=0
for f in "${PLATFORM_DIR}"/*.go; do
  [[ -f "${f}" ]] || continue
  [[ "${f}" == *_test.go ]] && continue
  platform_files=$((platform_files + 1))
done
if [[ "${platform_files}" -gt "${PLATFORM_MAX_FILES}" ]]; then
  fail_with "platform has ${platform_files} non-test files, baseline ${PLATFORM_MAX_FILES} (PLAT-15: split the package)"
else
  echo "    OK: platform non-test files ${platform_files}/${PLATFORM_MAX_FILES}"
fi

echo "==> LAYER-08: platform file size (PLAT-15)"
# 递归覆盖全部子包：历史上只扫根包，导致 async/async_nats.go（622 行）与
# secrets/vault.go（619 行）长期超限而门禁仍绿（评审差距 G3）。
while IFS= read -r f; do
  [[ -f "${f}" ]] || continue
  [[ "${f}" == *_test.go ]] && continue
  lines=$(awk 'END { print NR }' "${f}")
  [[ "${lines}" -le "${PLATFORM_MAX_FILE_LINES}" ]] && continue
  rel="${f#${PLATFORM_DIR}/}"
  registered=0
  for b in "${LAYER_LINE_BASELINE[@]}"; do
    [[ "${rel}" == "${b}" ]] && registered=1
  done
  if [[ "${registered}" -eq 0 ]]; then
    fail_with "${f} has ${lines} lines > ${PLATFORM_MAX_FILE_LINES} (PLAT-15: split the file)"
  else
    echo "    BASELINE: ${rel} ${lines} lines (must leave the baseline after split)"
  fi
done < <(find "${PLATFORM_DIR}" -name '*.go' -type f | sort)

echo "==> LAYER-09: platform must not define business-semantic ports (PLAT-15)"
semantic_matches=$(grep -RHo --include='*.go' --exclude='*_test.go' -E \
  "^type ([A-Za-z]*(Tenant|Quota|Merchant)[A-Za-z]*) interface" \
  "${PLATFORM_DIR}" 2>/dev/null || true)
if [[ -z "${semantic_matches}" ]]; then
  echo "    OK: no business-semantic port in platform"
else
  while IFS= read -r match; do
    [[ -z "${match}" ]] && continue
    name="${match##*:type }"
    name="${name%% *}"
    registered=0
    for b in "${LAYER_SEMANTIC_BASELINE[@]}"; do
      [[ "${name}" == "${b}" ]] && registered=1
    done
    if [[ "${registered}" -eq 0 ]]; then
      fail_with "platform defines business-semantic port ${name} (PLAT-15: move it to the owning domain)"
    else
      echo "    BASELINE: ${name} (must move to the owning domain)"
    fi
  done <<< "${semantic_matches}"
fi

echo "==> LAYER-10: pkg must not import internal (dependency inversion)"
# pkg/ 是可复用库层：一旦 import internal/*，就丧失「可独立复用 / 可独立发布」属性，
# 并把测试工具链与业务实现绑死（评审差距 G2，pkg/testkit → internal/identity）。
if grep -RIn --include='*.go' -E \
  '"github.com/admin-frame/admin-frame/internal/' \
  pkg 2>/dev/null; then
  fail_with "pkg must stay reusable: it must not import internal/* (LAYER-10)"
else
  echo "    OK: pkg has no internal import"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "layering check failed (PLAT-12 / PLAT-15 / ARCHITECTURE §6.1)" >&2
  exit 1
fi
echo "OK: layering"
