#!/usr/bin/env bash
# ENT-02：幂等平台级挂载、配置契约与节点号唯一性约束。
# 用法: bash scripts/ci/check-idempotency.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-02: pipeline must mount idempotency middleware"
PIPELINE="internal/platform/httpserver/pipeline.go"
if [[ ! -f "${PIPELINE}" ]]; then
  echo "ERROR: missing ${PIPELINE}" >&2
  FAIL=1
elif ! grep -q 'idempotency.Middleware' "${PIPELINE}"; then
  echo "ERROR: ${PIPELINE} must mount idempotency.Middleware (ENT-02)" >&2
  FAIL=1
else
  echo "    OK: idempotency middleware mounted"
fi

echo "==> ENT-02: platform sample must declare id and idempotency"
# M1 已删除非密钥端样例；公共段仍留在 platform.yaml.example（密钥托管声明一并保留至 M2）。
PLATFORM_SAMPLE="configs/prod/modules/platform.yaml.example"
if [[ ! -f "${PLATFORM_SAMPLE}" ]]; then
  echo "ERROR: missing ${PLATFORM_SAMPLE} (ENT-02)" >&2
  FAIL=1
else
  if ! grep -qE '^id:' "${PLATFORM_SAMPLE}" || ! grep -q 'node_id_source' "${PLATFORM_SAMPLE}"; then
    echo "ERROR: ${PLATFORM_SAMPLE} must declare id.node_id_source (ENT-02)" >&2
    FAIL=1
  fi
  if ! grep -qE '^idempotency:' "${PLATFORM_SAMPLE}"; then
    echo "ERROR: ${PLATFORM_SAMPLE} must declare idempotency section (ENT-02)" >&2
    FAIL=1
  fi
fi
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: platform sample declares id / idempotency"
fi

echo "==> ENT-02: business code must not hard-code snowflake NodeID"
# 测试与文档除外；固定 NodeID 会让多副本生成重复 ID。
HITS="$(grep -RIn --include='*.go' -E 'NodeID:[[:space:]]*[0-9]+' cmd internal 2>/dev/null \
  | grep -v -E '_test\.go:' \
  || true)"
if [[ -n "${HITS}" ]]; then
  echo "ERROR: hard-coded NodeID found in non-test business code (ENT-02):" >&2
  printf '%s\n' "${HITS}" >&2
  FAIL=1
else
  echo "    OK: no hard-coded NodeID outside tests"
fi

echo "==> ENT-02: OpenAPI components must define Idempotency-Key"
COMPONENTS="api/openapi/components.yaml"
if [[ ! -f "${COMPONENTS}" ]]; then
  echo "ERROR: missing ${COMPONENTS}" >&2
  FAIL=1
elif ! grep -q 'Idempotency-Key' "${COMPONENTS}"; then
  echo "ERROR: ${COMPONENTS} must define Idempotency-Key header (ENT-02)" >&2
  FAIL=1
else
  echo "    OK: Idempotency-Key defined"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "idempotency check failed (ENT-02)" >&2
  exit 1
fi
echo "OK: idempotency"
