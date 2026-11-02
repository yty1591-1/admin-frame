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
        servicemonitor.yaml migrate-job.yaml migrate-config.yaml deployment-admin.yaml \
        deployment-biz.yaml deployment-c.yaml deployment-worker.yaml NOTES.txt; do
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

echo "==> ENT-05: 迁移必须先于 API 启动（T7）"
MIGRATE_JOB="${CHART}/templates/migrate-job.yaml"
# 必须是 pre-* hook。post-* 会让 API Pod 与迁移 Pod 并行启动，新代码在旧 schema
# 上运行（2026-11-02 kind 实测：改前 4 个 API Pod 与迁移 Pod 同一秒创建）。
if ! grep -q 'helm.sh/hook: pre-install,pre-upgrade' "${MIGRATE_JOB}"; then
  echo "ERROR: migrate Job must be a pre-install,pre-upgrade hook (T7)" >&2
  FAIL=1
fi
if grep -q 'helm.sh/hook: post-' "${MIGRATE_JOB}"; then
  echo "ERROR: migrate Job must not use post-* hooks: API would start before the schema is ready" >&2
  FAIL=1
fi
# pre-install 阶段 release 资源尚未创建，迁移依赖的 ConfigMap 与 ServiceAccount
# 必须同为 pre-* hook，且 weight 严格早于 Job(0)。缺任一项 Job 会卡在
# "Running 但无 Pod"（serviceaccount not found）。
for f in migrate-config.yaml serviceaccount.yaml; do
  if [[ ! -f "${CHART}/templates/${f}" ]]; then
    echo "ERROR: missing ${CHART}/templates/${f} (required by the pre-install migrate Job)" >&2
    FAIL=1
  elif ! grep -q 'helm.sh/hook: pre-install,pre-upgrade' "${CHART}/templates/${f}"; then
    echo "ERROR: ${f} must be a pre-install,pre-upgrade hook: the migrate Job runs before release resources exist" >&2
    FAIL=1
  fi
done
if ! grep -q 'hook-weight: "-20"' "${CHART}/templates/serviceaccount.yaml"; then
  echo "ERROR: serviceaccount hook-weight must be -20 (earlier than migrate-config -10 and job 0)" >&2
  FAIL=1
fi
if ! grep -q 'hook-weight: "-10"' "${CHART}/templates/migrate-config.yaml"; then
  echo "ERROR: migrate-config hook-weight must be -10 (earlier than job 0)" >&2
  FAIL=1
fi

echo "==> ENT-05: ServiceMonitor 必须显式处理 CRD 缺失"
SM="${CHART}/templates/servicemonitor.yaml"
if ! grep -q 'Capabilities.APIVersions.Has "monitoring.coreos.com/v1"' "${SM}"; then
  echo "ERROR: servicemonitor.yaml must check the Prometheus Operator CRD; otherwise helm install fails with the opaque 'no matches for kind ServiceMonitor'" >&2
  FAIL=1
fi
if ! grep -q '{{- fail ' "${SM}"; then
  echo "ERROR: servicemonitor.yaml must fail with actionable guidance when the CRD is absent" >&2
  FAIL=1
fi

echo "==> ENT-05: YAML 布尔陷阱（裸 off 会被解析成 false）"
# YAML 1.1 把裸 off 解析为布尔 false，渲染后是 iam.mode: false，容器启动即
# "unsupported iam.mode: false" 并 CrashLoop；helm lint 与 server-side dry-run
# 都发现不了（属运行时配置校验）。2026-11-02 kind 实测：c 端与 worker 均崩溃。
if grep -rnE '^[[:space:]]*mode:[[:space:]]+off[[:space:]]*$' "${CHART}"/values*.yaml; then
  echo "ERROR: bare 'mode: off' is parsed as boolean false by YAML 1.1; quote it as \"off\"" >&2
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
  # --api-versions 声明「渲染目标集群已安装 Prometheus Operator」：
  # staging/prod 的 serviceMonitor.enabled=true 依赖 monitoring.coreos.com/v1，
  # 而 helm template 的离线 Capabilities 不含任何 CRD。不声明会让模板里的
  # CRD 检查（templates/servicemonitor.yaml）判定为「集群缺 Operator」并 fail。
  # 生产集群本来就要先装 Operator，故这里按「已装」渲染；「未装时报错友好」
  # 由 kind 上的 server-side dry-run 单独验证。
  RENDER_API_VERSIONS="monitoring.coreos.com/v1"
  for env in dev staging prod; do
    out="$(mktemp)"
    if ! "${HELM_BIN}" template admin-frame "${CHART}" -f "${CHART}/values-${env}.yaml" \
      --api-versions "${RENDER_API_VERSIONS}" >"${out}" 2>&1; then
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
