#!/usr/bin/env bash
# OBSERVABILITY_STANDARDS：日志基础字段、healthz/readyz/version 挂载。
# 用法: bash scripts/ci/check-observability.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> OBS-01: NewLogger must bind service, channel, env"
LOGGER="internal/platform/logger.go"
if [[ ! -f "${LOGGER}" ]]; then
  echo "ERROR: missing ${LOGGER}" >&2
  FAIL=1
else
  for field in service channel env; do
    if ! grep -q "\"${field}\"" "${LOGGER}"; then
      echo "ERROR: ${LOGGER} must With(\"${field}\", ...) (OBS-01)" >&2
      FAIL=1
    else
      echo "    OK: logger field ${field}"
    fi
  done
fi

echo "==> OBS-01: AccessLog must emit trace_id"
MW="internal/platform/middleware.go"
if [[ -f "${MW}" ]] && grep -q '"trace_id"' "${MW}"; then
  echo "    OK: AccessLog trace_id"
else
  echo "ERROR: AccessLog must log trace_id (OBS-01)" >&2
  FAIL=1
fi

echo "==> Tracing: API mains must Init pkg/tracing and mount HTTPMiddleware"
for cmd in admin-api biz-api c-api; do
  main="cmd/${cmd}/main.go"
  if [[ ! -f "${main}" ]]; then
    echo "ERROR: missing ${main}" >&2
    FAIL=1
    continue
  fi
  if ! grep -q 'tracing.Init' "${main}"; then
    echo "ERROR: ${main} missing tracing.Init" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} tracing.Init"
  fi
  if ! grep -q 'tracing.HTTPMiddleware' "${main}"; then
    echo "ERROR: ${main} missing tracing.HTTPMiddleware" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} tracing.HTTPMiddleware"
  fi
done

echo "==> OBS-02/04: API mains must MountHealth, MountVersion, MountMetrics"
for cmd in admin-api biz-api c-api; do
  main="cmd/${cmd}/main.go"
  if [[ ! -f "${main}" ]]; then
    echo "ERROR: missing ${main}" >&2
    FAIL=1
    continue
  fi
  if ! grep -q 'MountHealth' "${main}"; then
    echo "ERROR: ${main} missing MountHealth (OBS-02)" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} MountHealth"
  fi
  if ! grep -q 'MountVersion' "${main}"; then
    echo "ERROR: ${main} missing MountVersion (OBS-04)" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} MountVersion"
  fi
  if ! grep -q 'MountMetrics' "${main}"; then
    echo "ERROR: ${main} missing MountMetrics (Prometheus scrape)" >&2
    FAIL=1
  else
    echo "    OK: ${cmd} MountMetrics"
  fi
done

echo "==> OBS-02: wiring registers /healthz, /readyz, /metrics"
WIRING="internal/platform/wiring.go"
if [[ -f "${WIRING}" ]] \
  && grep -q '/healthz' "${WIRING}" \
  && grep -q '/readyz' "${WIRING}"; then
  echo "    OK: healthz/readyz routes"
else
  echo "ERROR: ${WIRING} must register /healthz and /readyz (OBS-02)" >&2
  FAIL=1
fi
if [[ -f "${WIRING}" ]] && grep -q '/metrics' "${WIRING}"; then
  echo "    OK: metrics route"
else
  echo "ERROR: ${WIRING} must register /metrics" >&2
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

if [[ "${FAIL}" -ne 0 ]]; then
  echo "observability check failed (OBSERVABILITY_STANDARDS)" >&2
  exit 1
fi
echo "OK: observability"
