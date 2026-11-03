#!/usr/bin/env bash
# 在 kind 里部署单体 trace 后端（Jaeger + OTel Collector），用于验证
# 「一次请求的 trace 可从入口追到 DB / 外部依赖」（ENT-06 AC-ENT-06-4）。
#
# 用法：
#   bash scripts/observability/install-kind-otel.sh
#   kubectl -n monitoring port-forward svc/jaeger-query 16686:16686
#
# Collector 配置由 deploy/observability/otel/collector.yaml 派生（不另存副本），
# 只做两处演练必需的替换：
#   1. 导出端 tempo.observability -> jaeger-collector.<ns>（kind 只装了 Jaeger）；
#   2. 采样率 10% -> 100%（否则单条 trace 大概率被丢弃，验证变成看运气）。
# 替换失败会显式退出，避免「配置没改却以为验证过了」。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

NS="${OTEL_NS:-monitoring}"
KIND_CLUSTER="${KIND_CLUSTER:-af-verify}"
COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-otel/opentelemetry-collector-contrib:0.104.0}"
JAEGER_IMAGE="${JAEGER_IMAGE:-jaegertracing/all-in-one:1.57}"
STANDALONE="deploy/observability/otel/standalone/otel-standalone.yaml"
SOURCE_CONFIG="deploy/observability/otel/collector.yaml"

log() { printf '[otel] %s\n' "$*"; }

# 临时文件放仓库内 tmp_otel/（.gitignore 已忽略 /tmp*）：
# Windows 下 mktemp 返回带盘符路径，沙箱拒绝删除；跨盘 /tmp 也会落到 D:\tmp。
WORK_DIR="${ROOT}/tmp_otel"
mkdir -p "${WORK_DIR}"
TMP_CONFIG="${WORK_DIR}/collector.yaml"
trap 'rm -rf "${WORK_DIR}"' EXIT

command -v kubectl >/dev/null 2>&1 || { echo "ERROR: kubectl not found" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker not found" >&2; exit 1; }

kubectl get namespace "${NS}" >/dev/null 2>&1 || kubectl create namespace "${NS}"

# ensure_image 把本地镜像导入 kind 节点：多平台 manifest 会让 kind load 失败，
# 故统一走 docker save + ctr import。
ensure_image() {
  local image="$1"
  local node
  node="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
  if docker exec "${node}" ctr -n k8s.io images ls 2>/dev/null | awk '{print $1}' | grep -qx "${image}"; then
    log "image present: ${image}"
    return 0
  fi
  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    log "pulling ${image}"
    docker pull "${image}" >/dev/null
  fi
  local tar
  tar="${WORK_DIR}/image.tar"
  log "importing ${image} into ${node}"
  docker save "${image}" -o "${tar}"
  # 必须 cd 到文件所在目录再传相对名：MSYS2_ARG_CONV_EXCL='*' 会关闭路径转换，
  # 传 POSIX 绝对路径（/d/...）会被 docker 当成 "\d\..." 导致 GetFileAttributesEx 失败。
  ( cd "${WORK_DIR}" && MSYS2_ARG_CONV_EXCL='*' docker cp image.tar "${node}:/root/import.tar" )
  MSYS2_ARG_CONV_EXCL='*' docker exec "${node}" ctr -n k8s.io images import /root/import.tar >/dev/null
  MSYS2_ARG_CONV_EXCL='*' docker exec "${node}" rm -f /root/import.tar
  rm -f "${tar}"
}

ensure_image "${COLLECTOR_IMAGE}"
ensure_image "${JAEGER_IMAGE}"

log "生成 collector 配置（导出端 -> jaeger-collector.${NS}，采样 100%）"
sed \
  -e "s#tempo\.observability\.svc\.cluster\.local#jaeger-collector.${NS}.svc.cluster.local#g" \
  -e "s#jaeger-collector\.observability\.svc\.cluster\.local#jaeger-collector.${NS}.svc.cluster.local#g" \
  -e 's#exporters: \[otlp/tempo\]#exporters: [otlp/jaeger]#' \
  -e 's#sampling_percentage: 10#sampling_percentage: 100#' \
  "${SOURCE_CONFIG}" > "${TMP_CONFIG}"

for want in "jaeger-collector.${NS}.svc.cluster.local:4317" "exporters: [otlp/jaeger]" "sampling_percentage: 100"; do
  if ! grep -qF "${want}" "${TMP_CONFIG}"; then
    echo "ERROR: collector 配置替换未生效（缺少 ${want}），请检查 ${SOURCE_CONFIG}" >&2
    exit 1
  fi
done

kubectl -n "${NS}" create configmap otel-collector-config \
  --from-file=collector.yaml="${TMP_CONFIG}" --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "${NS}" apply -f "${STANDALONE}"

# Collector 不热加载配置：ConfigMap 变了也要重启 Pod 才生效。
log "重启 collector 以加载最新配置"
kubectl -n "${NS}" rollout restart deploy/otel-collector >/dev/null

log "等待 jaeger 与 collector 就绪"
kubectl -n "${NS}" rollout status deploy/jaeger --timeout=180s
kubectl -n "${NS}" rollout status deploy/otel-collector --timeout=180s

cat <<EOF
OK: trace 后端已就绪（namespace=${NS}）
  应用侧配置：
    config.tracing.exporter=otlp
    config.tracing.endpoint=otel-collector.${NS}.svc.cluster.local:4318
  查询 trace：
    kubectl -n ${NS} port-forward svc/jaeger-query 16686:16686
    curl -s 'http://127.0.0.1:16686/api/traces?service=<service>&limit=20'
EOF
