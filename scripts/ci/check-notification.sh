#!/usr/bin/env bash
# ENT-10 NT-08：通知域门禁。
#
# 三项对账，防止「代码与文档/契约各自漂移」：
#   NF-01 代码中的 notification.* 审计动作常量 ↔ docs/audit/EVENTS.md 双向对账
#        （对齐 check-tenant.sh TN-01；新增动作靠人肉同步 EVENTS.md 的补丁）
#   NF-02 状态机迁移矩阵快照：transitions 白名单以测试内硬编码快照锁定，
#        改白名单必须同步改测试（go test 承担，脚本负责触发）
#   NF-03 渠道包 internal 引用面扫描：铁律 5.1.3——业务域禁止在事务里
#        同步调外部渠道；pkg/sms|email|push 只允许出现在
#        notification/infrastructure/channel（编排适配）与
#        identity 的验证码旁路（ADR-0008 §3 唯一例外）
#
# 用法: bash scripts/ci/check-notification.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

# 提取 EVENTS.md 中 status=active 的 notification.* 动作（表格第一列反引号包裹）。
# planned 行是目录占位（路径未合入），不参与代码对账。
doc_actions() {
  grep -E '^\| `notification\.[a-z_.]+`' docs/audit/EVENTS.md | grep -E '\| active \|$' \
    | grep -o '`notification\.[a-z_.]*`' | tr -d '`' | sort -u
}

echo "==> NF-01: audit actions code <-> EVENTS.md"
# 审计动作常量的形式是 Action* = "notification.xxx"；Topic / 其他常量不算动作。
mapfile -t CODE < <(
  grep -rhoE 'Action[A-Za-z]+ *= *"notification\.[a-z_.]+"' internal/notification/app/ --include='*.go' 2>/dev/null \
    | grep -v _test | sed -E 's/.*"(notification\.[a-z_.]+)".*/\1/' | sort -u
)
mapfile -t DOC < <(doc_actions)
if [[ ${#CODE[@]} -eq 0 ]]; then
  echo "    ERROR: no notification.* action constants found in internal/notification/app" >&2
  FAIL=1
elif [[ ${#DOC[@]} -eq 0 ]]; then
  echo "    ERROR: no notification.* actions found in docs/audit/EVENTS.md" >&2
  FAIL=1
else
  MISSING_IN_DOC=$(comm -23 <(printf '%s\n' "${CODE[@]}") <(printf '%s\n' "${DOC[@]}"))
  MISSING_IN_CODE=$(comm -13 <(printf '%s\n' "${CODE[@]}") <(printf '%s\n' "${DOC[@]}"))
  if [[ -n "${MISSING_IN_DOC}" ]]; then
    echo "    ERROR: actions in code but missing in EVENTS.md:" >&2
    echo "${MISSING_IN_DOC}" | sed 's/^/      /' >&2
    FAIL=1
  fi
  if [[ -n "${MISSING_IN_CODE}" ]]; then
    echo "    ERROR: actions in EVENTS.md but missing in code (stale docs? planned rows are allowed only with status=planned):" >&2
    echo "${MISSING_IN_CODE}" | sed 's/^/      /' >&2
    FAIL=1
  fi
fi
[[ ${FAIL} -eq 0 ]] && echo "    OK: notification.* actions in sync (${#CODE[@]} actions)"

echo "==> NF-02: transition matrix snapshots (go test)"
SNAPSHOT_TESTS=(
  "TestTransitionMatrixSnapshot|./internal/notification/domain"
  "TestDeliveryTransitionMatrixSnapshot|./internal/notification/domain"
)
for entry in "${SNAPSHOT_TESTS[@]}"; do
  test_name="${entry%%|*}"
  pkg="${entry##*|}"
  if go test -run "^${test_name}$" "${pkg}" >/dev/null 2>&1; then
    echo "    OK: ${test_name}"
  else
    echo "    ERROR: transition matrix changed - update the snapshot in notification_test.go" >&2
    FAIL=1
  fi
done

echo "==> NF-03: channel driver import surface (ADR-0008 §3)"
# 允许引用 pkg 渠道驱动的白名单：
#   - notification/infrastructure/channel：编排适配层（唯一合法的封装点）
#   - identity 验证码旁路：ADR-0008 §3 的受控例外（NT-10 收敛后仍保留同步语义）
#   - cmd/*：装配层（渠道驱动构造注入）
VIOLATIONS=$(grep -rlE '"github.com/admin-frame/admin-frame/pkg/(sms|email|push)"' internal/ --include='*.go' 2>/dev/null \
  | grep -v _test \
  | grep -v 'internal/notification/infrastructure/channel/' \
  | grep -v 'internal/identity/' \
  || true)
if [[ -n "${VIOLATIONS}" ]]; then
  echo "    ERROR: business domains must not import channel drivers directly (ADR-0008: outbox events only):" >&2
  echo "${VIOLATIONS}" | sed 's/^/      /' >&2
  FAIL=1
else
  echo "    OK: channel drivers only referenced by adapter / identity bypass / cmd"
fi

if [[ ${FAIL} -ne 0 ]]; then
  echo "FAIL: check-notification" >&2
  exit 1
fi
echo "OK: notification"
