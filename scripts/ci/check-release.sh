#!/usr/bin/env bash
# RELEASE_STANDARDS：Dockerfile 禁 latest、HEALTHCHECK、版本注入与 Checklist。
# 用法: bash scripts/ci/check-release.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
DF="Dockerfile"

echo "==> RELS-01: Dockerfile must not use :latest"
if [[ ! -f "${DF}" ]]; then
  echo "ERROR: missing ${DF}" >&2
  exit 1
fi
bad_latest=0
while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  content="${line#*:}"
  trimmed="${content#"${content%%[![:space:]]*}"}"
  if [[ "${trimmed}" == \#* ]]; then
    continue
  fi
  echo "ERROR: :latest in Dockerfile (RELS-01): ${line}" >&2
  bad_latest=1
done <<< "$(grep -nE ':latest' "${DF}" || true)"
if [[ "${bad_latest}" -ne 0 ]]; then
  FAIL=1
else
  echo "    OK: no :latest"
fi

echo "==> RELS-02: HEALTHCHECK present"
if grep -qE '^[[:space:]]*HEALTHCHECK' "${DF}"; then
  echo "    OK: HEALTHCHECK"
else
  echo "ERROR: Dockerfile missing HEALTHCHECK (RELS-02)" >&2
  FAIL=1
fi
if ! grep -q 'healthz' "${DF}"; then
  echo "ERROR: HEALTHCHECK should target /healthz (RELS-02)" >&2
  FAIL=1
else
  echo "    OK: healthz referenced"
fi

echo "==> RELS-03: build version ARG / ldflags wiring"
for arg in VERSION COMMIT BUILD_TIME; do
  if ! grep -q "ARG ${arg}" "${DF}"; then
    echo "ERROR: Dockerfile missing ARG ${arg} (RELS-03)" >&2
    FAIL=1
  else
    echo "    OK: ARG ${arg}"
  fi
done
if ! grep -qE '\-X[[:space:]]*main\.version|main\.version=' "${DF}"; then
  echo "ERROR: Dockerfile should inject -X main.version (RELS-03)" >&2
  FAIL=1
else
  echo "    OK: ldflags version injection"
fi

WIRING="internal/platform/wiring.go"
if [[ -f "${WIRING}" ]] && grep -q 'BuildInfo' "${WIRING}" && grep -q 'build_time' "${WIRING}"; then
  echo "    OK: /version exposes build info"
else
  echo "ERROR: MountVersion must expose commit/build_time via BuildInfo (RELS-03)" >&2
  FAIL=1
fi

echo "==> RELS-07: release checklist present"
if [[ -f docs/release/CHECKLIST.md ]]; then
  echo "    OK: docs/release/CHECKLIST.md"
else
  echo "ERROR: missing docs/release/CHECKLIST.md (RELS-07)" >&2
  FAIL=1
fi

if [[ -f docs/RELEASE_STANDARDS.md ]]; then
  echo "    OK: docs/RELEASE_STANDARDS.md"
else
  echo "ERROR: missing docs/RELEASE_STANDARDS.md" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "release check failed (RELEASE_STANDARDS)" >&2
  exit 1
fi
echo "OK: release"
