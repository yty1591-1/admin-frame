#!/usr/bin/env bash
# ENT-17 / RELS-09：发布前置的迁移门禁。
#
# 目标：把 DATA-05（expand → 代码 → contract）从「评审记忆」变成发布流水线里
# 的可执行断言——迁移一旦违反 expand 规则，**发布直接不开始**，而不是等
# 上线后由回滚补救。
#
# 检查项：
#   MG-01 文件命名与配对：NNNNNN_name.up.sql / .down.sql 成对，无其它文件
#   MG-02 序号从 000001 起连续、不重号
#   MG-03 相对基线不得删除或改名既有迁移（迁移是追加日志）
#   MG-04 新增 up 迁移默认 expand-only（禁止 DROP/RENAME/CHANGE/TRUNCATE）；
#         显式声明 `-- CONTRACT: <已停写依据>` 的才允许破坏性 DDL（DATA-05 收口阶段）
#   MG-05 up 迁移中 ADD COLUMN ... NOT NULL 必须带 DEFAULT（否则旧代码写入即失败）
#   MG-06 down 迁移必须存在且非空，或显式声明 `-- IRREVERSIBLE:` 与处置说明
#   MG-07 提供 MIGRATE_DSN 时探测 schema 状态：dirty=false 才允许发布
#
# 用法：
#   bash scripts/ci/check-migration-gate.sh [--base <git-ref>] [--stage pre|post]
#
# 环境变量：
#   MIGRATION_BASE_REF  基线 ref（默认 main → origin/main → 上一个 tag）
#   MIGRATE_DSN         MySQL DSN；设置后启用 MG-07（不设则只做静态检查）
#   MIGRATE_BIN         迁移二进制路径（默认 bin/migrate，缺失时用 go run）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

BASE_REF="${MIGRATION_BASE_REF:-}"
STAGE="pre"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      BASE_REF="${2:-}"
      shift 2
      ;;
    --stage)
      STAGE="${2:-}"
      shift 2
      ;;
    -h|--help)
      sed -n '2,30p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done
if [[ "${STAGE}" != "pre" && "${STAGE}" != "post" ]]; then
  echo "invalid --stage '${STAGE}' (want pre|post)" >&2
  exit 2
fi

if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi

FAIL=0
MIG_DIR="migrations"
fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

resolve_base() {
  if [[ -n "${BASE_REF}" ]]; then
    echo "${BASE_REF}"
    return 0
  fi
  local cand
  for cand in main origin/main master origin/master; do
    if git rev-parse --verify --quiet "${cand}^{commit}" >/dev/null; then
      echo "${cand}"
      return 0
    fi
  done
  # 发布流水线常以「上一个 tag」为基线：没有分支基线时退化为最近 tag。
  local tag
  tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  if [[ -n "${tag}" ]]; then
    echo "${tag}"
    return 0
  fi
  return 1
}

# strip_sql 去掉 SQL 行注释与块注释：注释里提到 DROP 是在解释规则，
# 不能当成破坏性语句（否则门禁会逼着大家删掉说明）。
strip_sql() {
  sed -E 's/--.*$//' "$1" | sed -E ':a;N;$!ba;s@/\*([^*]|\*+[^*/])*\*+/@@g'
}

