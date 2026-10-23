#!/usr/bin/env bash
# ENT-02：幂等平台级挂载、配置契约与节点号唯一性约束。
# 用法: bash scripts/ci/check-idempotency.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-02: pipeline must mount idempotency middleware"
PIPELINE="internal/platform/pipeline.go"
if [[ ! -f "${PIPELINE}" ]]; then
  echo "ERROR: missing ${PIPELINE}" >&2
  FAIL=1
elif ! grep -q 'idempotency.Middleware' "${PIPELINE}"; then
  echo "ERROR: ${PIPELINE} must mount idempotency.Middleware (ENT-02)" >&2
  FAIL=1
else
  echo "    OK: idempotency middleware mounted"
fi

echo "==> ENT-02: config samples must declare id and idempotency"
for sample in configs/admin.yaml.example configs/biz.yaml.example configs/c.yaml.example configs/worker.yaml.example; do
  if [[ ! -f "${sample}" ]]; then
    echo "ERROR: missing ${sample}" >&2
    FAIL=1
    continue
  fi
  if ! grep -qE '^id:' "${sample}" || ! grep -q 'node_id_source' "${sample}"; then
    echo "ERROR: ${sample} must declare id.node_id_source (ENT-02)" >&2
    FAIL=1
  fi
  if ! grep -qE '^idempotency:' "${sample}"; then
    echo "ERROR: ${sample} must declare idempotency section (ENT-02)" >&2
    FAIL=1
  fi
done
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: config samples declare id / idempotency"
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
