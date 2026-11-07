#!/usr/bin/env bash
# 跑 `make ci`（或指定目标）并汇总「本次到底跳过了什么」。
#
# 动机：门禁脚本在缺前置条件时会 SKIP 而不是 FAIL（本地无 MySQL/无 helm/无 CGO 时便于
# 只跑其余检查）。代价是跳过信息淹没在几百行日志里，最终只看得到 MAKE_EXIT=0。
# 2026-11-07 的 `internal/iam total 68.5% < 69%` 就是被这层降级藏住的。
#
# 行为：
#   1. 执行 make，输出同时落盘（默认 tmp/ci/ci-<时间戳>.log）；
#   2. 汇总所有 SKIP/跳过行，分成「已豁免」（附理由）与「未豁免」两栏；
#   3. CI_STRICT=1 时，只要存在未豁免跳过即失败（已豁免项必须在下方 ALLOW 表写明理由）。
#
# 用法:
#   bash scripts/ci/ci-run.sh              # 等价 make ci + 汇总
#   CI_STRICT=1 bash scripts/ci/ci-run.sh  # 严格模式（make ci-strict）
#   bash scripts/ci/ci-run.sh test-ci lint # 只跑指定目标
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

LOG_DIR="${CI_LOG_DIR:-tmp/ci}"
mkdir -p "${LOG_DIR}"
log="${LOG_DIR}/ci-$(date +%Y%m%d-%H%M%S).log"

targets=("$@")
if [[ "${#targets[@]}" -eq 0 ]]; then
  targets=(ci)
fi

# 已豁免的跳过：pattern|理由。pattern 为 grep -E 正则。
# 豁免只允许「工具/环境缺失」类，且必须写清由谁在 CI 覆盖；判据类门禁一律不得豁免。
ALLOW=(
  "promtool not installed|本地未装 promtool；CI deploy job 用 prometheus 2.55.1 校验"
  "-race skipped|本地无 CGO 工具链；由 make ci-race 在 golang 容器内补跑（ci-full 默认执行）"
  "GIT_BASE_REF empty|变更行覆盖率仅 PR 场景；ci-full 会注入 origin/main"
  "MG-07: skipped|schema 探测需 MIGRATE_DSN，CI 同样未设置（MG-01～06 静态检查已覆盖）"
  "not a git work tree|仅在仓库目录之外执行时出现"
)

echo "==> make ${targets[*]}（日志: ${log}）"
set +e
make "${targets[@]}" 2>&1 | tee "${log}"
status=${PIPESTATUS[0]}
set -e

# 「静默消失」的门禁不会打印 SKIP，只能从环境反推：DSN 为空时
# check-iam-coverage.sh 直接不跑 total 与 persistence 两道门禁。
# 只在整套门禁（ci / test-ci）时判定，跑单个目标时 DSN 本就无关。
env_skips=()
if printf '%s\n' "${targets[@]}" | grep -qxE 'ci|test-ci'; then
  for v in IAM_TEST_MYSQL_DSN TENANT_TEST_MYSQL_DSN NOTIFY_TEST_MYSQL_DSN FEATUREFLAG_TEST_MYSQL_DSN; do
    if [[ -z "${!v:-}" ]]; then
      env_skips+=("${v} empty: 依赖真实 MySQL 的用例与 IAM 总体覆盖率门禁未生效")
    fi
  done
fi

mapfile -t skip_lines < <(grep -nE 'SKIP:|skipped|跳过' "${log}" 2>/dev/null \
  | grep -vE '^[0-9]+:[[:space:]]*#' \
  | grep -vE '^[0-9]+:OK:' || true)
# 排除 `OK: ... skipped ...`：那是门禁自己的通过结论（如「本次未改动 IAM 生产代码，
# 无需 diff 覆盖率」），不是缺口。真正的缺口不会以 OK 开头。

allowed=()
blocked=()
for line in "${skip_lines[@]}"; do
  body="${line#*:}"
  matched=0
  for rule in "${ALLOW[@]}"; do
    pattern="${rule%%|*}"
    reason="${rule#*|}"
    if grep -qE -- "${pattern}" <<< "${body}"; then
      allowed+=("${reason}  ← ${body}")
      matched=1
      break
    fi
  done
  [[ "${matched}" -eq 1 ]] || blocked+=("${body}")
done

echo
echo "==> 跳过项汇总"
if [[ "${#allowed[@]}" -eq 0 && "${#blocked[@]}" -eq 0 && "${#env_skips[@]}" -eq 0 ]]; then
  echo "    无：本次运行与 CI 判定等价"
else
  if [[ "${#allowed[@]}" -gt 0 ]]; then
    echo "    已豁免（${#allowed[@]}）:"
    for a in "${allowed[@]}"; do echo "      - ${a}"; done
  fi
  if [[ "${#env_skips[@]}" -gt 0 ]]; then
    echo "    环境缺失导致的静默跳过（${#env_skips[@]}）:"
    for e in "${env_skips[@]}"; do echo "      - ${e}"; done
  fi
  if [[ "${#blocked[@]}" -gt 0 ]]; then
    echo "    未豁免（${#blocked[@]}）:"
    for b in "${blocked[@]}"; do echo "      - ${b}"; done
  fi
fi
echo

if [[ "${status}" -ne 0 ]]; then
  echo "FAIL: make ${targets[*]} exit ${status}（详见 ${log}）" >&2
  exit "${status}"
fi

if [[ "${CI_STRICT:-0}" == "1" ]]; then
  unaccounted=$(( ${#blocked[@]} + ${#env_skips[@]} ))
  if [[ "${unaccounted}" -gt 0 ]]; then
    echo "STRICT FAIL: ${unaccounted} 项门禁未执行（CI 会执行）。" >&2
    echo "  用 make ci-full 补齐依赖，或为合理的环境性缺失在 scripts/ci/ci-run.sh 的 ALLOW 表登记理由。" >&2
    exit 1
  fi
  echo "OK: strict（无未豁免跳过）"
fi

echo "OK: ${targets[*]}"
