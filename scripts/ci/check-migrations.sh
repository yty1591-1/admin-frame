#!/usr/bin/env bash
# DATA_STANDARDS：迁移命名、up/down 配对、禁止业务进程自动 migrate。
# 用法: bash scripts/ci/check-migrations.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
MIG_DIR="migrations"

echo "==> DATA-01/02: migration naming and up/down pairs"
if [[ ! -d "${MIG_DIR}" ]]; then
  echo "ERROR: missing ${MIG_DIR}/ (DATA-01)" >&2
  exit 1
fi

shopt -s nullglob
ups=("${MIG_DIR}"/*.up.sql)
downs=("${MIG_DIR}"/*.down.sql)
other=("${MIG_DIR}"/*)
shopt -u nullglob

if [[ "${#ups[@]}" -eq 0 ]]; then
  echo "ERROR: no ${MIG_DIR}/*.up.sql found (DATA-01)" >&2
  FAIL=1
fi

for f in "${other[@]}"; do
  [[ -f "${f}" ]] || continue
  base="$(basename "${f}")"
  if [[ "${base}" == *.up.sql || "${base}" == *.down.sql ]]; then
    continue
  fi
  if [[ "${base}" == README.md || "${base}" == .gitkeep ]]; then
    continue
  fi
  echo "ERROR: unexpected file in migrations/: ${base} (only *.up.sql / *.down.sql) (DATA-01)" >&2
  FAIL=1
done

declare -A UP_BASES=()
for f in "${ups[@]}"; do
  base="$(basename "${f}")"
  if [[ ! "${base}" =~ ^[0-9]{6}_[a-z0-9_]+\.up\.sql$ ]]; then
    echo "ERROR: bad up name (want NNNNNN_desc.up.sql): ${base} (DATA-01)" >&2
    FAIL=1
    continue
  fi
  stem="${base%.up.sql}"
  UP_BASES["${stem}"]=1
  down="${MIG_DIR}/${stem}.down.sql"
  if [[ ! -f "${down}" ]]; then
    echo "ERROR: missing down for ${base} (DATA-02)" >&2
    FAIL=1
  else
    echo "    OK: ${base} <-> ${stem}.down.sql"
  fi
done

for f in "${downs[@]}"; do
  base="$(basename "${f}")"
  if [[ ! "${base}" =~ ^[0-9]{6}_[a-z0-9_]+\.down\.sql$ ]]; then
    echo "ERROR: bad down name: ${base} (DATA-01)" >&2
    FAIL=1
    continue
  fi
  stem="${base%.down.sql}"
  if [[ -z "${UP_BASES[${stem}]+x}" ]]; then
    echo "ERROR: orphan down without up: ${base} (DATA-02)" >&2
    FAIL=1
  fi
done

echo "==> DATA-03: forbid auto-migrate in API/worker mains"
# 业务进程启动路径禁止迁移执行器 / ORM AutoMigrate。
PATTERN='AutoMigrate|migrate\.Up|migrate\.Down|MustMigrate|RunMigration|\.Up\(ctx|\.Up\(context'
SEARCH_PATHS=(cmd/admin-api cmd/biz-api cmd/c-api cmd/worker)
for p in "${SEARCH_PATHS[@]}"; do
  if [[ ! -d "${p}" ]]; then
    continue
  fi
  if grep -RIn --include='*.go' -E "${PATTERN}" "${p}" >/dev/null 2>&1; then
    echo "ERROR: auto-migrate pattern found under ${p} (DATA-03):" >&2
    grep -RIn --include='*.go' -E "${PATTERN}" "${p}" >&2 || true
    FAIL=1
  else
    echo "    OK: ${p}"
  fi
done

# 额外：非 migrate 包不应 import golang-migrate 执行 API（允许注释提及）。
if grep -RIn --include='*.go' -E 'github.com/golang-migrate/migrate' cmd/admin-api cmd/biz-api cmd/c-api cmd/worker >/dev/null 2>&1; then
  echo "ERROR: golang-migrate imported outside cmd/migrate (DATA-03)" >&2
  grep -RIn --include='*.go' -E 'github.com/golang-migrate/migrate' cmd/admin-api cmd/biz-api cmd/c-api cmd/worker >&2 || true
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "migration check failed (DATA_STANDARDS)" >&2
  exit 1
fi
echo "OK: migrations"
