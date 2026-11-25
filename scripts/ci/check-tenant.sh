#!/usr/bin/env bash
# ENT-09 T-17：租户域门禁。
#
# 三项对账，防止「代码与文档/契约各自漂移」：
#   TN-01 代码中的 tenant.* 审计动作常量 ↔ docs/audit/EVENTS.md 双向对账
#        （P2-17：新增动作靠人肉同步 EVENTS.md 的补丁）
#   TN-02 路由 ↔ OpenAPI 双向对账：transport 注册的每条路由必须在 admin.yaml
#        paths 段挂载并定义了对应方法；契约里声明的方法必须有路由实现
#        （曾抓到 7.5.0 邀请端点漏挂 paths 段）
#   TN-03 状态机迁移矩阵快照：transitions 白名单以测试内的硬编码快照锁定，
#        改白名单必须同步改测试（go test 承担，脚本负责触发）
#
# 用法: bash scripts/ci/check-tenant.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "ERROR: 需要 python3 或 python（TN-04 静态断言）" >&2
  exit 1
fi

FAIL=0

# 提取 EVENTS.md 中的 tenant.* 动作（表格第一列反引号包裹）。
doc_actions() {
  grep -o '`tenant\.[a-z_.]*`' docs/audit/EVENTS.md | tr -d '`' | sort -u
}

