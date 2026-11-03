#!/usr/bin/env bash
# 在 kind 里部署单体 Grafana，用于验证 deploy/observability/grafana/dashboards 下的
# 看板能被加载且面板查询能出数（ENT-06 AC-ENT-06-3）。
#
# 用法：
#   bash scripts/observability/install-kind-grafana.sh
#   curl -s http://127.0.0.1:13000/api/search | head
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

NS="${GRAFANA_NS:-monitoring}"
STANDALONE_DIR="deploy/observability/grafana/standalone"
DASHBOARD_DIR="deploy/observability/grafana/dashboards"

command -v kubectl >/dev/null 2>&1 || { echo "ERROR: kubectl not found" >&2; exit 1; }

kubectl get namespace "${NS}" >/dev/null 2>&1 || kubectl create namespace "${NS}"

# 看板 ConfigMap：直接来自入库的看板 JSON，保证「验证对象」与仓库内容同源。
kubectl -n "${NS}" create configmap grafana-standalone-dashboards \
  --from-file="${DASHBOARD_DIR}" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f "${STANDALONE_DIR}/grafana-standalone.yaml"

echo "==> waiting for grafana-standalone"
kubectl -n "${NS}" rollout status deploy/grafana-standalone --timeout=180s

echo "OK: grafana-standalone deployed in ${NS}"
