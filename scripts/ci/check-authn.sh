#!/usr/bin/env bash
# ENT-01：身份认证模块门禁（生产配置 / 三端契约 / 撤销路径 / 迁移 / 审计 / 密钥）。
# 用法: bash scripts/ci/check-authn.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-01: production mfa baseline"
if ! grep -q 'identity.mfa.admin_required must be true' internal/platform/config.go; then
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
  if ! go test -count=1 ./internal/platform/ -run 'TestValidateMFA|TestValidateSMS|TestValidateOAuth|TestValidateCChannel'; then
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
  file="migrations/000011_create_identity_tables.${suffix}.sql"
  if [[ ! -f "${file}" ]]; then
    echo "ERROR: missing ${file}" >&2
    FAIL=1
  else
    echo "    OK: ${file}"
  fi
done
MIGRATION_FILES="$(find migrations -maxdepth 1 -type f -name '000011_*' | wc -l | tr -d ' ')"
if [[ "${MIGRATION_FILES}" -ne 2 ]]; then
  echo "ERROR: expected exactly 2 files with 000011_ prefix, got ${MIGRATION_FILES}" >&2
  FAIL=1
else
  echo "    OK: 000011 migration pair only"
fi

echo "==> ENT-01: auth actions registered in audit catalog"
ACTIONS_FILE="internal/shared/audit/actions.go"
if [[ ! -f docs/audit/EVENTS.md || ! -f "${ACTIONS_FILE}" ]]; then
  echo "ERROR: missing docs/audit/EVENTS.md or ${ACTIONS_FILE}" >&2
  FAIL=1
else
  AUTH_ACTIONS="$(grep -oE 'auth\.[a-z._]+' docs/audit/EVENTS.md | sort -u)"
  for action in ${AUTH_ACTIONS}; do
    name="$(grep -oE "Action[A-Za-z0-9]+[[:space:]]*=[[:space:]]*\"${action}\"" "${ACTIONS_FILE}" | head -n1 | sed -E 's/^([A-Za-z0-9]+).*/\1/')"
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
