#!/usr/bin/env bash
# ENT-12：数据范围契约门禁。
#
# 四项断言，防止「scope 原语存在但没人用」与「仓储过滤被悄悄绕过」：
#   AZ-01 原语契约：authz 包必须提供 EnforceMerchant / ScopeWhere /
#        ErrScopeDenied / EnforceFromContext（rbac-iam.md §4.4 第 5 条）
#   AZ-02 越权自动化用例存在：authz scope 用例 + 业务域越权用例
#   AZ-03 仓储强制过滤：通知域仓储 List 必须对空租户键 fail closed
#        （禁止全表通知单查询路径），且 SQL 带 tenant_id 条件
#   AZ-04 业务接线：租户域写汇聚点（mutableTenant）与通知 transport
#        必须引用 EnforceFromContext / enforceDataScope
#
# 用法: bash scripts/ci/check-authz.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "    ERROR: $1" >&2; FAIL=1; }

echo "==> AZ-01: authz scope primitives"
for symbol in "func (s Snapshot) EnforceMerchant" "type ScopeWhere struct" \
  "ErrScopeDenied =" "func EnforceFromContext"; do
  if ! grep -rq "${symbol}" internal/shared/authz/*.go; then
    fail "authz primitive missing: ${symbol}"
  fi
done
if [[ $FAIL -eq 0 ]]; then
  echo "    OK: EnforceMerchant / ScopeWhere / ErrScopeDenied / EnforceFromContext"
fi

echo "==> AZ-02: cross-tenant denial test cases"
grep -q "TestScopeWhereAllows\|TestEnforceMerchant" internal/shared/authz/scope_test.go \
  || fail "authz scope unit tests missing (internal/shared/authz/scope_test.go)"
grep -q "TestDataScopeBlocksCrossTenant" internal/tenant/app/data_scope_test.go \
  || fail "tenant app cross-tenant denial tests missing (internal/tenant/app/data_scope_test.go)"
grep -q "TestNotificationHandlerDataScopeDenied" internal/notification/transport/rest/handler_test.go \
  || fail "notification handler data scope test missing"
if [[ $FAIL -eq 0 ]]; then
  echo "    OK: denial cases present in authz / tenant / notification"
fi

echo "==> AZ-03: repository fail closed on empty tenant"
grep -q "ErrTenantIDRequired" internal/notification/infrastructure/persistence/mysql_notification.go \
  || fail "MySQL request repo List must reject empty tenant (fail closed)"
grep -q "ErrTenantIDRequired" internal/notification/infrastructure/persistence/memory_notification.go \
  || fail "memory request repo List must reject empty tenant (fail closed)"
grep -q 'where := \[\]string{"tenant_id = ?"}' internal/notification/infrastructure/persistence/mysql_notification.go \
  || fail "MySQL request repo List SQL must pin tenant_id"
if [[ $FAIL -eq 0 ]]; then
  echo "    OK: notification repos reject empty tenant; SQL pinned to tenant_id"
fi

echo "==> AZ-04: business wiring"
grep -q "authz.EnforceFromContext" internal/tenant/app/service.go \
  || fail "tenant app write choke point (mutableTenant) must enforce data scope"
for f in internal/tenant/app/member.go internal/tenant/app/quota.go internal/tenant/app/invitation.go; do
  grep -q "enforceDataScope" "$f" \
    || fail "tenant read path must enforce data scope: $f"
done
grep -q "EnforceFromContext" internal/notification/transport/rest/handler.go \
  || fail "notification transport must enforce data scope"
if [[ $FAIL -eq 0 ]]; then
  echo "    OK: tenant app + notification transport wired to authz"
fi

if [[ $FAIL -ne 0 ]]; then
  echo "check-authz: FAILED" >&2
  exit 1
fi
echo "check-authz: OK"
