#!/usr/bin/env bash
# ENT-03：通用异步基础设施（信封 / Outbox / Relay / 指标 / 迁移 / 配置）。
# 用法: bash scripts/ci/check-outbox.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-03: pkg/outbox and pkg/mq must exist"
for file in \
  pkg/outbox/doc.go \
  pkg/outbox/event.go \
  pkg/outbox/policy.go \
  pkg/outbox/store.go \
  pkg/outbox/relay.go \
  pkg/outbox/router.go \
  pkg/outbox/dedupe.go \
  pkg/outbox/mysql.go \
  pkg/outbox/memory.go \
  pkg/mq/envelope.go \
  pkg/mq/broker.go; do
  if [[ ! -f "${file}" ]]; then
    echo "ERROR: missing ${file}" >&2
    FAIL=1
  fi
done
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: async packages present"
fi

echo "==> ENT-03: pkg must not depend on internal"
HITS="$(grep -RIn --include='*.go' 'admin-frame/internal/' pkg/outbox pkg/mq 2>/dev/null || true)"
if [[ -n "${HITS}" ]]; then
  echo "ERROR: pkg/outbox or pkg/mq imports internal (layering violation):" >&2
  printf '%s\n' "${HITS}" >&2
  FAIL=1
else
  echo "    OK: no internal dependency"
fi

echo "==> ENT-03: envelope must carry required fields"
ENVELOPE="pkg/mq/envelope.go"
for field in EventID EventType Version TenantID OccurredAt TraceID IdempotencyKey Payload; do
  if ! grep -q "${field}" "${ENVELOPE}"; then
    echo "ERROR: ${ENVELOPE} missing ${field}" >&2
    FAIL=1
  fi
done
if ! grep -q 'MaxEventPayloadBytes' "${ENVELOPE}"; then
  echo "ERROR: ${ENVELOPE} must bound payload size (MaxEventPayloadBytes)" >&2
  FAIL=1
fi
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: envelope fields bounded"
fi

echo "==> ENT-03: migration must create async tables"
UP="migrations/000009_create_async_outbox.up.sql"
DOWN="migrations/000009_create_async_outbox.down.sql"
for file in "${UP}" "${DOWN}"; do
  if [[ ! -f "${file}" ]]; then
    echo "ERROR: missing ${file}" >&2
    FAIL=1
    continue
  fi
done
if [[ -f "${UP}" ]]; then
  for table in async_outbox async_dlq async_consumed; do
    if ! grep -q "CREATE TABLE.*${table}" "${UP}"; then
      echo "ERROR: ${UP} must create ${table}" >&2
      FAIL=1
    fi
  done
fi
if [[ -f "${DOWN}" ]]; then
  for table in async_outbox async_dlq async_consumed; do
    if ! grep -q "DROP TABLE IF EXISTS ${table}" "${DOWN}"; then
      echo "ERROR: ${DOWN} must drop ${table}" >&2
      FAIL=1
    fi
  done
fi

echo "==> ENT-03: async config samples must be complete"
for sample in configs/admin.yaml.example configs/biz.yaml.example configs/c.yaml.example configs/worker.yaml.example; do
  if [[ ! -f "${sample}" ]]; then
    echo "ERROR: missing ${sample}" >&2
    FAIL=1
    continue
  fi
  if ! grep -qE '^async:' "${sample}"; then
    echo "ERROR: ${sample} must declare async section (ENT-03)" >&2
    FAIL=1
    continue
  fi
  for key in 'driver:' 'publish_in_api:' 'lease:' 'max_attempts:' 'visibility_timeout:'; do
    if ! grep -q "${key}" "${sample}"; then
      echo "ERROR: ${sample} missing async key ${key}" >&2
      FAIL=1
    fi
  done
done
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: async config samples complete"
fi

echo "==> ENT-03: async driver must reject unimplemented brokers"
CONFIG="internal/platform/config.go"
if ! grep -q 'AsyncDriverDB' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must define AsyncDriverDB" >&2
  FAIL=1
elif ! grep -q 'is not implemented (only db is supported)' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must reject async.driver other than db" >&2
  FAIL=1
else
  echo "    OK: only db driver accepted"
fi

echo "==> ENT-03: async metrics must be registered"
METRICS="internal/platform/async_metrics.go"
for metric in async_outbox_delivery_total async_outbox_pending async_outbox_lag_seconds async_dlq_depth; do
  if ! grep -q "${metric}" "${METRICS}"; then
    echo "ERROR: ${METRICS} missing metric ${metric}" >&2
    FAIL=1
  fi
done
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: async metrics declared"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "outbox check failed (ENT-03)" >&2
  exit 1
fi
echo "OK: outbox"
