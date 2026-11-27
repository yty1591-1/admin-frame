#!/usr/bin/env bash
# 为 test job 决定是否注入 MySQL 集成测试 DSN。
#
# 背景（2026-11-26 Gitea CI 实测）：GitHub Actions 的 `services.mysql` 是
# GitHub 专有能力，Gitea Act Runner 不会拉起 sidecar 容器。而 workflow 若把
# DSN 硬编码在 job 级 env 里，测试会「以为库可用、实际连不上」，
# 83 个 SQL 用例批量报 `dial tcp 127.0.0.1:3306: connection refused`，
# 且不会走 t.Skip 分支——这是最坏的一种失败：错误信息指向被测代码。
#
# 本脚本把「有没有库」变成显式探测：
#   - 已有 DSN（调用方显式指定）→ 原样保留，不做探测；
#   - 127.0.0.1:<port> 可连 → 注入与 GitHub service 完全一致的 DSN；
#   - 不可连 + MYSQL_REQUIRED 未置位 → 不注入，让用例 t.Skip 并打印原因；
#   - 不可连 + MYSQL_REQUIRED=1 → 硬失败。
#
# MYSQL_REQUIRED 的用途是防止「探测失败→静默跳过」把 SQL 覆盖悄悄丢掉：
# GitHub 平台一定提供 services.mysql，探不到就是真问题，必须红；
# Gitea 等不提供 services 的平台置空前缀，按「跳过并说明」处理。
#
# 用法: bash scripts/ci/test-mysql-env.sh
#   在有 $GITHUB_ENV 的环境（Actions / Act）写入该文件供后续 step 继承；
#   在本地无 $GITHUB_ENV 时仅打印，供 eval 使用。
set -euo pipefail

MYSQL_HOST="${TEST_MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${TEST_MYSQL_PORT:-3306}"
# 与 .github/workflows/ci.yml 中 services.mysql 的账号保持一致（CI-12：测试默认值）。
MYSQL_USER="${TEST_MYSQL_USER:-iam}"
MYSQL_PASS="${TEST_MYSQL_PASS:-iam}"
MYSQL_DB="${TEST_MYSQL_DB:-iam_test}"
MYSQL_REQUIRED="${MYSQL_REQUIRED:-}"
# service 容器「healthy」到宿主端口真正可连之间有窗口期，单次探测会误判，
# 因此按固定间隔重试；总等待 30s 覆盖 GitHub service 常见启动时间。
MYSQL_PROBE_ATTEMPTS="${MYSQL_PROBE_ATTEMPTS:-30}"

report() { echo "$1"; }
export_line() {
  local k="$1" v="$2"
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    printf '%s=%s\n' "${k}" "${v}" >>"${GITHUB_ENV}"
  fi
  printf 'export %s=%s\n' "${k}" "${v}"
}

# 调用方已显式指定时不做任何推断，避免覆盖生产/预发的真实配置。
if [[ -n "${IAM_TEST_MYSQL_DSN:-}" ]]; then
  report "==> test-mysql-env: IAM_TEST_MYSQL_DSN 已由调用方设置，跳过探测"
  exit 0
fi

# 探测手段：bash /dev/tcp。部分精简 shell 不支持，此时按「不可连」处理
# （宁可不注入让用例跳过，也不注入一个大概率连不上的 DSN）。
reachable=0
for _ in $(seq 1 "${MYSQL_PROBE_ATTEMPTS}"); do
  if (exec 3<>"/dev/tcp/${MYSQL_HOST}/${MYSQL_PORT}") 2>/dev/null; then
    reachable=1
    break
  fi
  sleep 1
done

if [[ "${reachable}" -eq 0 ]]; then
  if [[ "${MYSQL_REQUIRED}" == "1" ]]; then
    echo "ERROR: ${MYSQL_HOST}:${MYSQL_PORT} 在 ${MYSQL_PROBE_ATTEMPTS}s 内不可连，但该平台声明了 MySQL service（MYSQL_REQUIRED=1）。" >&2
    echo "       SQL 集成用例会被静默跳过，覆盖率门禁随之失效——按硬失败处理。" >&2
    exit 1
  fi
  report "==> test-mysql-env: ${MYSQL_HOST}:${MYSQL_PORT} 不可连，跳过 DSN 注入"
  report "    MySQL 集成用例将 t.Skip 并打印原因（runner 不提供 services 时，请显式配置外部 MySQL）"
  exit 0
fi

dsn="${MYSQL_USER}:${MYSQL_PASS}@tcp(${MYSQL_HOST}:${MYSQL_PORT})/${MYSQL_DB}?parseTime=true&loc=UTC"
report "==> test-mysql-env: ${MYSQL_HOST}:${MYSQL_PORT} 可连，注入 MySQL 集成测试 DSN"
for key in IAM_TEST_MYSQL_DSN TENANT_TEST_MYSQL_DSN NOTIFY_TEST_MYSQL_DSN \
  FEATUREFLAG_TEST_MYSQL_DSN CONFIGCENTER_TEST_MYSQL_DSN; do
  export_line "${key}" "${dsn}"
done
