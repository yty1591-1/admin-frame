#!/usr/bin/env bash
# PLAT-12 / ARCHITECTURE §6.1：分层依赖门禁。
#
# 规则：
#   1. transport 只依赖 app/domain/shared/pkg，不得 import internal/platform；
#   2. platform 不依赖业务域（internal/<domain>/{domain,app,infrastructure}）；
#   3. security 不依赖 platform，也不依赖业务域；
#   4. shared 不依赖 platform 与业务域（避免契约层反向依赖）；
#   5. iam/domain 不依赖 app/transport/infrastructure/platform；
#   6. iam/transport 不依赖 iam/infrastructure。
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

if [[ "${FAIL}" -ne 0 ]]; then
  echo "layering check failed (PLAT-12 / ARCHITECTURE §6.1)" >&2
  exit 1
fi
echo "OK: layering"
