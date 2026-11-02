#!/usr/bin/env bash
# 在 kind（或任何未装 Prometheus Operator 的集群）里部署单体 Prometheus，
# 用于验证 deploy/observability/prometheus/rules 下的告警规则能被真实指标触发。
#
# 背景：ENT-06 T6 要求「5xx / DB 池耗尽 / Outbox 积压」三类故障能触发对应告警。
# 生产用 Operator + ServiceMonitor（Chart 已渲染），但 kind 上没有 Operator，
# 这里用单体 Prometheus + pod role 发现达到同一目的：规则文件完全一致。
#
# 用法：
#   bash scripts/observability/install-kind-prometheus.sh
#   kubectl -n monitoring port-forward svc/prometheus-standalone 9090:9090
#   curl -s 'http://127.0.0.1:9090/api/v1/rules' | head
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

NS="${PROM_NS:-monitoring}"
STANDALONE_DIR="deploy/observability/prometheus/standalone"
RULES_DIR="deploy/observability/prometheus/rules"

command -v kubectl >/dev/null 2>&1 || { echo "ERROR: kubectl not found" >&2; exit 1; }

kubectl get namespace "${NS}" >/dev/null 2>&1 || kubectl create namespace "${NS}"

# 规则 ConfigMap：直接从入库的规则文件生成，保证验证对象与仓库内容同源。
kubectl -n "${NS}" create configmap prometheus-standalone-rules \
  --from-file="${RULES_DIR}" --dry-run=client -o yaml | kubectl apply -f -

# 抓取配置 ConfigMap。
kubectl -n "${NS}" create configmap prometheus-standalone-config \
  --from-file="${STANDALONE_DIR}/prometheus.yml" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f "${STANDALONE_DIR}/prometheus-standalone.yaml"

echo "==> waiting for prometheus-standalone"
kubectl -n "${NS}" rollout status deploy/prometheus-standalone --timeout=180s

echo "OK: prometheus-standalone deployed in ${NS}"
