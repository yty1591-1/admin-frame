#!/usr/bin/env bash
# API_STANDARDS：分端 OpenAPI validate + 相对基线的 breaking 检测。
# 用法: bash scripts/ci/check-openapi.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

# Git Bash on Windows 常未继承系统 PATH 中的 Go。
if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found in PATH" >&2
  exit 1
fi

OASDIFF_VERSION="${OASDIFF_VERSION:-v1.33.0}"
SPECS=(api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml)
SEVERITY_FILE="api/openapi/.oasdiff-severity.txt"
COMPONENTS="api/openapi/components.yaml"
BUNDLER="./scripts/openapi/bundle"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT

oasdiff() {
  go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" "$@"
}

# 多文件契约须先装配为单文件，才能让 oasdiff 的 breaking 比对不受 $ref 路径差异影响。
bundle() {
  go run "${BUNDLER}" "$1" "$2"
}

# 从 OpenAPI 文本提取 info.version（info 块下首个 version）。
extract_version() {
  awk '
    /^info:[[:space:]]*$/ { in_info=1; next }
    in_info && /^[^[:space:]#]/ { in_info=0 }
    in_info && /^[[:space:]]+version:[[:space:]]*/ {
      sub(/^[[:space:]]+version:[[:space:]]*/, "", $0)
      gsub(/["'\''[:space:]]/, "", $0)
      print $0
      exit
    }
  '
}

# 解析 MAJOR.MINOR.PATCH（允许可选 v 前缀）；失败返回非 0。
parse_semver() {
  local v="${1#v}"
  if [[ ! "${v}" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)([.-].*)?$ ]]; then
    return 1
  fi
  _maj="${BASH_REMATCH[1]}"
  _min="${BASH_REMATCH[2]}"
  _pat="${BASH_REMATCH[3]}"
  return 0
}

# 修订版 major 是否大于基线 major。
major_bumped() {
  local base_v="$1" rev_v="$2"
  local bm bm_min bm_pat rm rm_min rm_pat
  parse_semver "${base_v}" || return 1
  bm="${_maj}"; bm_min="${_min}"; bm_pat="${_pat}"
  parse_semver "${rev_v}" || return 1
  rm="${_maj}"; rm_min="${_min}"; rm_pat="${_pat}"
  [[ "${rm}" -gt "${bm}" ]]
}

resolve_base_ref() {
  if [[ -n "${OPENAPI_BASE_REF:-}" ]]; then
    echo "${OPENAPI_BASE_REF}"
    return 0
  fi
  if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    if git rev-parse --verify "origin/${GITHUB_BASE_REF}" >/dev/null 2>&1; then
      echo "origin/${GITHUB_BASE_REF}"
      return 0
    fi
    if git rev-parse --verify "${GITHUB_BASE_REF}" >/dev/null 2>&1; then
      echo "${GITHUB_BASE_REF}"
      return 0
    fi
  fi
  local cand
  for cand in origin/main origin/master main master; do
    if git rev-parse --verify "${cand}" >/dev/null 2>&1; then
      echo "${cand}"
      return 0
    fi
  done
  return 1
}

echo "==> oasdiff ${OASDIFF_VERSION}"
echo "==> validate shared components"
oasdiff validate "${COMPONENTS}" --fail-on ERR

FAIL=0
for spec in "${SPECS[@]}"; do
  echo "==> validate ${spec}"
  if ! oasdiff validate "${spec}" --fail-on ERR; then
    FAIL=1
  fi
done

# API-13：分端规范自包含——B/C 端不得跨引 schemas/admin。
# 跨引会把 A 端同名组件一并提升进 bundle，导致「同名不同源」的组件冲突
# （Role / VersionInfo / SubjectTypePath），也让端别隔离在文档层失效。
echo "==> API-13: channel specs must be self-contained"
for channel in biz c; do
  # 只看真实引用行：注释里提到 schemas/admin 是在解释本规则，不算违规。
  hits="$(grep -rn "schemas/admin" "api/openapi/paths/${channel}" "api/openapi/schemas/${channel}" 2>/dev/null |
    grep -vE ':[[:space:]]*#' || true)"
  if [[ -n "${hits}" ]]; then
    echo "ERROR: ${channel} spec must not reference schemas/admin (API-13):" >&2
    printf '%s\n' "${hits}" >&2
    FAIL=1
  else
    echo "    OK: ${channel} has no admin schema import"
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "openapi contract check failed (API-08/API-13)" >&2
  exit 1
fi

BASE_REF=""
if BASE_REF="$(resolve_base_ref)"; then
  echo "==> breaking base ref: ${BASE_REF}"
else
  echo "WARN: no git base ref; skipping breaking checks (validate only)" >&2
  echo "OK: openapi validate"
  exit 0
fi

# 基线为多文件目录树，须整树取出才能解析相对 $ref。
BASE_DIR="${TMP_DIR}/base"
mkdir -p "${BASE_DIR}"
if ! git archive "${BASE_REF}" api/openapi | tar -x -C "${BASE_DIR}"; then
  echo "WARN: cannot export ${BASE_REF}:api/openapi; skipping breaking checks (validate only)" >&2
  echo "OK: openapi validate"
  exit 0
fi

for spec in "${SPECS[@]}"; do
  name="$(basename "${spec}" .yaml)"
  base_spec="${BASE_DIR}/${spec}"
  if [[ ! -f "${base_spec}" ]]; then
    echo "==> ${spec}: new file vs ${BASE_REF}, skip breaking"
    continue
  fi

  base_ver="$(extract_version <"${base_spec}" || true)"
  rev_ver="$(extract_version <"${spec}" || true)"
  echo "==> breaking ${spec} (${base_ver:-?} -> ${rev_ver:-?})"

  base_bundle="${TMP_DIR}/base-${name}.yaml"
  rev_bundle="${TMP_DIR}/rev-${name}.yaml"
  # 基线整树可能尚未满足 API-13（B 端跨引 A 端 schema 于 2026-11-04 按 API-13 修复），
  # 这类历史树无法装配成单文件：明确告警并跳过本端 breaking 比对，
  # 基线刷新后自动恢复严格比对（不允许静默跳过）。
  if ! bundle "${base_spec}" "${base_bundle}"; then
    echo "WARN: cannot bundle ${BASE_REF}:${spec} (legacy cross-channel refs); skipping breaking check for this spec" >&2
    continue
  fi
  bundle "${spec}" "${rev_bundle}"

  if [[ -n "${base_ver}" && -n "${rev_ver}" ]] && major_bumped "${base_ver}" "${rev_ver}"; then
    echo "    major bumped: intentional breaking allowed (API-07)"
    # 仍打印报告，但不因 ERR 失败。
    oasdiff breaking "${base_bundle}" "${rev_bundle}" \
      --severity-levels "${SEVERITY_FILE}" \
      --deprecation-days-stable 90 \
      --format text || true
    continue
  fi

  if ! oasdiff breaking "${base_bundle}" "${rev_bundle}" \
    --fail-on ERR \
    --severity-levels "${SEVERITY_FILE}" \
    --deprecation-days-stable 90 \
    --format text; then
    echo "ERROR: breaking changes in ${spec} without major bump of info.version" >&2
    echo "       bump major (e.g. 1.x.y -> 2.0.0) or make the change backward compatible (API-07)" >&2
    FAIL=1
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "openapi breaking check failed (API-07)" >&2
  exit 1
fi

echo "OK: openapi validate + breaking"
