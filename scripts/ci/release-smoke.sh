#!/usr/bin/env bash
# ENT-17 / RELS-10：发布冒烟：探针 + 版本一致性 + 未认证拒绝。
#
# 只依赖 curl：GitHub Runner、生产跳板机与本地容器都能直接跑，不引入
# python/jq 之类的额外依赖（发布路径上每多一个依赖就多一个失败面）。
#
# 用法：
#   bash scripts/ci/release-smoke.sh <base-url> [expected-version]
#
# 环境变量：
#   SMOKE_RETRIES    探针重试次数（默认 10；--wait 后仍可能有瞬时 503）
#   SMOKE_INTERVAL   重试间隔秒（默认 3）
#   SMOKE_PROTECTED  未认证探测路径（默认 /admin/v1/tenants）
set -euo pipefail

BASE="${1:-}"
EXPECT_VERSION="${2:-}"
RETRIES="${SMOKE_RETRIES:-10}"
INTERVAL="${SMOKE_INTERVAL:-3}"
PROTECTED="${SMOKE_PROTECTED:-/admin/v1/tenants}"

if [[ -z "${BASE}" ]]; then
  echo "usage: release-smoke.sh <base-url> [expected-version]" >&2
  exit 2
fi
BASE="${BASE%/}"

FAIL=0
fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

# http_get <path> → 打印 "status<TAB>body"
http_get() {
  local path="$1" out code body
  out="$(curl -sS --max-time 8 -w '\n%{http_code}' "${BASE}${path}" 2>&1 || true)"
  code="$(printf '%s' "${out}" | tail -n1)"
  body="$(printf '%s' "${out}" | sed '$d')"
  # 末尾换行不能省：read 在无换行 EOF 上返回非 0，set -e 会直接终止脚本。
  printf '%s\t%s\n' "${code}" "${body}"
}

# json_string <body> <field>：从扁平 JSON 中取字符串字段（版本号等）。
json_string() {
  printf '%s' "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 |
    sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/'
}

# json_number <body> <field>：取数字字段（业务码）。
json_number() {
  printf '%s' "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*-*[0-9]\+" | head -1 |
    sed -E 's/.*:[[:space:]]*//'
}

wait_probe() {
  local path="$1" i code
  for ((i = 1; i <= RETRIES; i++)); do
    IFS=$'\t' read -r code _ < <(http_get "${path}")
    if [[ "${code}" == "200" ]]; then
      echo "    OK: ${path} 200 (attempt ${i})"
      return 0
    fi
    sleep "${INTERVAL}"
  done
  fail "${path} not ready after ${RETRIES} attempts"
  return 1
}

echo "==> smoke: ${BASE}"
wait_probe "/healthz" || true
wait_probe "/readyz" || true

IFS=$'\t' read -r code body < <(http_get "/version")
if [[ "${code}" != "200" ]]; then
  fail "/version returned ${code}"
else
  version="$(json_string "${body}" "version")"
  commit="$(json_string "${body}" "commit")"
  if [[ -z "${version}" ]]; then
    fail "/version payload has no version field: ${body}"
  fi
  if [[ -n "${EXPECT_VERSION}" && "${version}" != "${EXPECT_VERSION}" ]]; then
    fail "version mismatch: running ${version}, expected ${EXPECT_VERSION} (发布未生效或流量仍指向旧版本)"
  else
    echo "    OK: version=${version} commit=${commit:-?}"
  fi
fi

# 未认证请求必须被拒：证明中间件链与鉴权入口在生产配置下仍然生效
# （发布最常见的静默故障是 auth 模式被配成 demo/off）。
IFS=$'\t' read -r code body < <(http_get "${PROTECTED}")
if [[ "${code}" != "401" ]]; then
  fail "${PROTECTED} without token = ${code}, want 401 (auth mode off?)"
else
  biz_code="$(json_number "${body}" "code")"
  if [[ -n "${biz_code}" && "${biz_code}" != "40100" ]]; then
    fail "${PROTECTED} 401 body code=${biz_code}, want 40100"
  else
    echo "    OK: unauthenticated request rejected (40100)"
  fi
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "release smoke failed (ENT-17 / RELS-10)" >&2
  exit 1
fi
echo "OK: release smoke"
