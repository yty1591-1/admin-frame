#!/usr/bin/env bash
# PERFORMANCE_STANDARDS：BASELINES 登记与对应 Benchmark 可运行。
# 用法: bash scripts/ci/check-perf.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
BASELINES="docs/perf/BASELINES.md"
BENCHTIME="${PERF_BENCHTIME:-50ms}"
ROW_COUNT=0

echo "==> PERF-01: baselines and standards"
for f in "${BASELINES}" docs/PERFORMANCE_STANDARDS.md; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f}" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if ! command -v go >/dev/null 2>&1; then
  echo "ERROR: go not found (PERF-02)" >&2
  exit 1
fi

echo "==> PERF-02: registered benchmarks must exist and run"
while IFS= read -r line; do
  [[ "${line}" == *"|"* ]] || continue
  [[ "${line}" == *"路径 ID"* || "${line}" == *"|---"* ]] && continue

  pkg="$(printf '%s\n' "${line}" | awk -F'|' '{
    gsub(/`/,"",$3); gsub(/^[[:space:]]+|[[:space:]]+$/,"",$3); print $3
  }')"
  bench="$(printf '%s\n' "${line}" | awk -F'|' '{
    gsub(/`/,"",$4); gsub(/^[[:space:]]+|[[:space:]]+$/,"",$4); print $4
  }')"

  [[ -z "${pkg}" || -z "${bench}" ]] && continue
  [[ "${pkg}" == "包" || "${bench}" == "Benchmark" ]] && continue

  if [[ ! "${bench}" =~ ^Benchmark[A-Za-z0-9_]+$ ]]; then
    echo "ERROR: invalid Benchmark name '${bench}' in ${BASELINES}" >&2
    FAIL=1
    continue
  fi

  ROW_COUNT=$((ROW_COUNT + 1))
  pkg_path="${pkg#./}"
  if ! grep -RIn --include='*_test.go' -E "^func ${bench}\\(b \\*testing\\.B\\)" "${pkg_path}" >/dev/null 2>&1; then
    echo "ERROR: ${bench} not found under ${pkg} (PERF-02)" >&2
    FAIL=1
    continue
  fi
  echo "    found: ${bench} in ${pkg}"

  echo "    running: go test ${pkg} -bench=^${bench}\$ -benchtime=${BENCHTIME} -count=1"
  if ! go test "${pkg}" -bench="^${bench}$" -benchtime="${BENCHTIME}" -count=1 -run='^$'; then
    echo "ERROR: benchmark ${bench} failed in ${pkg} (PERF-02)" >&2
    FAIL=1
  else
    echo "    OK: ${bench}"
  fi
done < "${BASELINES}"

if [[ "${ROW_COUNT}" -eq 0 ]]; then
  echo "ERROR: ${BASELINES} has no registered Benchmark rows (PERF-01)" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "perf check failed (PERFORMANCE_STANDARDS)" >&2
  exit 1
fi
echo "OK: perf"
