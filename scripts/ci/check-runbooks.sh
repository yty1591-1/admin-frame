#!/usr/bin/env bash
# RUNBOOK_STANDARDS：基线 runbook、必备章节、ALERTS 目录链接。
# 用法: bash scripts/ci/check-runbooks.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
RB_DIR="docs/runbooks"
REQUIRED_RB=(api-5xx-spike.md db-connection-pool.md release-rollback.md)
REQUIRED_SECTIONS=('## 症状' '## 影响' '## 排查' '## 缓解与恢复' '## 升级' '## 相关链接')

echo "==> RB-01/04: baseline runbooks exist"
if [[ ! -d "${RB_DIR}" ]]; then
  echo "ERROR: missing ${RB_DIR}/ (RB-01)" >&2
  exit 1
fi
for f in "${REQUIRED_RB[@]}"; do
  path="${RB_DIR}/${f}"
  if [[ ! -f "${path}" ]]; then
    echo "ERROR: missing ${path} (RB-04)" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> RB-02: required sections in non-exempt runbooks"
shopt -s nullglob
for path in "${RB_DIR}"/*.md; do
  base="$(basename "${path}")"
  case "${base}" in
    README.md|ALERTS.md|_TEMPLATE.md) continue ;;
  esac
  if [[ ! "${base}" =~ ^[a-z0-9]+(-[a-z0-9]+)*\.md$ ]]; then
    echo "ERROR: runbook name must be kebab-case.md: ${base} (RB-01)" >&2
    FAIL=1
  fi
  for sec in "${REQUIRED_SECTIONS[@]}"; do
    if ! grep -qF "${sec}" "${path}"; then
      echo "ERROR: ${path} missing section '${sec}' (RB-02)" >&2
      FAIL=1
    fi
  done
done
shopt -u nullglob
echo "    OK: section scan done"

echo "==> RB-03: ALERTS.md P1/P2 rows must link to runbooks"
ALERTS="${RB_DIR}/ALERTS.md"
if [[ ! -f "${ALERTS}" ]]; then
  echo "ERROR: missing ${ALERTS} (RB-03)" >&2
  FAIL=1
else
  # Parse table rows containing | P1 | or | P2 |
  while IFS= read -r line; do
    [[ "${line}" == *"|"* ]] || continue
    [[ "${line}" == *"P1"* || "${line}" == *"P2"* ]] || continue
    [[ "${line}" == *"级别"* ]] && continue
    [[ "${line}" == *"|---"* ]] && continue
    if ! printf '%s' "${line}" | grep -qE '\[[^]]+\]\(\./[^)]+\.md\)'; then
      echo "ERROR: P1/P2 row missing runbook link: ${line} (RB-03)" >&2
      FAIL=1
      continue
    fi
    # Extract first markdown link target ./file.md
    target="$(printf '%s' "${line}" | sed -n 's/.*](\.\/\([^)]*\.md\)).*/\1/p' | head -1)"
    if [[ -n "${target}" && ! -f "${RB_DIR}/${target}" ]]; then
      echo "ERROR: ALERTS link missing file: ${RB_DIR}/${target} (RB-03)" >&2
      FAIL=1
    fi
  done < "${ALERTS}"
  echo "    OK: ALERTS.md scanned"
fi

if [[ -f docs/RUNBOOK_STANDARDS.md ]]; then
  echo "    OK: docs/RUNBOOK_STANDARDS.md"
else
  echo "ERROR: missing docs/RUNBOOK_STANDARDS.md" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "runbooks check failed (RUNBOOK_STANDARDS)" >&2
  exit 1
fi
echo "OK: runbooks"
