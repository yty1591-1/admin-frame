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
UP="migrations/000004_create_async_outbox.up.sql"
DOWN="migrations/000004_create_async_outbox.down.sql"
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

echo "==> ENT-03: async config keys must be declared in platform key spec"
# M3：configs/ 样例已删除；契约改查代码 key spec（CFG-07）。
SPEC="internal/configcenter/spec/platform.go"
if [[ ! -f "${SPEC}" ]]; then
  echo "ERROR: missing ${SPEC} (ENT-03)" >&2
  FAIL=1
else
  for key in 'async.driver' 'async.outbox.publish_in_api' 'async.outbox.lease' \
    'async.outbox.max_attempts' 'async.consumer.visibility_timeout'; do
    if ! grep -q "${key}" "${SPEC}"; then
      echo "ERROR: ${SPEC} missing async key ${key} (ENT-03)" >&2
      FAIL=1
    fi
  done
fi
if [[ "${FAIL}" -eq 0 ]]; then
  echo "    OK: async config keys declared in key spec"
fi

echo "==> ENT-03: async driver must reject unimplemented brokers"
CONFIG="internal/platform/config/async_validate.go"
if ! grep -q 'AsyncDriverDB' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must define AsyncDriverDB" >&2
  FAIL=1
elif ! grep -q 'AsyncDriverNATS' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must define AsyncDriverNATS" >&2
  FAIL=1
elif ! grep -q 'is not implemented (only db and nats are supported)' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must reject async.driver other than db and nats" >&2
  FAIL=1
elif ! grep -q 'validateAsyncBroker' "${CONFIG}"; then
  echo "ERROR: ${CONFIG} must validate async.broker when driver=nats" >&2
  FAIL=1
else
  echo "    OK: db and nats drivers accepted, unknown drivers rejected"
fi

echo "==> ENT-03: async metrics must be registered"
METRICS="internal/platform/async/async_metrics.go"
for metric in async_outbox_delivery_total async_outbox_pending async_outbox_lag_seconds async_dlq_depth async_broker_publish_total async_broker_consume_total; do
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
