#!/usr/bin/env bash
# ENT-13：依赖韧性与第三方集成门禁（RELIABILITY_STANDARDS.md §4.2 / §5.1）。
#
# 三项断言，防止「注册表存在但没人用」与「回调端点绕过模板」：
#   RS-01 依赖注册表：Registry / Caller / Observer 原语 + 指标适配 +
#         bootstrap 注入存在
#   RS-02 回调模板三件套：既有回调端点必须挂 pkg/webhook 验签，幂等
#         服务与状态机推进存在（参照实现：notification callback）
#   RS-03 出站直连扫描：internal 内裸 http.Client{ / http.Get( / http.Post(
#         引用必须出现在白名单（oauth 驱动 / Vault——已各自端口化）
#
# 用法: bash scripts/ci/check-resilience.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "    ERROR: $1" >&2; FAIL=1; }

echo "==> RS-01: dependency registry primitives + wiring"
for symbol in "type Registry struct" "type Caller interface" "ErrUnknownDependency" \
  "CallObserved(name, result string)" "RetryObserved(name string)"; do
  grep -rq "${symbol}" internal/platform/dependency/ --include='*.go' \
    || fail "registry primitive missing: ${symbol}"
done
test -f internal/platform/dependency/observability/metrics.go \
  || fail "dependency metrics adapter missing (dependency_call_total)"
# 装配入口拆为 bootstrap.go（Run）与 infra.go（prepareInfra），注册表在后者构造。
grep -q "deps.Dependencies = dependency.NewRegistry" internal/platform/bootstrap.go internal/platform/infra.go 2>/dev/null \
  || fail "registry must be constructed in bootstrap"
grep -q "Dependencies \*dependency.Registry" internal/platform/bootstrap.go \
  || fail "Deps must expose the registry"
if [[ $FAIL -eq 0 ]]; then echo "    OK: registry + metrics + bootstrap wiring"; fi

echo "==> RS-02: callback template (sign / idempotent / state machine)"
# 参照实现三件套断言（新回调端点必须同构，review 依据 §5.1）。
grep -q "pkgwebhook" internal/notification/transport/callback/handler.go \
  || fail "callback endpoint must verify signatures via pkg/webhook"
grep -qE "fail closed|拒绝启动|密钥缺失" internal/notification/transport/callback/handler.go \
  || fail "callback endpoint must fail closed on missing secret"
grep -q "幂等吸收" internal/notification/app/receipt.go \
  || fail "receipt service must document idempotent absorption"
grep -q "Transition" internal/notification/app/receipt.go \
  || fail "receipt must advance via domain state machine"
grep -q "### 5.1 第三方回调接入模板" docs/RELIABILITY_STANDARDS.md \
  || fail "callback template section missing in RELIABILITY_STANDARDS.md"
if [[ $FAIL -eq 0 ]]; then echo "    OK: callback template present (webhook verify + idempotent + state machine)"; fi

echo "==> RS-03: bare outbound HTTP scan"
# 白名单：oauth 驱动（自带超时客户端注入）、Vault（ENT-07 fail-fast 语义）。
VIOLATIONS=$(grep -rn 'http\.Client{\|http\.Get(\|http\.Post(' internal/ --include='*.go' 2>/dev/null \
  | grep -v _test \
  | grep -v "internal/identity/infrastructure/oauth" \
  | grep -v "internal/platform/secrets" || true)
if [[ -n "$VIOLATIONS" ]]; then
  fail "bare outbound HTTP found (use internal/platform/dependency registry or httpclient):"
  echo "$VIOLATIONS" | head -5 >&2
else
  echo "    OK: no bare outbound HTTP outside whitelist"
fi

if [[ $FAIL -ne 0 ]]; then
  echo "check-resilience: FAILED" >&2
  exit 1
fi
echo "check-resilience: OK"
