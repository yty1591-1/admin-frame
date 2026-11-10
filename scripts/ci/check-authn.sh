#!/usr/bin/env bash
# ENT-01：身份认证模块门禁（生产配置 / 三端契约 / 撤销路径 / 迁移 / 审计 / 密钥）。
# 用法: bash scripts/ci/check-authn.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-01: production mfa baseline"
if ! grep -q 'identity.mfa.admin_required must be true' internal/identity/config/validate.go; then
  echo "ERROR: config.go missing production MFA baseline (identity.mfa.admin_required)" >&2
  FAIL=1
else
  echo "    OK: production MFA baseline"
fi
if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/platform/ -run 'TestValidateMFA|TestValidateSMS|TestValidateOAuth|TestValidateCChannel|TestValidateRevocationStateTTL'; then
    echo "ERROR: identity config validation tests failed" >&2
    FAIL=1
  else
    echo "    OK: identity config validation tests"
  fi
else
  echo "ERROR: go not found; cannot run identity config tests" >&2
  FAIL=1
fi

echo "==> ENT-01: auth endpoints on admin/biz/c"
for channel in admin biz c; do
  spec="api/openapi/${channel}.yaml"
  if [[ ! -f "${spec}" ]]; then
    echo "ERROR: missing ${spec}" >&2
    FAIL=1
    continue
  fi
  for path in auth/login auth/refresh auth/logout; do
    if ! grep -Fq "${path}" "${spec}"; then
      echo "ERROR: ${spec} missing ${path}" >&2
      FAIL=1
    else
      echo "    OK: ${channel} ${path}"
    fi
  done
done

echo "==> ENT-01: account mutation contract declares Idempotency-Key"
ACCOUNT_SPEC="api/openapi/paths/admin/auth.yaml"
if [[ ! -f "${ACCOUNT_SPEC}" ]]; then
  echo "ERROR: missing ${ACCOUNT_SPEC}" >&2
  FAIL=1
else
  idempotent_refs="$(grep -c 'components/parameters/IdempotencyKey' "${ACCOUNT_SPEC}" || true)"
  # 账号处置共 10 条 path：disable/enable/lock/unlock/restore/mfa-reset/
  # password-reset/delete/单会话撤销/全量撤销。
  if [[ "${idempotent_refs}" -ne 10 ]]; then
    echo "ERROR: expected 10 Idempotency-Key refs in ${ACCOUNT_SPEC}, got ${idempotent_refs}" >&2
    FAIL=1
  else
    echo "    OK: 10 account mutation paths declare Idempotency-Key"
  fi
fi
if ! grep -Fq 'schemas/BizLoginRequest' api/openapi/paths/biz/auth.yaml; then
  echo "ERROR: biz login must reference BizLoginRequest (merchant_id required)" >&2
  FAIL=1
else
  echo "    OK: biz login uses required merchant_id contract"
fi

echo "==> ENT-01: revocation path wired"
if ! grep -Rq 'RevocationChecker' internal/security; then
  echo "ERROR: internal/security does not reference RevocationChecker" >&2
  FAIL=1
else
  echo "    OK: RevocationChecker wired in security"
fi
REVOCATION_TESTS="$(grep -Rl --include='*_test.go' 'RevocationChecker' internal/security internal/identity || true)"
if [[ -z "${REVOCATION_TESTS}" ]]; then
  echo "ERROR: no revocation tests found" >&2
  FAIL=1
else
  echo "    OK: revocation tests present"
fi

echo "==> ENT-01: identity migration pair"
for suffix in up down; do
  file="migrations/000001_create_identity_tables.${suffix}.sql"
  if [[ ! -f "${file}" ]]; then
    echo "ERROR: missing ${file}" >&2
    FAIL=1
  else
    echo "    OK: ${file}"
  fi
done
MIGRATION_FILES="$(find migrations -maxdepth 1 -type f -name '000001_*' | wc -l | tr -d ' ')"
if [[ "${MIGRATION_FILES}" -ne 2 ]]; then
  echo "ERROR: expected exactly 2 files with 000001_ prefix, got ${MIGRATION_FILES}" >&2
  FAIL=1
else
  echo "    OK: 000001 migration pair only"
fi

echo "==> ENT-01: auth actions registered in audit catalog"
ACTIONS_FILE="internal/shared/audit/actions.go"
if [[ ! -f docs/audit/EVENTS.md || ! -f "${ACTIONS_FILE}" ]]; then
  echo "ERROR: missing docs/audit/EVENTS.md or ${ACTIONS_FILE}" >&2
  FAIL=1
