#!/usr/bin/env bash
# FE-SEC-01：前端 XSS 出口巡检（FE-S-01）
#
# 扫描 frontend/apps 与 frontend/packages 的 .ts/.tsx/.vue 源码，拦截：
#   v-html=            （未净化的 HTML 注入）
#   innerHTML =        （DOM HTML 注入）
#   eval( / new Function(  （动态代码执行，不允许豁免）
#   window.open( 缺 noopener/noreferrer（反向钓鱼）
#
# 例外只能通过 scripts/ci/frontend-security-allowlist.txt 登记（带原因与到期日）；
# 条目过期、指向不存在的文件或没有命中，都会失败。
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

ALLOWLIST="scripts/ci/frontend-security-allowlist.txt"
TODAY="$(date +%F)"
errors=0
findings="$(mktemp)"
trap 'rm -f "$findings"' EXIT

fail() {
  echo "ERROR: $*"
  errors=$((errors + 1))
}

allow_paths=()
allow_rules=()

echo "==> FE-SEC-01: frontend XSS sinks must be allowlisted"

if [[ ! -f "$ALLOWLIST" ]]; then
  fail "allowlist missing: $ALLOWLIST"
else
  while IFS='|' read -r path rule reason expires; do
    [[ -z "${path// /}" || "${path:0:1}" == "#" ]] && continue
    if [[ -z "$rule" || -z "$reason" || -z "$expires" ]]; then
      fail "allowlist entry needs path|rule|reason|expires: $path"
      continue
    fi
    if [[ ! "$expires" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
      fail "allowlist entry has invalid expiry ($expires): $path"
      continue
    fi
    if [[ "$expires" < "$TODAY" ]]; then
      fail "allowlist entry expired on $expires: $path ($rule)"
      continue
    fi
    case "$rule" in
      v-html | innerHTML | window-open) ;;
      eval)
        fail "eval/new Function cannot be allowlisted: $path"
        continue
        ;;
      *)
        fail "allowlist entry has unknown rule '$rule': $path"
        continue
        ;;
    esac
    if [[ ! -f "$path" ]]; then
      fail "allowlist entry points to a missing file: $path"
      continue
    fi
    allow_paths+=("$path")
    allow_rules+=("$rule")
  done <"$ALLOWLIST"
fi

sinks="$(git ls-files 'frontend/apps/**' 'frontend/packages/**' |
  grep -E '\.(ts|tsx|vue)$' |
  grep -vE '(__tests__|\.test\.)' || true)"

# 批量扫描（逐文件 grep 在 Git Bash 下每次 fork 很慢；这里每类 sink 只起一次 grep）。
collect() {
  local pattern="$1" rule="$2"
  printf '%s\n' "$sinks" | xargs -r grep -HnE "$pattern" 2>/dev/null |
    cut -d: -f1 | sort -u | sed "s#\$#|${rule}#" || true
}

{
  collect 'v-html=' 'v-html'
  collect 'innerHTML[[:space:]]*=' 'innerHTML'
  collect '(^|[^[:alnum:]_$])eval\(|new[[:space:]]+Function[[:space:]]*\(' 'eval'
  printf '%s\n' "$sinks" | xargs -r grep -HnE 'window\.open\(' 2>/dev/null |
    grep -vE 'noopener|noreferrer' | cut -d: -f1 | sort -u | sed 's#$#|window-open#' || true
} >"$findings"

checked="$(printf '%s\n' "$sinks" | grep -c . || true)"
findings_total="$(grep -c . <"$findings" || true)"

while IFS='|' read -r path rule; do
  [[ -z "$path" ]] && continue
  matched=0
  for i in "${!allow_paths[@]}"; do
    if [[ "${allow_paths[$i]}" == "$path" && "${allow_rules[$i]}" == "$rule" ]]; then
      matched=1
      break
    fi
  done
  if [[ "$matched" == "0" ]]; then
    fail "unallowlisted $rule sink: $path"
  fi
done <"$findings"

for i in "${!allow_paths[@]}"; do
  if ! grep -Fqx "${allow_paths[$i]}|${allow_rules[$i]}" "$findings"; then
    fail "stale allowlist entry (no matching hit): ${allow_paths[$i]} (${allow_rules[$i]})"
  fi
done

echo "    checked $checked files, hits $findings_total, allowlisted ${#allow_paths[@]}"

if [[ "$errors" -gt 0 ]]; then
  echo "frontend security check failed (FE-SEC-01)"
  exit 1
fi
echo "OK: frontend XSS sinks (no unallowlisted hits, ${#allow_paths[@]} exceptions)"