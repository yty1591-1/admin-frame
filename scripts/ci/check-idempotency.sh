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
# 配置按端拆分后：端样例入库为 configs/prod/<channel>.yaml.example，本地运行配置
# configs/prod-<channel>.yaml 被 .gitignore 排除，仅存在本地，CI 不得依赖。
# 公共段（id / idempotency / async）收敛到 configs/prod/modules/platform.yaml.example。
for sample in configs/prod/admin.yaml.example configs/prod/biz.yaml.example configs/prod/c.yaml.example configs/prod/worker.yaml.example; do
  if [[ ! -f "${sample}" ]]; then
    echo "ERROR: missing ${sample} (ENT-02)" >&2
    FAIL=1
  fi
done
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
