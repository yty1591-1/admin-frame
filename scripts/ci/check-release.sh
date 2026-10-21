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

ENDPOINTS="internal/platform/endpoints.go"
if [[ -f "${ENDPOINTS}" ]] && grep -q 'BuildInfo' "${ENDPOINTS}" && grep -q 'build_time' "${ENDPOINTS}"; then
  echo "    OK: /version exposes build info"
else
  echo "ERROR: MountVersion must expose commit/build_time via BuildInfo (RELS-03)" >&2
  FAIL=1
fi

echo "==> RELS-01/03/06: release image pipeline"
IMG_WF=".github/workflows/image.yml"
if [[ ! -f "${IMG_WF}" ]]; then
  echo "ERROR: missing ${IMG_WF} (发版镜像流水线，见 RELEASE_STANDARDS §3/§6)" >&2
  FAIL=1
else
  if grep -nE ':latest' "${IMG_WF}"; then
    echo "ERROR: floating :latest tag in ${IMG_WF} (RELS-01)" >&2
    FAIL=1
  else
    echo "    OK: no :latest push tag"
  fi
  if grep -qE '^[[:space:]]*latest[[:space:]]*[:=][[:space:]]*false' "${IMG_WF}"; then
    echo "    OK: metadata-action latest disabled"
  else
    echo "ERROR: metadata-action must disable implicit latest tag (RELS-01)" >&2
    FAIL=1
  fi
  if grep -q 'aquasecurity/trivy-action' "${IMG_WF}"; then
    echo "    OK: image scan wired"
  else
    echo "ERROR: image pipeline must scan before release (RELS-06 / §6)" >&2
    FAIL=1
  fi
  for arg in VERSION COMMIT BUILD_TIME; do
    if grep -qF "${arg}=\${{ steps.ver.outputs" "${IMG_WF}"; then
      echo "    OK: build arg ${arg} from pipeline"
    else
      echo "ERROR: image pipeline missing ${arg} build arg (RELS-03)" >&2
      FAIL=1
    fi
  done
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