else
  # 动作名允许数字（如 auth.m2m.*），否则会被截断成 auth.m 造成误报。
  AUTH_ACTIONS="$(grep -oE 'auth\.[a-z0-9._]+' docs/audit/EVENTS.md | sort -u)"
  for action in ${AUTH_ACTIONS}; do
    # 末尾 || true：无匹配时 grep 返回 1，在 set -euo pipefail 下会静默终止脚本，
    # 让「常量缺失」变成「门禁无输出即失败」——这里必须走到下面的 ERROR 分支。
    name="$(grep -oE "Action[A-Za-z0-9]+[[:space:]]*=[[:space:]]*\"${action}\"" "${ACTIONS_FILE}" | head -n1 | sed -E 's/^([A-Za-z0-9]+).*/\1/' || true)"
    if [[ -z "${name}" ]]; then
      echo "ERROR: ${ACTIONS_FILE} missing constant for ${action}" >&2
      FAIL=1
      continue
    fi
    count="$(grep -c "\b${name}\b" "${ACTIONS_FILE}" || true)"
    if [[ "${count}" -lt 2 ]]; then
      echo "ERROR: ${name} must appear in const and KnownActions (got ${count})" >&2
      FAIL=1
    else
      echo "    OK: ${action} -> ${name}"
    fi
  done
fi

echo "==> ENT-01: M2M switch consistency"
if ! grep -q 'identity.m2m.allowed_scopes is required' internal/identity/config/validate.go; then
  echo "ERROR: m2m enabled without allowed_scopes must fail startup (identity-authn.md 21.2.6)" >&2
  FAIL=1
else
  echo "    OK: m2m allowed_scopes required"
fi
if ! grep -q 'identity.m2m.token_ttl must be in' internal/identity/config/validate.go; then
  echo "ERROR: m2m token_ttl range validation missing" >&2
  FAIL=1
else
  echo "    OK: m2m token_ttl range"
fi
if ! grep -q 'unknown permission code' cmd/internal/appconfig/appconfig.go; then
  echo "ERROR: m2m allowed_scopes must be checked against the IAM catalog at assembly" >&2
  FAIL=1
else
  echo "    OK: m2m scopes checked against IAM catalog"
fi

echo "==> ENT-01: M2M disabled routes are not registered (404)"
if ! grep -q 'TestM2MRoutesAbsentWhenDisabled' internal/identity/transport/admin/router_test.go; then
  echo "ERROR: missing 404 route test for identity.m2m.enabled=false" >&2
  FAIL=1
elif command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/identity/transport/admin/ -run TestM2MRoutesAbsentWhenDisabled; then
    echo "ERROR: m2m disabled route test failed" >&2
    FAIL=1
  else
    echo "    OK: m2m routes absent when disabled"
  fi
fi

echo "==> ENT-01: M2M metrics and audit naming"
for metric in identity_m2m_token_total identity_m2m_token_duration_seconds; do
  if ! grep -q "${metric}" internal/identity/observability/metrics.go; then
    echo "ERROR: missing metric ${metric}" >&2
    FAIL=1
  else
    echo "    OK: ${metric}"
  fi
done
for pair in \
  "auth.m2m.token.issued:ActionAuthM2MTokenIssued" \
  "auth.m2m.token.denied:ActionAuthM2MTokenDenied" \
  "auth.m2m.client.created:ActionAuthM2MClientCreated" \
  "auth.m2m.secret.rotated:ActionAuthM2MSecretRotated"; do
  action="${pair%%:*}"
  name="${pair##*:}"
  count="$(grep -c "\b${name}\b" internal/shared/audit/actions.go || true)"
  if [[ "${count}" -lt 2 ]]; then
    echo "ERROR: ${name} must appear in const and KnownActions (got ${count})" >&2
    FAIL=1
  else
    echo "    OK: ${action} -> ${name}"
  fi
done

echo "==> ENT-01: M2M negative cases (wrong secret / scope escalation)"
if command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/identity/app/ -run 'TestM2MUnknownClientRejected|TestM2MScopeEscalationRejected'; then
    echo "ERROR: m2m negative case tests failed" >&2
    FAIL=1
  else
    echo "    OK: m2m negative cases"
  fi
fi

echo "==> ENT-01: SCIM switch consistency"
if ! grep -q 'identity.scim.provider is required' internal/identity/config/validate.go; then
  echo "ERROR: scim enabled without provider must fail startup (identity-authn.md 21.1.7)" >&2
  FAIL=1
