#!/usr/bin/env bash
# RELEASE_STANDARDS：Dockerfile 禁 latest、HEALTHCHECK、版本注入、基础 OS 支持期与 Checklist。
# 用法: bash scripts/ci/check-release.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
DF="Dockerfile"

echo "==> RELS-01: Dockerfile must not use :latest"
if [[ ! -f "${DF}" ]]; then
  echo "ERROR: missing ${DF}" >&2
  exit 1
fi
bad_latest=0
while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  content="${line#*:}"
  trimmed="${content#"${content%%[![:space:]]*}"}"
  if [[ "${trimmed}" == \#* ]]; then
    continue
  fi
  echo "ERROR: :latest in Dockerfile (RELS-01): ${line}" >&2
  bad_latest=1
done <<< "$(grep -nE ':latest' "${DF}" || true)"
if [[ "${bad_latest}" -ne 0 ]]; then
  FAIL=1
else
  echo "    OK: no :latest"
fi

echo "==> RELS-02: HEALTHCHECK present"
if grep -qE '^[[:space:]]*HEALTHCHECK' "${DF}"; then
  echo "    OK: HEALTHCHECK"
else
  echo "ERROR: Dockerfile missing HEALTHCHECK (RELS-02)" >&2
  FAIL=1
fi
if ! grep -q 'healthz' "${DF}"; then
  echo "ERROR: HEALTHCHECK should target /healthz (RELS-02)" >&2
  FAIL=1
else
  echo "    OK: healthz referenced"
fi

# RELS-02/§3：运行镜像基础 OS 必须在支持期内。
# 过期的 Alpine 会让 Trivy 只警告而不提供 OS 漏洞数据（"detection may be insufficient"），
# 扫描结果变成假绿。升级基础镜像时同步抬高这里的下限。
MIN_ALPINE_MINOR="3.24"
echo "==> RELS-02/§3: runtime base OS must be supported (alpine >= ${MIN_ALPINE_MINOR})"
runtime_from="$(grep -nE '^[[:space:]]*FROM[[:space:]]' "${DF}" | grep -iE 'AS[[:space:]]+runtime[[:space:]]*$' | tail -n 1 || true)"
if [[ -z "${runtime_from}" ]]; then
  # 兜底：最后一条引用外部镜像（带 tag 或 registry 路径）的 FROM。
  runtime_from="$(grep -nE '^[[:space:]]*FROM[[:space:]]+[^ ]*[:/]' "${DF}" | grep -viE 'AS[[:space:]]+builder[[:space:]]*$' | tail -n 1 || true)"
fi
if [[ -z "${runtime_from}" ]]; then
  echo "ERROR: cannot locate runtime FROM in ${DF} (RELS-02)" >&2
  FAIL=1
elif [[ "${runtime_from}" =~ alpine:([0-9]+)\.([0-9]+) ]]; then
  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  if (( major > 3 )) || { (( major == 3 )) && (( minor >= ${MIN_ALPINE_MINOR#3.} )); }; then
    echo "    OK: runtime base alpine:${major}.${minor}"
  else
    echo "ERROR: runtime base alpine:${major}.${minor} is EOL/unsupported; use alpine:${MIN_ALPINE_MINOR}+ (RELS-02/§3)" >&2
    FAIL=1
  fi
else
  echo "ERROR: runtime base must pin an alpine major.minor tag (RELS-02/§3; switching distro requires updating this check): ${runtime_from}" >&2
  FAIL=1
fi

echo "==> RELS-03: build version ARG / ldflags wiring"
for arg in VERSION COMMIT BUILD_TIME; do
  if ! grep -q "ARG ${arg}" "${DF}"; then
    echo "ERROR: Dockerfile missing ARG ${arg} (RELS-03)" >&2
    FAIL=1
  else
    echo "    OK: ARG ${arg}"
  fi
done
if ! grep -qE '\-X[[:space:]]*main\.version|main\.version=' "${DF}"; then
  echo "ERROR: Dockerfile should inject -X main.version (RELS-03)" >&2
  FAIL=1
else
  echo "    OK: ldflags version injection"
fi

ENDPOINTS="internal/platform/httpserver/endpoints.go"
if [[ -f "${ENDPOINTS}" ]] && grep -q 'BuildInfo' "${ENDPOINTS}" && grep -q 'build_time' "${ENDPOINTS}"; then
  echo "    OK: /version exposes build info"
else
  echo "ERROR: MountVersion must expose commit/build_time via BuildInfo (RELS-03)" >&2
  FAIL=1
fi

echo "==> RELS-01/03/06: release image pipeline"
IMG_WF=".github/workflows/image.yml"
if [[ ! -f "${IMG_WF}" ]]; then
  echo "ERROR: missing ${IMG_WF} (发版镜像流水线，见 RELEASE_STANDARDS §3/§6)" >&2
  FAIL=1
else
  if grep -nE ':latest' "${IMG_WF}"; then
    echo "ERROR: floating :latest tag in ${IMG_WF} (RELS-01)" >&2
    FAIL=1
  else
    echo "    OK: no :latest push tag"
  fi
  if grep -qE '^[[:space:]]*latest[[:space:]]*[:=][[:space:]]*false' "${IMG_WF}"; then
    echo "    OK: metadata-action latest disabled"
  else
    echo "ERROR: metadata-action must disable implicit latest tag (RELS-01)" >&2
    FAIL=1
  fi
  if grep -q 'aquasecurity/trivy-action' "${IMG_WF}"; then
    echo "    OK: image scan wired"
  else
    echo "ERROR: image pipeline must scan before release (RELS-06 / §6)" >&2
    FAIL=1
  fi
  for arg in VERSION COMMIT BUILD_TIME; do
    if grep -qF "${arg}=\${{ steps.ver.outputs" "${IMG_WF}"; then
      echo "    OK: build arg ${arg} from pipeline"
    else
      echo "ERROR: image pipeline missing ${arg} build arg (RELS-03)" >&2
      FAIL=1
    fi
  done
fi

echo "==> RELS-15: tag builds publish GitHub Release assets"
# workflow artifact 只留 90 天：SBOM / 制品若不进 Release，发版后无据可查。
if grep -qE '^[[:space:]]+contents:[[:space:]]*write' "${IMG_WF}"; then
  echo "    OK: contents: write"
else
  echo "ERROR: ${IMG_WF} 缺 'contents: write'，无法创建 Release（RELS-15）" >&2
  FAIL=1
fi
if grep -qE 'gh release (create|upload)' "${IMG_WF}"; then
  echo "    OK: gh release wired"
else
  echo "ERROR: ${IMG_WF} 未创建 GitHub Release（RELS-15）" >&2
  FAIL=1
fi
# 只在 tag 上产出：主干 push 的 sha- 产物进 Release 会让正式版无从分辨。
if grep -qF 'refs/tags/v' "${IMG_WF}"; then
  echo "    OK: release gated on tag"
else
  echo "ERROR: ${IMG_WF} 的 Release 必须只在 v* tag 上产出（RELS-15）" >&2
  FAIL=1
fi
for asset in 'SHA256SUMS' 'images.json' '.tar.gz' '.cdx.json'; do
  if grep -qF -- "${asset}" "${IMG_WF}"; then
    echo "    OK: asset ${asset}"
  else
    echo "ERROR: Release 资产缺 ${asset}（RELS-15）" >&2
    FAIL=1
  fi
done
# configs/prod/.env 是演练凭据，整目录复制会把凭据随制品外发（SEC-01）。
if grep -qE 'cp[[:space:]]+-r[[:space:]]+configs([[:space:]]|$)' "${IMG_WF}"; then
  echo "ERROR: bundle 不得整目录复制 configs（含 .env 演练凭据，SEC-01）" >&2
  FAIL=1
else
  echo "    OK: bundle only ships config examples"
fi

echo "==> RELS-07: release checklist present"
if [[ -f docs/release/CHECKLIST.md ]]; then
  echo "    OK: docs/release/CHECKLIST.md"
else
  echo "ERROR: missing docs/release/CHECKLIST.md (RELS-07)" >&2
  FAIL=1
fi

if [[ -f docs/RELEASE_STANDARDS.md ]]; then
  echo "    OK: docs/RELEASE_STANDARDS.md"
else
  echo "ERROR: missing docs/RELEASE_STANDARDS.md" >&2
  FAIL=1
fi

# ---- ENT-17：发布流水线、迁移门禁、金丝雀与回滚 ----
REL_WF=".github/workflows/release.yml"
MIG_GATE="scripts/ci/check-migration-gate.sh"
SMOKE="scripts/ci/release-smoke.sh"
CANARY_GATE="scripts/ci/release-canary-gate.sh"
CANARY_VALUES="deploy/helm/admin-frame/values-canary.yaml"

echo "==> RELS-09: release pipeline must gate on migrations before deploy"
if [[ ! -f "${REL_WF}" ]]; then
  echo "ERROR: missing ${REL_WF} (ENT-17)" >&2
  FAIL=1
else
  if [[ ! -f "${MIG_GATE}" ]]; then
    echo "ERROR: missing ${MIG_GATE}" >&2
    FAIL=1
  fi
  # 迁移门禁必须在第一次 helm upgrade 之前：顺序错了等于「先上线再校验迁移」。
  # 只取**非注释行**：文件头与步骤注释里提到 `helm upgrade` 会把行号拉到最前面，
  # 让「门禁在部署之后」的坏顺序反而看起来合规（与下方 --atomic 计数同口径）。
  first_code_line() {
    grep -nE -- "$2" "$1" 2>/dev/null |
      awk -F: '{ln=$1; sub(/^[0-9]+:/,""); if ($0 !~ /^[[:space:]]*#/) {print ln; exit}}'
  }
  mig_line="$(first_code_line "${REL_WF}" 'check-migration-gate\.sh')"
  upgrade_line="$(first_code_line "${REL_WF}" 'helm upgrade')"
  if [[ -z "${mig_line}" ]]; then
    echo "ERROR: ${REL_WF} must run ${MIG_GATE}" >&2
    FAIL=1
  elif [[ -n "${upgrade_line}" && "${mig_line}" -gt "${upgrade_line}" ]]; then
    echo "ERROR: migration gate must run before the first helm upgrade (RELS-09)" >&2
    FAIL=1
  else
    echo "    OK: migration gate precedes deploy"
  fi
  # 失败必回滚 + 等待就绪：缺任一参数，失败会留在半发布状态。
  for flag in '--wait' 'helm rollback'; do
    if grep -qF -- "${flag}" "${REL_WF}"; then
      echo "    OK: ${flag}"
    else
      echo "ERROR: ${REL_WF} missing '${flag}' (RELS-09)" >&2
      FAIL=1
    fi
  done
  # --atomic 必须覆盖每一次 upgrade：只在一处加等于留下半发布路径。
  # 统计前剔除整行注释：文件头注释里提到 --atomic 会污染计数，造成假通过。
  wf_body="$(grep -vE '^[[:space:]]*#' "${REL_WF}")"
  upgrades="$(grep -c 'helm upgrade --install' <<<"${wf_body}" || true)"
  atomics="$(grep -c -- '--atomic' <<<"${wf_body}" || true)"
  if [[ "${upgrades}" -lt 3 ]]; then
    echo "ERROR: ${REL_WF} must have >=3 helm upgrade steps (staging/canary/promote), got ${upgrades}" >&2
    FAIL=1
  elif [[ "${atomics}" -lt "${upgrades}" ]]; then
    echo "ERROR: every helm upgrade must pass --atomic (got ${atomics}/${upgrades}) (RELS-09)" >&2
    FAIL=1
  else
    echo "    OK: --atomic on all ${upgrades} upgrade steps"
  fi
  # 发布开关：未显式启用的仓库（fork / 演练）不得向集群写入。
  if grep -qF 'vars.RELEASE_ENABLED' "${REL_WF}"; then
    echo "    OK: deploy gated by RELEASE_ENABLED"
  else
    echo "ERROR: ${REL_WF} deploy jobs must be gated by vars.RELEASE_ENABLED" >&2
    FAIL=1
  fi
  # 生产与 staging 都必须走 Environment 审批（GitHub 保护规则挂在 Environment 上）。
  for env_name in staging production; do
    if grep -qE "^[[:space:]]+environment:[[:space:]]*${env_name}$" "${REL_WF}"; then
      echo "    OK: environment ${env_name}"
    else
      echo "ERROR: ${REL_WF} missing 'environment: ${env_name}' approval gate" >&2
      FAIL=1
    fi
  done
  # 不可变引用：镜像必须按 digest 部署（RELS-04）。
  if grep -qE 'image\.digest' "${REL_WF}"; then
    echo "    OK: images pinned by digest"
  else
    echo "ERROR: ${REL_WF} must deploy by image.digest, not a floating tag (RELS-04)" >&2
    FAIL=1
  fi
fi

echo "==> RELS-10: release smoke script"
if [[ ! -f "${SMOKE}" ]]; then
  echo "ERROR: missing ${SMOKE}" >&2
  FAIL=1
else
  for probe in '/healthz' '/readyz' '/version'; do
    if grep -qF -- "${probe}" "${SMOKE}"; then
      echo "    OK: probes ${probe}"
    else
      echo "ERROR: ${SMOKE} missing ${probe} probe (RELS-10)" >&2
      FAIL=1
    fi
  done
  if grep -qF 'release-smoke.sh' "${REL_WF}" 2>/dev/null; then
    echo "    OK: used by ${REL_WF}"
  else
    echo "ERROR: ${REL_WF} must run ${SMOKE} after each deploy" >&2
    FAIL=1
  fi
fi

echo "==> RELS-11: canary values and metric gate"
if [[ ! -f "${CANARY_VALUES}" ]]; then
  echo "ERROR: missing ${CANARY_VALUES}" >&2
  FAIL=1
else
  # 只看 migrate: 块内部（到下一个顶层键为止），避免误把 ingress/pdb 的 enabled: false 当证据。
  if awk '/^migrate:/{f=1;next} f&&/^[^[:space:]]/{exit} f&&/enabled:[[:space:]]*false/{found=1} END{exit !found}' "${CANARY_VALUES}"; then
    echo "    OK: canary never runs migrations"
  else
    echo "ERROR: ${CANARY_VALUES} must disable migrate (single migration owner)" >&2
    FAIL=1
  fi
  if ! grep -qF 'release-canary-gate.sh' "${REL_WF}" 2>/dev/null; then
    echo "ERROR: ${REL_WF} must run ${CANARY_GATE} before promoting" >&2
    FAIL=1
  else
    echo "    OK: canary gate wired into pipeline"
  fi
  if [[ ! -f "${CANARY_GATE}" ]]; then
    echo "ERROR: missing ${CANARY_GATE}" >&2
    FAIL=1
  elif ! grep -qF 'PROM_URL is required' "${CANARY_GATE}"; then
    echo "ERROR: ${CANARY_GATE} must fail closed when PROM_URL is unset" >&2
    FAIL=1
  else
    echo "    OK: canary gate fails closed without metrics"
  fi
  # 渲染校验：金丝雀必须是「单副本 + 无迁移 Job + 无 Ingress」。
  if command -v helm >/dev/null 2>&1; then
    # 直接捕获到变量而不是临时文件：门禁不应依赖可写临时目录（也不留下垃圾）。
    canary_out=""
    if ! canary_out="$(helm template admin-frame-canary deploy/helm/admin-frame \
      -f deploy/helm/admin-frame/values-prod.yaml -f "${CANARY_VALUES}" \
      --api-versions monitoring.coreos.com/v1 \
      --set fullnameOverride=admin-frame-canary \
      --set image.digest="sha256:$(printf 'a%.0s' {1..64})" 2>&1)"; then
      echo "ERROR: helm template with ${CANARY_VALUES} failed:" >&2
      printf '%s\n' "${canary_out}" >&2
      FAIL=1
    else
      deploys="$(grep -cE '^kind: Deployment$' <<<"${canary_out}" || true)"
      jobs="$(grep -cE '^kind: Job$' <<<"${canary_out}" || true)"
      ingresses="$(grep -cE '^kind: Ingress$' <<<"${canary_out}" || true)"
      if [[ "${deploys}" -ne 1 || "${jobs}" -ne 0 || "${ingresses}" -ne 0 ]]; then
        echo "ERROR: canary render must have 1 Deployment, 0 Job, 0 Ingress (got ${deploys}/${jobs}/${ingresses})" >&2
        FAIL=1
      elif ! grep -qE '^[[:space:]]+replicas:[[:space:]]*1$' <<<"${canary_out}"; then
        echo "ERROR: canary render must pin replicas: 1" >&2
        FAIL=1
      else
        echo "    OK: canary renders 1 Deployment / 1 replica / no migration / no ingress"
      fi
    fi
  else
    echo "    SKIP: helm not installed; canary render not verified"
  fi
fi

echo "==> RELS-12: rollback runbook contains executable commands"
ROLLBACK_RB="docs/runbooks/release-rollback.md"
if [[ ! -f "${ROLLBACK_RB}" ]]; then
  echo "ERROR: missing ${ROLLBACK_RB}" >&2
  FAIL=1
elif ! grep -qE 'helm rollback' "${ROLLBACK_RB}"; then
  echo "ERROR: ${ROLLBACK_RB} must contain the actual 'helm rollback' command (RELS-12)" >&2
  FAIL=1
else
  echo "    OK: helm rollback documented"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "release check failed (RELEASE_STANDARDS)" >&2
  exit 1
fi
echo "OK: release"
