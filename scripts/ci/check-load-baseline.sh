#!/usr/bin/env bash
# ENT-16 / PERF-06：全链路压测基线登记一致性门禁。
#
# 只校验「登记是否自洽、产物是否真实存在、压测是否被误接进默认门禁」，
# 不重新跑压测（PERF-06：全链路压测不是合并门禁）。
#
# 检查项：
#   LB-01 登记表每行的场景 ID / 执行器 / 环境 / 结论格式合法，执行器真实存在
#   LB-02 结论=通过 的行必须有产物 JSON；结论=待执行 的行产物列必须为 "-"
#   LB-03 产物内容自洽：samples > 0、errors = 0、success_rate ≥ 0.99
#   LB-04 产物目录无孤儿文件（每个 JSON 都被登记表引用）
#   LB-05 压测未进默认门禁：CI target 不得包含 load / e2e 目标
#   LB-06 k6 场景阈值与 identity-slo.md §1 的 SLO 数值一致
#
# 用法: bash scripts/ci/check-load-baseline.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
DOC="docs/perf/LOAD_BASELINES.md"
ARTIFACT_DIR="tests/load/artifacts"
K6_SCRIPT="tests/load/identity_login.js"
GO_RUNNER="tests/e2e/load_baseline_test.go"
SLO_DOC="docs/identity-slo.md"

fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

echo "==> LB-01: registry exists and is well formed"
for f in "${DOC}" "docs/perf/BASELINES.md" "docs/PERFORMANCE_STANDARDS.md" "${K6_SCRIPT}" "${GO_RUNNER}" "${SLO_DOC}"; do
  if [[ ! -f "${f}" ]]; then
    fail "missing ${f}"
  fi
done
if [[ "${FAIL}" -ne 0 ]]; then
  echo "load-baseline check failed (missing files)" >&2
  exit 1
fi

# cell <line> <column>：取 markdown 表格第 N 列（1 = 首个 | 之后的内容），去首尾空白。
cell() {
  printf '%s\n' "$1" | awk -F'|' -v n="$2" '{
    v = $(n + 1)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
    print v
  }'
}

ROWS=0
PASSED=0
PENDING=0
IN_REGISTRY=0
while IFS= read -r line; do
  # 只解析「## 2. 登记表」到下一个二级标题之间的表格：文档里还有环境变量表、
  # 判读表等同构表格，全量解析会把它们误判成登记行。
  case "${line}" in
    "## 2."*) IN_REGISTRY=1; continue ;;
    "## 3."*|"## 4."*|"## 5."*|"## 6."*|"## 7."*) IN_REGISTRY=0; continue ;;
  esac
  [[ "${IN_REGISTRY}" -eq 1 ]] || continue
  [[ "${line}" == "|"*"|" ]] || continue
  [[ "${line}" == *"场景 ID"* ]] && continue
  [[ "${line}" == *"|---"* ]] && continue

  id="$(cell "${line}" 1)"
  exec_spec="$(cell "${line}" 2)"
  env_name="$(cell "${line}" 3)"
  rate="$(cell "${line}" 4)"
  target="$(cell "${line}" 5)"
  verdict="$(cell "${line}" 7)"
  artifact="$(cell "${line}" 8)"

  [[ -z "${id}" ]] && continue

  if [[ "${id}" != LB-* ]]; then
    fail "registry row must start with 'LB-': ${line}"
    continue
  fi
  if [[ ! "${id}" =~ ^LB-[A-Z0-9]+(-[A-Z0-9]+)*$ ]]; then
    fail "invalid scenario id '${id}' (want LB-<SCOPE>-<RATE|NAME>)"
    continue
  fi
  for field_pair in "执行器:${exec_spec}" "环境:${env_name}" "到达率:${rate}" "目标:${target}" "结论:${verdict}" "产物:${artifact}"; do
    if [[ -z "${field_pair#*:}" ]]; then
      fail "${id}: empty ${field_pair%%:*} column"
    fi
  done

  # 执行器：file 或 file::TestFunc，文件与函数都必须真实存在。
  exec_file="${exec_spec%%::*}"
  exec_func=""
  if [[ "${exec_spec}" == *"::"* ]]; then
    exec_func="${exec_spec#*::}"
  fi
  if [[ ! -f "${exec_file}" ]]; then
    fail "${id}: executor file '${exec_file}' not found"
  elif [[ -n "${exec_func}" ]] && ! grep -q "^func ${exec_func}(" "${exec_file}"; then
    fail "${id}: executor '${exec_func}' not found in ${exec_file}"
  fi

  ROWS=$((ROWS + 1))
  case "${verdict}" in
    通过)
      PASSED=$((PASSED + 1))
      if [[ "${artifact}" == "-" ]]; then
        fail "${id}: verdict=通过 requires an artifact"
        continue
      fi
      if [[ ! -f "${artifact}" ]]; then
        fail "${id}: artifact '${artifact}' not found (LB-02)"
        continue
      fi
      json_num() {
        grep -o "\"$2\":[[:space:]]*[-0-9.]*" "$1" | head -1 | sed 's/.*:[[:space:]]*//'
      }
      samples="$(json_num "${artifact}" samples)"
      errors="$(json_num "${artifact}" errors)"
      success="$(json_num "${artifact}" success_rate)"
      if [[ -z "${samples}" || -z "${errors}" || -z "${success}" ]]; then
        fail "${id}: artifact '${artifact}' missing samples/errors/success_rate (LB-03)"
        continue
      fi
      if ! awk -v s="${samples}" 'BEGIN{exit !(s > 0)}'; then
        fail "${id}: artifact '${artifact}' has samples=${samples} (LB-03)"
      fi
      if ! awk -v e="${errors}" 'BEGIN{exit !(e == 0)}'; then
        fail "${id}: artifact '${artifact}' has errors=${errors} (LB-03)"
      fi
      if ! awk -v r="${success}" 'BEGIN{exit !(r >= 0.99)}'; then
        fail "${id}: artifact '${artifact}' has success_rate=${success} < 0.99 (LB-03)"
      fi
      echo "    OK: ${id} -> ${artifact} (p95 $(json_num "${artifact}" p95_ms)ms)"
      ;;
    待执行)
      PENDING=$((PENDING + 1))
      if [[ "${artifact}" != "-" ]]; then
        fail "${id}: verdict=待执行 must use '-' in artifact column (no fabricated data)"
      fi
      echo "    pending: ${id} (${env_name})"
      ;;
    *)
      fail "${id}: invalid verdict '${verdict}' (want 通过|待执行)"
      ;;
  esac
