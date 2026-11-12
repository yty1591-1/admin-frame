#!/usr/bin/env bash
# CI-16：每个 workflow job 必须有本地可执行入口（防「job 集合漂移」）。
#
# 背景：2026-11-07 的 CI 双红——`test` job 的 IAM total 覆盖率门禁只在设置
# IAM_TEST_MYSQL_DSN 时启用，而 Image workflow 的 Trivy 扫描在本地根本没有对应入口。
# 结果是本地 `make ci` exit 0、远端连红，且从本地无从发现。
#
# 本门禁把「新增 job 必须同时给本地入口」变成可执行断言，做三件事：
#   1. 构建类 workflow（ci.yml / image.yml）的每个 job 必须在 PARITY 表登记；
#   2. PARITY 指向的 make 目标必须真实存在（防写错目标名后门禁空转）；
#   3. 发布类 workflow（release.yml）的每个 job 必须在 DEPLOY 表登记「本地等价物或
#      「为何无法本地化」，防止新 job 悄悄飘进来没人管。
# 反向也查：表里登记了 workflow 中已不存在的 job，说明表过期，同样报错。
#
# 只做静态解析、不需要网络与容器，可在任意 job 里跑。
#
# 用法: bash scripts/ci/check-ci-parity.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "    ERROR: $1" >&2; FAIL=1; }

# 构建/门禁类流水线：job → 本地 make 目标（空格分隔，多个即需全部存在）。
# 新增或改名 job 时必须同 MR 更新本表；目标名必须与 Makefile 完全一致。
declare -A PARITY=(
  [fmt]="fmt-check"
  [mod]="tidy-check"
  [lint]="lint"
  [test]="test-ci"
  [integration]="test-integration"
  [vuln]="vuln check-deps"
  [arch]="check-transport check-layering check-ci-paths check-migrations check-reliability check-observability check-git check-release check-sbom check-runbooks check-oncall check-audit check-license check-codegen check-perf check-idempotency check-outbox check-authn check-alerts check-backup"
  [secrets]="secrets check-debug check-no-static-config"
  [openapi]="openapi"
  [openapi-docs]="openapi-artifacts"
  [build]="build"
  [deploy]="check-deploy check-alerts check-release"
  [image]="image-scan"
  [release]="build check-release check-sbom"
)

# 发布类流水线：需要真实集群 / registry，多数 job 无法本地化。
# 值以 `make ` 开头表示有本地等价目标（会校验其存在），否则写清无法本地化的原因。
declare -A DEPLOY=(
  [plan]="无本地等价物：发布编排与 tag/服务矩阵选择"
  [preflight]="make check-migration-gate"
  [digests]="无本地等价物：需从 registry 解析镜像 digest"
  [verify]="make check-sbom"
  [staging]="无本地等价物：需要 k8s 集群"
  [canary]="无本地等价物：需要 k8s 集群与指标后端"
  [promote]="无本地等价物：需要 k8s 集群"
  [rollback]="无本地等价物：需要 k8s 集群"
  [notice]="无本地等价物：发布被禁用时的状态通报"
)

echo "==> CI-16: workflow jobs must have a local entry point"

# job 名 = jobs: 段下缩进 2 空格、以冒号结尾的键。
extract_jobs() {
  awk '
    /^jobs:[[:space:]]*$/ { injobs = 1; next }
    injobs && /^[^[:space:]#]/ { injobs = 0 }
    injobs && /^  [A-Za-z0-9_.-]+:[[:space:]]*$/ {
      line = $0
      sub(/^  /, "", line)
      sub(/:.*/, "", line)
      print line
    }
  ' "$1"
}

# make 目标名：规则行冒号左侧的全部名字（支持 `check-openapi openapi:` 多目标写法）。
# `:($|[^=])` 同时排除 `VAR := value` 赋值行，也覆盖 `target:` 后直接换行的情况。
make_targets="$(awk '
  /^\.PHONY/ { next }
  /^[A-Za-z0-9_.-]+([ \t]+[A-Za-z0-9_.-]+)*[ \t]*:($|[^=])/ {
    lhs = $0
    sub(/:.*/, "", lhs)
    n = split(lhs, parts, /[ \t]+/)
    for (i = 1; i <= n; i++) if (parts[i] != "") print parts[i]
  }
' Makefile | sort -u)"

if [[ -z "${make_targets}" ]]; then
  fail "no make target parsed from Makefile (rule shape changed?)"
fi

target_exists() { grep -qx -- "$1" <<< "${make_targets}"; }

check_group() {
  local file="$1" label="$2" job_count=0 unmapped=0
  local jobs
  jobs="$(extract_jobs "${file}" | sort -u || true)"
  if [[ -z "${jobs}" ]]; then
    fail "no job parsed from ${file} (jobs: block shape changed?)"
    echo "    ${label}: parsed 0 jobs"
    return
  fi
  while IFS= read -r job; do
    [[ -n "${job}" ]] || continue
    job_count=$((job_count + 1))
    local mapped=""
    if [[ "${label}" == "build" ]]; then
      mapped="${PARITY[${job}]+set}"
    else
      mapped="${DEPLOY[${job}]+set}"
    fi
    if [[ -z "${mapped}" ]]; then
      fail "job '${job}' in ${file} has no local entry (CI-16): 请新增 make 目标并在 scripts/ci/check-ci-parity.sh 登记"
      unmapped=$((unmapped + 1))
      continue
    fi
    if [[ "${label}" != "build" ]]; then
      # 发布类只校验「有 make 等价目标」的那几项。
      local entry="${DEPLOY[${job}]}"
      if [[ "${entry}" == make\ * ]]; then
        if ! target_exists "${entry#make }"; then
          fail "job '${job}' maps to missing make target '${entry#make }' (CI-16)"
        fi
      fi
      continue
    fi
    for target in ${PARITY[${job}]}; do
      if ! target_exists "${target}"; then
        fail "job '${job}' maps to missing make target '${target}' (CI-16)"
      fi
    done
  done <<< "${jobs}"
  echo "    ${label} (${file##*/}): ${job_count} jobs, unmapped ${unmapped}"
}

for wf in .github/workflows/ci.yml .github/workflows/image.yml; do
  [[ -f "${wf}" ]] || { fail "missing workflow ${wf}"; continue; }
  check_group "${wf}" build
done

for wf in .github/workflows/release.yml; do
  [[ -f "${wf}" ]] || { fail "missing workflow ${wf}"; continue; }
  check_group "${wf}" deploy
done

# 反向：表里登记了 workflow 中已不存在的 job，说明表过期。
all_jobs="$(for wf in .github/workflows/*.yml; do [[ -f "${wf}" ]] && extract_jobs "${wf}"; done | sort -u)"
stale=0
for job in "${!PARITY[@]}"; do
  if ! grep -qx -- "${job}" <<< "${all_jobs}"; then
    fail "PARITY registers '${job}' but no workflow defines it; 请同步删除该行（避免门禁空转）"
    stale=$((stale + 1))
  fi
done
for job in "${!DEPLOY[@]}"; do
  if ! grep -qx -- "${job}" <<< "${all_jobs}"; then
    fail "DEPLOY registers '${job}' but no workflow defines it; 请同步删除该行"
    stale=$((stale + 1))
  fi
done

echo "    stale entries ${stale}"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "ci parity check failed (CI_STANDARDS CI-16)" >&2
  exit 1
fi
echo "OK: ci parity ($(printf '%s\n' "${all_jobs}" | grep -c . ) jobs)"
