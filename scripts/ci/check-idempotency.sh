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

echo "==> ENT-02: platform key spec must declare id and idempotency"
# M3：configs/ 样例已删除；契约改查代码 key spec。
SPEC="internal/configcenter/spec/platform.go"
if [[ ! -f "${SPEC}" ]]; then
  echo "ERROR: missing ${SPEC} (ENT-02)" >&2
  FAIL=1
else
  if ! grep -q 'id.node_id_source' "${SPEC}"; then
    echo "ERROR: ${SPEC} must declare id.node_id_source (ENT-02)" >&2
    FAIL=1
  fi
  if ! grep -q 'idempotency.enabled' "${SPEC}"; then
    echo "ERROR: ${SPEC} must declare idempotency keys (ENT-02)" >&2
    FAIL=1
  fi
fi
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: platform key spec declares id / idempotency"
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
