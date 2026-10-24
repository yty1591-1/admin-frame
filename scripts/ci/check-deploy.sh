#!/usr/bin/env bash
# ENT-05：Helm Chart 结构、渲染、明文密钥与 digest 门禁。
# 用法: bash scripts/ci/check-deploy.sh
# 说明: helm 缺失时只做静态检查（跳过渲染），CI deploy job 必须安装 helm。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
CHART="deploy/helm/admin-frame"

echo "==> ENT-05: chart skeleton"
for f in Chart.yaml values.yaml values-dev.yaml values-staging.yaml values-prod.yaml; do
  if [[ ! -f "${CHART}/${f}" ]]; then
    echo "ERROR: missing ${CHART}/${f} (AC-ENT-05-1)" >&2
    FAIL=1
  fi
done
for t in _helpers.tpl _workload.tpl configmap.yaml serviceaccount.yaml service.yaml \
        service-headless.yaml ingress.yaml hpa.yaml pdb.yaml networkpolicy.yaml \
        servicemonitor.yaml migrate-job.yaml deployment-admin.yaml deployment-biz.yaml \
        deployment-c.yaml deployment-worker.yaml; do
  if [[ ! -f "${CHART}/templates/${t}" ]]; then
    echo "ERROR: missing ${CHART}/templates/${t} (AC-ENT-05-2)" >&2
    FAIL=1
  fi
done

echo "==> ENT-05: workload hardening (probes / resources / securityContext)"
WORKLOAD="${CHART}/templates/_workload.tpl"
for key in "startupProbe" "livenessProbe" "readinessProbe" "resources:" "securityContext:" "topologySpreadConstraints"; do
  if ! grep -q "${key}" "${WORKLOAD}"; then
    echo "ERROR: ${WORKLOAD} missing ${key} (AC-ENT-05-2)" >&2
    FAIL=1
  fi
done

echo "==> ENT-05: migrate image support"
if ! grep -q 'FROM runtime AS migrate' Dockerfile; then
  echo "ERROR: Dockerfile must define migrate target (migrate-job.yaml)" >&2
  FAIL=1
fi
if ! grep -q 'COPY migrations /app/migrations' Dockerfile; then
  echo "ERROR: Dockerfile must copy migrations for migrate Job" >&2
  FAIL=1
fi

echo "==> ENT-05: no plaintext secrets in values"
for f in "${CHART}"/values*.yaml; do
  # 只拦截「未加引号的非空值」；空字符串（""）与 Secret 引用允许。
  if grep -nE '^[[:space:]]*(password|dsn|secret_key|access_key|token):[[:space:]]*[[:alnum:]]' "${f}"; then
    echo "ERROR: plaintext secret-like value in ${f} (AC-ENT-05-3)" >&2
    FAIL=1
  fi
done
if grep -RqE 'ADMIN_FRAME_DB_DSN\s*[:=]\s*[^ ]' "${CHART}/values-prod.yaml"; then
  echo "ERROR: values-prod.yaml must reference secrets, not inline DSN" >&2
  FAIL=1
fi

echo "==> ENT-05: prod image must use digest"
if ! grep -qE '^\s*digest:\s*"sha256:[0-9a-f]{64}"' "${CHART}/values-prod.yaml"; then
  echo "ERROR: values-prod.yaml must pin image.digest (sha256:...)" >&2
  FAIL=1
fi
if grep -qE '^\s*tag:\s*["]?latest["]?\s*$' "${CHART}"/values*.yaml; then
  echo "ERROR: floating latest tag is forbidden (RELS-01)" >&2
  FAIL=1
fi

echo "==> ENT-05: helm render"
HELM_BIN="${HELM_BIN:-helm}"
if command -v "${HELM_BIN}" >/dev/null 2>&1; then
  "${HELM_BIN}" lint "${CHART}"
  for env in dev staging prod; do
    out="$(mktemp)"
    if ! "${HELM_BIN}" template admin-frame "${CHART}" -f "${CHART}/values-${env}.yaml" >"${out}" 2>&1; then
      echo "ERROR: helm template values-${env}.yaml failed:" >&2
      cat "${out}" >&2
      FAIL=1
    else
      echo "    OK: values-${env}.yaml rendered"
      if grep -qE '^\s+image: .*:latest$' "${out}"; then
        echo "ERROR: rendered manifest contains :latest image" >&2
        FAIL=1
      fi
    fi
    rm -f "${out}"
  done
else
  echo "    SKIP: helm not installed; CI deploy job must run helm template"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "deploy check failed (ENT-05)" >&2
  exit 1
fi
echo "OK: deploy"
