#!/usr/bin/env bash
# CONFIG_STANDARDS：example 齐全、禁止跟踪本地 yaml、example 无真实 DSN。
# 用法: bash scripts/ci/check-config.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

EXAMPLES=(
  configs/prod/admin.yaml.example
  configs/prod/biz.yaml.example
  configs/prod/c.yaml.example
  configs/prod/worker.yaml.example
)
MODULES=(
  configs/prod/modules/http.yaml.example
  configs/prod/modules/log.yaml.example
  configs/prod/modules/platform.yaml.example
  configs/prod/modules/auth.yaml.example
  configs/prod/modules/infra.yaml.example
  configs/prod/modules/iam.yaml.example
  configs/prod/modules/identity.yaml.example
)
FAIL=0

echo "==> CFG-01: required config examples"
for f in "${EXAMPLES[@]}" "${MODULES[@]}"; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f} (CFG-01)" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> CFG-02: git must not track configs/*.yaml (only *.example)"
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  tracked="$(git ls-files 'configs/*.yaml' 'configs/**/*.yaml' 2>/dev/null | grep -v '\.example$' || true)"
  if [[ -n "${tracked}" ]]; then
    echo "ERROR: tracked non-example config files (CFG-02):" >&2
    echo "${tracked}" >&2
    FAIL=1
  else
    echo "    OK: no tracked configs/*.yaml"
  fi
else
  echo "    SKIP: not a git work tree"
fi

echo "==> CFG-06: examples must not embed real DSN secrets"
for f in "${EXAMPLES[@]}" "${MODULES[@]}"; do
  [[ -f "${f}" ]] || continue
  while IFS= read -r line; do
    [[ "${line}" =~ ^[[:space:]]*# ]] && continue
    if [[ "${line}" =~ ^[[:space:]]*dsn:[[:space:]]*(.*)$ ]]; then
      val="${BASH_REMATCH[1]}"
      # 剥离 YAML 行内注释（" #" 及其后内容），再去除可能的引号
      val="${val%% #*}"
      val="${val%\"}"
      val="${val#\"}"
      val="${val%\'}"
      val="${val#\'}"
      # trim
      val="${val#"${val%%[![:space:]]*}"}"
      val="${val%"${val##*[![:space:]]}"}"
      if [[ -z "${val}" ]]; then
        continue
      fi
      lower="$(printf '%s' "${val}" | tr '[:upper:]' '[:lower:]')"
      if printf '%s' "${lower}" | grep -Eq 'changeme|placeholder|your-|example|todo|<dsn>|\$\{'; then
        continue
      fi
      echo "ERROR: ${f}: dsn must be empty or placeholder in example (CFG-06): ${line}" >&2
      FAIL=1
    fi
  done < "${f}"
done
echo "    OK: example dsn policy checked"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "config check failed (CONFIG_STANDARDS)" >&2
  exit 1
fi
echo "OK: config"