else
  echo "    OK: scim provider required"
fi
if ! grep -q 'identity.scim.enabled is only supported on channel=admin' internal/identity/config/validate.go; then
  echo "ERROR: scim must be rejected on biz/c channel" >&2
  FAIL=1
else
  echo "    OK: scim admin-only"
fi
if ! grep -q 'group_role_map requires identity.scim.enabled=true' internal/identity/config/validate.go; then
  echo "ERROR: scim group_role_map must not be configured when disabled" >&2
  FAIL=1
else
  echo "    OK: scim mapping requires enabled"
fi
if ! grep -q 'requires a ScimRoleProvisioner' cmd/internal/identityruntime/runtime.go; then
  echo "ERROR: scim group_role_map without a role provisioner must fail startup (silent no-grant)" >&2
  FAIL=1
else
  echo "    OK: scim mapping requires role provisioner"
fi

echo "==> ENT-01: SCIM disabled routes are not registered (404)"
if ! grep -q 'TestScimRoutesAbsentWhenDisabled' internal/identity/transport/admin/router_test.go; then
  echo "ERROR: missing 404 route test for identity.scim.enabled=false" >&2
  FAIL=1
elif command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/identity/transport/admin/ -run TestScimRoutesAbsentWhenDisabled; then
    echo "ERROR: scim disabled route test failed" >&2
    FAIL=1
  else
    echo "    OK: scim routes absent when disabled"
  fi
fi

echo "==> ENT-01: SCIM token namespace isolation (AC-P16-SCIM-6)"
if command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/identity/transport/scim/ -run TestSCIMRejectsMissingAndJWTBearer; then
    echo "ERROR: scim token namespace isolation test failed" >&2
    FAIL=1
  else
    echo "    OK: jwt cannot call scim api"
  fi
fi

echo "==> ENT-01: SCIM metrics and audit naming"
for metric in identity_scim_request_total identity_scim_request_duration_seconds identity_scim_group_map_total; do
  if ! grep -q "${metric}" internal/identity/observability/metrics.go; then
    echo "ERROR: missing metric ${metric}" >&2
    FAIL=1
  else
    echo "    OK: ${metric}"
  fi
done
for pair in \
  "auth.scim.user.created:ActionAuthScimUserCreated" \
  "auth.scim.user.updated:ActionAuthScimUserUpdated" \
  "auth.scim.user.deactivated:ActionAuthScimUserDeact" \
  "auth.scim.user.deleted:ActionAuthScimUserDeleted" \
  "auth.scim.group.updated:ActionAuthScimGroupUpdated" \
  "auth.scim.token.created:ActionAuthScimTokenCreated" \
  "auth.scim.token.revoked:ActionAuthScimTokenRevoked"; do
  action="${pair%%:*}"
  name="${pair##*:}"
  count="$(grep -c "\b${name}\b" internal/shared/audit/actions.go || true)"
  if [[ "${count}" -lt 2 ]]; then
    echo "ERROR: ${name} must appear in const and KnownActions (got ${count})" >&2
    FAIL=1
  else
    echo "    OK: ${action} -> ${name}"
  fi
done

echo "==> ENT-01: SCIM negative cases (duplicate externalId / forged token)"
if command -v go >/dev/null 2>&1; then
  if ! go test -count=1 ./internal/identity/app/ -run 'TestScimCreateUserRejectsDuplicateExternalID|TestScimAuthenticateRejectsRevokedAndUnknownToken'; then
    echo "ERROR: scim negative case tests failed" >&2
    FAIL=1
  else
    echo "    OK: scim negative cases"
  fi
fi

echo "==> ENT-01: no hardcoded secrets in example configs"
shopt -s nullglob
for file in configs/*.yaml.example; do
  while IFS= read -r entry; do
    [[ -z "${entry}" ]] && continue
    value="${entry#*:}"
    value="$(echo "${value}" | sed -E "s/^[[:space:]]+//; s/[[:space:]]+$//; s/^[\"']//; s/[\"']$//")"
    [[ -z "${value}" ]] && continue
    if echo "${value}" | grep -qiE 'changeme|placeholder|your-|example|<[^>]+>|\$\{'; then
      continue
    fi
    echo "ERROR: ${file} contains hardcoded secret: ${entry}" >&2
    FAIL=1
  done < <(grep -E '^[[:space:]]*(secret_key|client_secret)[[:space:]]*:' "${file}" || true)
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "authn check failed (ENT-01)" >&2
  exit 1
fi
echo "OK: authn"
