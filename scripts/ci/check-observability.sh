#!/usr/bin/env bash
# OBSERVABILITY_STANDARDS：日志基础字段、healthz/readyz/version 挂载。
# 用法: bash scripts/ci/check-observability.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> OBS-01: pkg/logger must bind service, channel, env"
LOGGER_PKG="pkg/logger"
if [[ ! -d "${LOGGER_PKG}" ]]; then
  echo "ERROR: missing ${LOGGER_PKG}" >&2
  FAIL=1
else
  for field in service channel env; do
    if ! grep -R -q "\"${field}\"" "${LOGGER_PKG}"; then
      echo "ERROR: ${LOGGER_PKG} must define field \"${field}\" (OBS-01)" >&2
      FAIL=1
    else
      echo "    OK: logger field ${field}"
    fi
  done
  if ! grep -q 'pkg/logger' internal/platform/logger.go; then
    echo "ERROR: platform.NewLogger must delegate to pkg/logger" >&2
    FAIL=1
  else
    echo "    OK: platform delegates to pkg/logger"
  fi
fi

echo "==> OBS-01: AccessLog must emit trace_id"
if grep -Rq '"trace_id"' pkg/middleware internal/platform/middleware.go 2>/dev/null; then
  echo "    OK: AccessLog trace_id"
else
  echo "ERROR: AccessLog must log trace_id (OBS-01)" >&2
  FAIL=1
fi

echo "==> Tracing: platform bootstrap must Init pkg/tracing and mount HTTPMiddleware"
BOOTSTRAP="internal/platform/bootstrap.go"
PIPELINE="internal/platform/pipeline.go"
if grep -q 'tracing.Init' "${BOOTSTRAP}"; then
  echo "    OK: bootstrap tracing.Init"
else
  echo "ERROR: ${BOOTSTRAP} missing tracing.Init" >&2
  FAIL=1
fi
if grep -q 'tracing.HTTPMiddleware' "${PIPELINE}"; then
  echo "    OK: pipeline tracing.HTTPMiddleware"
else
  echo "ERROR: ${PIPELINE} missing tracing.HTTPMiddleware" >&2
  FAIL=1
fi
for cmd in admin-api biz-api c-api; do
  main="cmd/${cmd}/main.go"
  if [[ ! -f "${main}" ]]; then
    echo "ERROR: missing ${main}" >&2
    FAIL=1
    continue
  fi
  if ! grep -q 'platform.Run' "${main}"; then
    echo "ERROR: ${main} must delegate lifecycle to platform.Run (PLAT-08)" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} platform.Run"
  fi
done

echo "==> OBS-02/04: platform bootstrap must MountHealth, MountVersion, MountMetrics"
for symbol in MountHealth MountVersion MountMetrics; do
  if ! grep -q "${symbol}" "${BOOTSTRAP}"; then
    echo "ERROR: ${BOOTSTRAP} missing ${symbol}" >&2
    FAIL=1
  else
    echo "    OK: bootstrap ${symbol}"
  fi
done

echo "==> OBS-02: endpoints registers /healthz, /readyz, /metrics"
ENDPOINTS="internal/platform/endpoints.go"
if [[ -f "${ENDPOINTS}" ]] \
  && grep -q '/healthz' "${ENDPOINTS}" \
  && grep -q '/readyz' "${ENDPOINTS}"; then
  echo "    OK: healthz/readyz routes"
else
  echo "ERROR: ${ENDPOINTS} must register /healthz and /readyz (OBS-02)" >&2
  FAIL=1
fi
if [[ -f "${ENDPOINTS}" ]] && grep -q '/metrics' "${ENDPOINTS}"; then
  echo "    OK: metrics route"
else
  echo "ERROR: ${ENDPOINTS} must register /metrics" >&2
  FAIL=1
fi

echo "==> OBS-02: OpenAPI probes present on all channels"
for ch in admin biz c; do
  spec="api/openapi/${ch}.yaml"
  if [[ ! -f "${spec}" ]]; then
    echo "ERROR: missing ${spec}" >&2
    FAIL=1
    continue
  fi
  for path in /healthz /readyz /version; do
    if ! grep -q "${path}" "${spec}"; then
      echo "ERROR: ${spec} missing ${path} (OBS-02/04)" >&2
      FAIL=1
    fi
  done
  echo "    OK: ${ch}.yaml probes"
done

echo "==> OBS-07: dashboard metric names must exist in code or rule files"
if python3 scripts/ci/dashboard_metrics_check.py; then
  echo "    OK: dashboard metrics defined"
else
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "observability check failed (OBSERVABILITY_STANDARDS)" >&2
  exit 1
fi
echo "OK: observability"
