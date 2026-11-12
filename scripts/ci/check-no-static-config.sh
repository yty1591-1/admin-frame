#!/usr/bin/env bash
# CFG-01 防回退：仓库不得再跟踪任何运行时静态配置。
#
# 背景：配置中心接管后，业务进程只认「配置中心 > 代码默认值」，configs/ 下不应再有运行时文件。
# 迁移期仍保留少量 *.example 作为只读参考，登记在 scripts/ci/configs-legacy-baseline.txt；
# 基线只减不增，M1 / M2 / M3 每删除一个文件就同步从基线移除，清空后本脚本仅做「零跟踪」校验。
#
# 检查项：
#   CFG-01a 被跟踪的 configs/** 必须全部是 *.example（禁止运行时 yaml / env / json / toml）
#   CFG-01b 被跟踪的 configs/** 必须 ⊆ 迁移期基线（禁止新增样例；删除文件须同步收缩基线）
#   CFG-01c configs/** 下不得存在未忽略的未跟踪文件（防止本地配置误入库）
#   CFG-06  基线样例中的 dsn 必须为空或占位符（禁止真实连接串）
#
# 用法: bash scripts/ci/check-no-static-config.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

BASELINE="scripts/ci/configs-legacy-baseline.txt"
FAIL=0

fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "ERROR: not a git work tree; CFG-01 需要 git 跟踪信息（请在仓库根执行）" >&2
  exit 1
fi

tracked="$(git ls-files 'configs' | LC_ALL=C sort -u || true)"

echo "==> CFG-01a: tracked configs must be examples only"
if [[ -z "${tracked}" ]]; then
  echo "    OK: no tracked files under configs/"
else
  bad="$(printf '%s\n' "${tracked}" | grep -Ev '\.example$' || true)"
  if [[ -n "${bad}" ]]; then
    fail "tracked runtime config files are forbidden (CFG-01):"
    printf '%s\n' "${bad}" >&2
  else
    echo "    OK: all tracked configs are *.example"
  fi
fi

echo "==> CFG-01b: no new config files beyond the legacy baseline"
if [[ ! -f "${BASELINE}" ]]; then
  fail "missing ${BASELINE} (CFG-01 防回退基线)"
else
  allowed="$(grep -Ev '^[[:space:]]*(#|$)' "${BASELINE}" | LC_ALL=C sort -u)"
  added="$(comm -23 <(printf '%s\n' "${tracked}") <(printf '%s\n' "${allowed}") || true)"
  if [[ -n "${added}" ]]; then
    fail "new files under configs/ are forbidden (CFG-01); move the value to config center key spec:"
    printf '%s\n' "${added}" >&2
  else
    echo "    OK: tracked configs are within the legacy baseline"
  fi
  removed="$(comm -13 <(printf '%s\n' "${tracked}") <(printf '%s\n' "${allowed}") || true)"
  if [[ -n "${removed}" ]]; then
    echo "    INFO: deleted files still listed in ${BASELINE}; shrink the baseline in the same MR:"
    while IFS= read -r f; do
      [[ -n "${f}" ]] && echo "        ${f}"
    done <<< "${removed}"
  fi
fi

echo "==> CFG-01c: no untracked, non-ignored files under configs/"
untracked="$(git ls-files --others --exclude-standard 'configs' || true)"
if [[ -n "${untracked}" ]]; then
  fail "untracked files under configs/ must not be committed (CFG-01):"
  printf '%s\n' "${untracked}" >&2
else
  echo "    OK: no pending files under configs/"
fi

echo "==> CFG-06: examples must not embed real DSN secrets"
while IFS= read -r f; do
  [[ -n "${f}" && -f "${f}" ]] || continue
  while IFS= read -r line; do
    [[ "${line}" =~ ^[[:space:]]*# ]] && continue
    if [[ "${line}" =~ ^[[:space:]]*dsn:[[:space:]]*(.*)$ ]]; then
      val="${BASH_REMATCH[1]}"
      val="${val%% #*}"
      val="${val%\"}"
      val="${val#\"}"
      val="${val%\'}"
      val="${val#\'}"
      val="${val#"${val%%[![:space:]]*}"}"
      val="${val%"${val##*[![:space:]]}"}"
      [[ -z "${val}" ]] && continue
      lower="$(printf '%s' "${val}" | tr '[:upper:]' '[:lower:]')"
      if printf '%s' "${lower}" | grep -Eq 'changeme|placeholder|your-|example|todo|<dsn>|\$\{'; then
        continue
      fi
      fail "${f}: dsn must be empty or placeholder in example (CFG-06): ${line}"
    fi
  done < "${f}"
done <<< "${tracked}"
echo "    OK: example dsn policy checked"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "static config check failed (CFG-01 / CFG-06)" >&2
  exit 1
fi
echo "OK: no static config"
