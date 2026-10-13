#!/usr/bin/env bash
# LICENSE_STANDARDS：允许/禁止列表存在、非空、互斥；NOTICE 存在。
# 用法: bash scripts/ci/check-license.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
ALLOW="deps/allowed-licenses.txt"
FORBID="deps/forbidden-licenses.txt"
NOTICE="deps/NOTICE"

echo "==> LIC-01/02: license list files"
for f in "${ALLOW}" "${FORBID}" docs/LICENSE_STANDARDS.md; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f}" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> LIC-01: allowed list non-empty"
allow_count=0
if [[ -f "${ALLOW}" ]]; then
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]] && continue
    allow_count=$((allow_count + 1))
  done < "${ALLOW}"
fi
if [[ "${allow_count}" -eq 0 ]]; then
  echo "ERROR: ${ALLOW} has no entries (LIC-01)" >&2
  FAIL=1
else
  echo "    OK: ${allow_count} allowed licenses"
fi

echo "==> LIC-02: forbidden list non-empty"
forbid_count=0
declare -A FORBIDDEN=()
if [[ -f "${FORBID}" ]]; then
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]] && continue
    forbid_count=$((forbid_count + 1))
    FORBIDDEN["${line}"]=1
  done < "${FORBID}"
fi
if [[ "${forbid_count}" -eq 0 ]]; then
  echo "ERROR: ${FORBID} has no entries (LIC-02)" >&2
  FAIL=1
else
  echo "    OK: ${forbid_count} forbidden licenses"
fi

echo "==> LIC-02: allow and forbid must not overlap"
if [[ -f "${ALLOW}" ]]; then
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]] && continue
    if [[ -n "${FORBIDDEN[${line}]+x}" ]]; then
      echo "ERROR: ${line} is both allowed and forbidden (LIC-02)" >&2
      FAIL=1
    fi
  done < "${ALLOW}"
fi
echo "    OK: overlap scan done"

echo "==> LIC-03: forbidden baseline names present"
for name in GPL-3.0 AGPL-3.0 SSPL unknown; do
  if [[ -z "${FORBIDDEN[${name}]+x}" ]]; then
    echo "ERROR: ${FORBID} must include ${name} (LIC-03)" >&2
    FAIL=1
  else
    echo "    OK: forbidden ${name}"
  fi
done

echo "==> LIC-06: NOTICE present"
if [[ ! -f "${NOTICE}" ]]; then
  echo "ERROR: missing ${NOTICE} (LIC-06)" >&2
  FAIL=1
else
  echo "    OK: ${NOTICE}"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "license check failed (LICENSE_STANDARDS)" >&2
  exit 1
fi
echo "OK: license"
