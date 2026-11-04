#!/usr/bin/env bash
# ENT-11：特性开关与灰度门禁（feature-flag-plan.md §2.G）。
#
# 四项断言，防止「flag 体系存在但治理缺位」：
#   FF-01 key 命名规范：domain 正则存在且有测试锁定
#   FF-02 审计动作 ↔ EVENTS.md 双向对账（featureflag.* 段）
#   FF-03 幽灵 key 扫描：代码中的 flag key 字面量必须来自登记的常量
#         （防自由字符串散落——feature-flag-plan.md 风险 #4）
#   FF-04 到期治理：非 permanent 必带 expires_at 的守卫存在
#         （ErrExpiryRequired）+ 到期任务实现存在（ListExpired）
#
# 用法: bash scripts/ci/check-featureflag.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "    ERROR: $1" >&2; FAIL=1; }

echo "==> FF-01: flag key naming guard"
grep -q 'keyPattern = regexp.MustCompile' internal/featureflag/domain/flag.go \
  || fail "flag key regex missing in domain"
grep -q "TestNewFlagValidation\|ErrKeyInvalid" internal/featureflag/domain/flag_test.go \
  || fail "flag key naming tests missing"
if [[ $FAIL -eq 0 ]]; then echo "    OK: key regex + validation tests"; fi

echo "==> FF-02: audit actions <-> EVENTS.md"
mapfile -t CODE < <(
  grep -rhoE 'ActionFlag[A-Za-z]+ *= *"featureflag\.[a-z_.]+"' internal/featureflag/app/ --include='*.go' 2>/dev/null \
    | grep -v _test | sed -E 's/.*"(featureflag\.[a-z_.]+)".*/\1/' | sort -u
)
mapfile -t DOC < <(grep -o '`featureflag\.[a-z_.]*`' docs/audit/EVENTS.md | tr -d '`' | sort -u)
if [[ ${#CODE[@]} -eq 0 ]]; then
  fail "no featureflag.* action constants found in internal/featureflag/app"
elif [[ ${#DOC[@]} -eq 0 ]]; then
  fail "no featureflag.* actions found in docs/audit/EVENTS.md"
else
  MISSING_DOC=0
  for action in "${CODE[@]}"; do
    if ! printf '%s\n' "${DOC[@]}" | grep -qx "$action"; then
      fail "code action missing in EVENTS.md: $action"
      MISSING_DOC=1
    fi
  done
  for action in "${DOC[@]}"; do
    found=0
    for c in "${CODE[@]}"; do
      [[ "$c" == "$action" ]] && found=1
    done
    if [[ $found -eq 0 ]]; then
      fail "EVENTS.md action has no code constant: $action"
      MISSING_DOC=1
    fi
  done
  if [[ $MISSING_DOC -eq 0 ]]; then
    echo "    OK: ${#CODE[@]} actions reconciled both ways"
  fi
fi

echo "==> FF-03: ghost flag key scan"
# flag key 字面量必须经由常量登记（tenant 域 FlagInvitationEmail 是范例）；
# 直接字面量引用（除常量定义行与测试外）视为幽灵 key。
GHOSTS=$(grep -rn '"[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*"' internal/ --include='*.go' 2>/dev/null \
  | grep -v _test \
  | grep -E 'IsEnabled\(|Evaluate|featureflag' \
  | grep -v 'FlagInvitationEmail\|featureflag\.' \
  | grep -vE '(module\.feature|example|test|placeholder)' || true)
if [[ -n "$GHOSTS" ]]; then
  fail "ghost flag key literals found (register constants instead):"
  echo "$GHOSTS" | head -5 >&2
else
  echo "    OK: no ghost flag key literals"
fi

echo "==> FF-04: expiry governance"
grep -q "ErrExpiryRequired" internal/featureflag/domain/flag.go \
  || fail "non-permanent expiry guard missing (ErrExpiryRequired)"
grep -q "ListExpired" internal/featureflag/domain/repository.go \
  || fail "expiry task data source missing (ListExpired)"
grep -q "func (s \*ExpireService) Run" internal/featureflag/app/expire.go \
  || fail "expire task implementation missing"
grep -q "featureflag.expire" docs/audit/EVENTS.md \
  || fail "expire meta-audit missing in EVENTS.md"
if [[ $FAIL -eq 0 ]]; then
  echo "    OK: expiry guards + task + meta-audit present"
fi

if [[ $FAIL -ne 0 ]]; then
  echo "check-featureflag: FAILED" >&2
  exit 1
fi
echo "check-featureflag: OK"