echo "==> MG-01: migration file naming and up/down pairing"
mapfile -t FILES < <(for f in "${MIG_DIR}"/*.sql; do [[ -e "${f}" ]] && basename "${f}"; done | sort)
if [[ "${#FILES[@]}" -eq 0 ]]; then
  echo "ERROR: no migration files under ${MIG_DIR}" >&2
  exit 1
fi

declare -A UP=() DOWN=()
for f in "${FILES[@]}"; do
  if [[ ! "${f}" =~ ^([0-9]{6})_[a-z0-9_]+\.(up|down)\.sql$ ]]; then
    fail "invalid migration filename '${f}' (want NNNNNN_name.up.sql / .down.sql)"
    continue
  fi
  num="${BASH_REMATCH[1]}"
  kind="${BASH_REMATCH[2]}"
  if [[ "${kind}" == "up" ]]; then
    UP["${num}"]="${f}"
  else
    DOWN["${num}"]="${f}"
  fi
done

for num in "${!UP[@]}"; do
  if [[ -z "${DOWN[${num}]:-}" ]]; then
    fail "migration ${num} has no .down.sql (${UP[${num}]})"
  fi
done
for num in "${!DOWN[@]}"; do
  if [[ -z "${UP[${num}]:-}" ]]; then
    fail "migration ${num} has no .up.sql (${DOWN[${num}]})"
  fi
done
echo "    migrations: ${#UP[@]} up / ${#DOWN[@]} down"

echo "==> MG-02: sequence starts at 000001 and has no gaps"
mapfile -t NUMBERS < <(printf '%s\n' "${!UP[@]}" | sort)
expect=1
for num in "${NUMBERS[@]}"; do
  want="$(printf '%06d' "${expect}")"
  if [[ "${num}" != "${want}" ]]; then
    fail "migration sequence gap: expected ${want}, found ${num}"
    break
  fi
  expect=$((expect + 1))
done
echo "    sequence: ${NUMBERS[0]}..${NUMBERS[${#NUMBERS[@]}-1]}"

BASE=""
if BASE="$(resolve_base)"; then
  echo "==> base ref: ${BASE}"
else
  echo "WARN: no git base ref; only static checks are enforced (MG-03/MG-04 skipped)" >&2
  BASE=""
fi

if [[ -n "${BASE}" ]]; then
  echo "==> MG-03: existing migrations must not be deleted or renamed"
  removed="$(git diff --name-status --diff-filter=DR "${BASE}" -- "${MIG_DIR}" || true)"
  if [[ -n "${removed}" ]]; then
    echo "${removed}" >&2
    fail "migrations are append-only: deleting or renaming breaks already-migrated environments (DATA-05)"
  else
    echo "    OK: no deletions/renames"
  fi

  echo "==> MG-04: new up migrations must be expand-only (or marked CONTRACT)"
  mapfile -t NEW_UP < <(git diff --name-only --diff-filter=A "${BASE}" -- "${MIG_DIR}" | grep -E '\.up\.sql$' || true)
  if [[ "${#NEW_UP[@]}" -eq 0 ]]; then
    echo "    OK: no new up migration in this change"
  fi
  for f in "${NEW_UP[@]}"; do
    echo "    checking ${f}"
    body="$(strip_sql "${f}")"
    # 破坏性 DDL：DROP / RENAME / CHANGE / TRUNCATE。
    destructive="$(printf '%s\n' "${body}" |
      grep -inE '\b(DROP[[:space:]]+(TABLE|COLUMN|DATABASE|SCHEMA)|RENAME[[:space:]]+(TABLE|COLUMN)|CHANGE[[:space:]]+COLUMN|TRUNCATE([[:space:]]+TABLE)?)\b' || true)"
    if [[ -n "${destructive}" ]]; then
      if grep -qiE '^--[[:space:]]*CONTRACT:' "${f}"; then
        echo "    OK: ${f} declared as CONTRACT (DATA-05)"
      else
        printf '%s\n' "${destructive}" | sed 's/^/      /' >&2
        fail "${f}: destructive DDL in an expand migration (split into expand → code → contract, or declare '-- CONTRACT: <依据>')"
      fi
    fi

    echo "==> MG-05: ADD COLUMN NOT NULL requires DEFAULT (${f})"
    while IFS= read -r line; do
      [[ -z "${line}" ]] && continue
      if ! grep -qiE '\bDEFAULT\b' <<<"${line}"; then
        echo "      ${line}" >&2
        fail "${f}: ADD COLUMN ... NOT NULL without DEFAULT breaks old writers; add DEFAULT or split into expand + contract"
      fi
    done < <(printf '%s\n' "${body}" | grep -iE '\bADD[[:space:]]+COLUMN\b' | grep -iE '\bNOT[[:space:]]+NULL\b' || true)
  done
fi

echo "==> MG-06: down migration must be reversible or explicitly irreversible"
for num in "${!UP[@]}"; do
  down="${DOWN[${num}]:-}"
  [[ -z "${down}" ]] && continue
  if [[ ! -s "${MIG_DIR}/${down}" ]]; then
    fail "${MIG_DIR}/${down} is empty: provide a real rollback or declare '-- IRREVERSIBLE: <原因>'"
    continue
  fi
  # 去掉注释后仍无语句 → 必须有 IRREVERSIBLE 声明（含处置说明）。
  if [[ -z "$(strip_sql "${MIG_DIR}/${down}" | tr -d '[:space:];')" ]]; then
    if ! grep -qiE '^--[[:space:]]*IRREVERSIBLE:' "${MIG_DIR}/${down}"; then
      fail "${MIG_DIR}/${down} has no statements and no '-- IRREVERSIBLE:' declaration"
    fi
  fi
done
echo "    OK: every down migration is explicit"

if [[ -n "${MIGRATE_DSN:-}" ]]; then
  echo "==> MG-07: schema status probe (stage=${STAGE})"
  MIGRATE_BIN="${MIGRATE_BIN:-bin/migrate}"
  if [[ -x "${MIGRATE_BIN}" || -x "${MIGRATE_BIN}.exe" ]]; then
    bin_path="${MIGRATE_BIN}"
    [[ -x "${bin_path}" ]] || bin_path="${MIGRATE_BIN}.exe"
    status_out="$("${bin_path}" -action status -driver mysql -dsn "${MIGRATE_DSN}" -dir "${MIG_DIR}" 2>&1 || true)"
  else
    status_out="$(go run ./cmd/migrate -action status -driver mysql -dsn "${MIGRATE_DSN}" -dir "${MIG_DIR}" 2>&1 || true)"
  fi
  echo "${status_out}" | sed 's/^/    /'
  # printStatus 的稳定输出：`dirty: false` / `pending: none` 或 `pending:` 后跟缩进列表。
  if ! grep -qE '^dirty:[[:space:]]*false$' <<<"${status_out}"; then
    fail "schema is dirty or status unreadable; refusing to release (MG-07)"
  fi
  if [[ "${STAGE}" == "post" ]] && ! grep -qE '^pending:[[:space:]]*none$' <<<"${status_out}"; then
    fail "schema still has pending migrations after deploy (MG-07 stage=post)"
  fi
else
  echo "==> MG-07: skipped (MIGRATE_DSN not set; static checks only)"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "migration gate failed (ENT-17 / RELS-09)" >&2
  exit 1
fi
echo "OK: migration gate"