echo "==> TN-01: audit actions code <-> EVENTS.md"
# 审计动作常量的形式是 Action* = "tenant.xxx"；Topic / 其他常量（如 TopicAudit）
# 不算动作，必须排除——否则会出现假阳性。
mapfile -t CODE < <(
  grep -rhoE 'Action[A-Za-z]+ *= *"tenant\.[a-z_.]+"' internal/tenant/app/ --include='*.go' 2>/dev/null \
    | grep -v _test | sed -E 's/.*"(tenant\.[a-z_.]+)".*/\1/' | sort -u
)
mapfile -t DOC < <(doc_actions)
if [[ ${#CODE[@]} -eq 0 ]]; then
  echo "    ERROR: no tenant.* action constants found in internal/tenant/app" >&2
  FAIL=1
elif [[ ${#DOC[@]} -eq 0 ]]; then
  echo "    ERROR: no tenant.* actions found in docs/audit/EVENTS.md" >&2
  FAIL=1
else
  MISSING_IN_DOC=$(comm -23 <(printf '%s\n' "${CODE[@]}") <(printf '%s\n' "${DOC[@]}"))
  MISSING_IN_CODE=$(comm -13 <(printf '%s\n' "${CODE[@]}") <(printf '%s\n' "${DOC[@]}"))
  if [[ -n "${MISSING_IN_DOC}" ]]; then
    echo "    ERROR: actions in code but missing in EVENTS.md:" >&2
    echo "${MISSING_IN_DOC}" | sed 's/^/      /' >&2
    FAIL=1
  fi
  if [[ -n "${MISSING_IN_CODE}" ]]; then
    echo "    ERROR: actions in EVENTS.md but missing in code (stale docs?):" >&2
    echo "${MISSING_IN_CODE}" | sed 's/^/      /' >&2
    FAIL=1
  fi
  if [[ ${FAIL} -eq 0 ]]; then
    echo "    OK: ${#CODE[@]} actions in sync"
  fi
fi

echo "==> TN-02: routes <-> OpenAPI paths"
# 路由侧：transport 注册的 METHOD PATH（Go 1.22 pattern，{param} 与 OpenAPI 一致）。
mapfile -t ROUTES < <(
  grep -rhoE '"(GET|POST|PATCH|PUT|DELETE) /admin/v1/tenants[^"]*"' \
    internal/tenant/transport/admin/router.go | tr -d '"' | sort -u
)
# 契约侧：awk 逐行解析 admin.yaml——tenant 路径行与其下一行的 $ref 行配对，
# 再到 tenant.yaml 里查操作定义下的 HTTP 方法。
mapfile -t OPENAPI < <(
  awk '
    { sub(/\r$/, "") }
    /^  \/admin\/v1\/tenants[^:]*:$/ {sub(/:$/, "", $1); path=$1; next}
    path != "" && /\$ref:/ {
      opid=$0; sub(/.*#\//, "", opid); sub(/\047.*$/, "", opid)
      print path " " opid; path=""
    }
  ' api/openapi/admin.yaml | while read -r path opid; do
    # 操作下可能有多个方法（如同路径的 GET 列表 + POST 创建），全部列出。
    methods=$(awk -v op="${opid}" '
      $0 ~ "^" op ":" {inop=1; next}
      inop && /^[^ ]/ {inop=0}
      inop && /^(  get|  post|  patch|  put|  delete):/ {
        gsub(/[: ]/, "", $1); print $1}
    ' api/openapi/paths/admin/tenant.yaml)
    while IFS= read -r method; do
      [[ -n "${method}" ]] && printf '%s %s\n' "$(echo "${method}" | tr a-z A-Z)" "${path}"
    done <<< "${methods}"
  done | sort -u
)
if [[ ${#ROUTES[@]} -eq 0 ]]; then
  echo "    ERROR: no tenant routes found in transport/admin/router.go" >&2
  FAIL=1
elif [[ ${#OPENAPI[@]} -eq 0 ]]; then
  echo "    ERROR: no tenant paths mounted in api/openapi/admin.yaml" >&2
  FAIL=1
else
  ROUTE_ONLY=$(comm -23 <(printf '%s\n' "${ROUTES[@]}") <(printf '%s\n' "${OPENAPI[@]}"))
  OPENAPI_ONLY=$(comm -13 <(printf '%s\n' "${ROUTES[@]}") <(printf '%s\n' "${OPENAPI[@]}"))
  if [[ -n "${ROUTE_ONLY}" ]]; then
    echo "    ERROR: routes not mounted in admin.yaml paths (see T-17 lesson):" >&2
    echo "${ROUTE_ONLY}" | sed 's/^/      /' >&2
    FAIL=1
  fi
  if [[ -n "${OPENAPI_ONLY}" ]]; then
    echo "    ERROR: OpenAPI operations without a route implementation:" >&2
    echo "${OPENAPI_ONLY}" | sed 's/^/      /' >&2
    FAIL=1
  fi
  if [[ ${FAIL} -eq 0 ]]; then
    echo "    OK: ${#ROUTES[@]} routes in sync"
  fi
fi

echo "==> TN-03: transition matrix snapshot (go test)"
if go test ./internal/tenant/domain/ -run TestTransitionMatrixSnapshot -count=1 > /dev/null 2>&1; then
  echo "    OK: matrix unchanged"
else
  echo "    ERROR: transition matrix changed - update the snapshot in tenant_test.go" >&2
  FAIL=1
fi

echo "==> TN-04: activation requires an approved application"
# 审核流是 ENT-09 的验收缺口，只靠单测覆盖不够：这里检查「开通前置还在、
# 且审批校验排在 owner 校验之前」，防止将来重构把审批前置挪走或改成可跳过。
svc="internal/tenant/app/service.go"
router="internal/tenant/transport/admin/router.go"
if [[ ! -f "${svc}" ]]; then
  echo "    ERROR: missing ${svc}" >&2
  FAIL=1
elif ! grep -rqF 'func (s *Service) ensureApproved(' internal/tenant/app/; then
  echo "    ERROR: missing ensureApproved definition (TN-04)" >&2
  FAIL=1
elif ! "${PY}" - "${svc}" <<'PY'
import pathlib, sys
text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
call = text.find("s.ensureApproved(txCtx")
owner = text.find("s.members.CountActiveByRole(txCtx")
if call < 0:
    print("ERROR: activation path does not call ensureApproved (TN-04)", file=sys.stderr)
    raise SystemExit(1)
if owner >= 0 and call > owner:
    print("ERROR: approval check must run before owner check (TN-04)", file=sys.stderr)
    raise SystemExit(1)
raise SystemExit(0)
PY
then
  FAIL=1
else
  echo "    OK: pending → active checks approval before owner"
fi

for route in \
  'GET /admin/v1/tenants/{id}/approval' \
  'POST /admin/v1/tenants/{id}/approval' \
  'POST /admin/v1/tenants/{id}/approval/approve' \
  'POST /admin/v1/tenants/{id}/approval/reject'; do
  if ! grep -qF "\"${route}\"" "${router}" 2>/dev/null; then
    echo "    ERROR: router missing '${route}' (TN-04)" >&2
    FAIL=1
  fi
done
if ! grep -qF 'PermTenantApprove' "${router}"; then
  echo "    ERROR: approval decisions must use tenant:approve, not tenant:write (TN-04)" >&2
  FAIL=1
fi
if ! grep -rqF "tenant:approve" migrations/; then
  echo "    ERROR: missing permission seed for tenant:approve (TN-04)" >&2
  FAIL=1
fi
if ! grep -qF "tenant:approve" docs/iam/PERMISSIONS.md; then
  echo "    ERROR: docs/iam/PERMISSIONS.md missing tenant:approve (TN-04)" >&2
  FAIL=1
fi
echo "    OK: approval routes and permission wired"

if [[ ${FAIL} -ne 0 ]]; then
  echo "FAIL: check-tenant" >&2
  exit 1
fi
echo "OK: check-tenant"