done < "${DOC}"

if [[ "${ROWS}" -eq 0 ]]; then
  fail "${DOC} has no registered scenario rows"
fi
echo "    rows: ${ROWS} (passed=${PASSED}, pending=${PENDING})"

echo "==> LB-04: no orphan artifacts"
if [[ -d "${ARTIFACT_DIR}" ]]; then
  while IFS= read -r f; do
    base="$(basename "${f}")"
    if ! grep -q "${base}" "${DOC}"; then
      fail "orphan artifact '${ARTIFACT_DIR}/${base}' is not referenced by ${DOC}"
    fi
  done < <(find "${ARTIFACT_DIR}" -maxdepth 1 -name '*.json' -type f | sort)
else
  fail "missing artifact directory ${ARTIFACT_DIR}"
fi

echo "==> LB-05: load tests must not enter the default gate"
CI_LINE="$(awk '/^ci:/{print; exit}' Makefile)"
if [[ -z "${CI_LINE}" ]]; then
  fail "cannot locate 'ci:' target in Makefile"
else
  # 按「独立 target 词」匹配：check-load-baseline 是登记门禁，不能因此误判。
  for token in "load-baseline" "check-e2e" "k6"; do
    if printf '%s\n' "${CI_LINE}" | grep -Eq "(^|[[:space:]])${token}([[:space:]]|$)"; then
      fail "ci target must not run '${token}' (PERF-06: load tests are not merge gates)"
    fi
  done
  if [[ "${CI_LINE}" != *"check-load-baseline"* ]]; then
    fail "ci target must include check-load-baseline (registry consistency only)"
  fi
fi

echo "==> LB-06: k6 thresholds must match identity-slo.md"
# k6 阈值：thresholds['identity_login_duration'] = ['p(95)<300', 'p(99)<800'];
js_login="$(grep -m1 "identity_login_duration'] = " "${K6_SCRIPT}")"
js_refresh="$(grep -m1 "identity_refresh_duration'] = " "${K6_SCRIPT}")"
js_mfa="$(grep -m1 "identity_mfa_duration'] = " "${K6_SCRIPT}")"

js_p95() { printf '%s\n' "$1" | grep -o 'p(95)<[0-9]*' | head -1 | sed 's/.*<//'; }
js_p99() { printf '%s\n' "$1" | grep -o 'p(99)<[0-9]*' | head -1 | sed 's/.*<//'; }
# SLO 表：| 登录 P95 | ≤ 300 ms | ...  → 300
slo_ms() {
  awk -F'|' -v label="$1" '
    $2 ~ label {
      v = $3
      gsub(/[^0-9]/, "", v)
      print v
      exit
    }' "${SLO_DOC}"
}

check_pair() {
  local label="$1" js_val="$2" slo_label="$3"
  local slo_val
  slo_val="$(slo_ms "${slo_label}")"
  if [[ -z "${js_val}" || -z "${slo_val}" ]]; then
    fail "cannot compare ${label}: k6='${js_val}' slo='${slo_val}'"
    return
  fi
  if [[ "${js_val}" != "${slo_val}" ]]; then
    fail "${label}: k6 threshold ${js_val}ms != identity-slo ${slo_val}ms"
  else
    echo "    OK: ${label} = ${js_val}ms"
  fi
}

check_pair "login p95" "$(js_p95 "${js_login}")" "登录 P95"
check_pair "login p99" "$(js_p99 "${js_login}")" "登录 P99"
check_pair "refresh p95" "$(js_p95 "${js_refresh}")" "刷新 P95"
check_pair "mfa p95" "$(js_p95 "${js_mfa}")" "MFA 校验 P95"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "load-baseline check failed (ENT-16 / PERF-06)" >&2
  exit 1
fi
echo "OK: load baseline registry"
