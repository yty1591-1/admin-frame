#!/usr/bin/env bash
# ENT-07 T4：密钥托管门禁。
#
# 覆盖 docs/production-readiness-implementation-plan.md §7.2 的 check-secret-provider：
#   1. 密钥 Provider 抽象存在，且已接入启动路径（防「写了抽象但没人用」的死代码）；
#   2. 生产样例配置必须声明托管来源，不得依赖隐式环境变量注入；
#   3. 生产制品必须把 Secret 挂载为文件（环境变量会进入 /proc 与子进程环境）；
#   4. 生产样例不得出现密钥明文。
#
# 用法: bash scripts/ci/check-secret-provider.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "ERROR: $*" >&2; FAIL=1; }

echo "==> ENT-07: secret provider abstraction"
for f in secrets.go env.go file.go chain.go; do
  [[ -f "internal/platform/secrets/${f}" ]] || fail "internal/platform/secrets/${f} 缺失"
done
for sym in "type Provider interface" "func NewResolver" "func NewEnvProvider" "func NewFileProvider"; do
  if ! grep -rqF "${sym}" internal/platform/secrets/*.go; then
    fail "internal/platform/secrets 缺少 ${sym}"
  fi
done

echo "==> ENT-07: provider wired into startup path"
if ! grep -q "applySecrets" internal/platform/config_secrets.go; then
  fail "internal/platform/config_secrets.go 未实现 applySecrets（Provider 未接入配置解析）"
fi
if ! grep -q "c.resolveSecrets()" internal/platform/config.go; then
  fail "internal/platform/config.go 的 normalize 未调用 resolveSecrets（Provider 不会被启动路径执行）"
fi

echo "==> ENT-07: prod config declares managed source"
PLATFORM_EXAMPLE="configs/prod/modules/platform.yaml.example"
if [[ ! -f "${PLATFORM_EXAMPLE}" ]]; then
  fail "${PLATFORM_EXAMPLE} 缺失"
else
  if ! grep -q "^secrets:" "${PLATFORM_EXAMPLE}"; then
    fail "${PLATFORM_EXAMPLE} 缺少 secrets 段（生产不得依赖隐式环境变量注入）"
  fi
  if ! grep -qE "^[[:space:]]+providers:[[:space:]]*\[.*file.*\]" "${PLATFORM_EXAMPLE}"; then
    fail "${PLATFORM_EXAMPLE} 的 secrets.providers 未声明 file 来源"
  fi
  if ! grep -qE "^[[:space:]]+dir:[[:space:]]*\"/var/run/secrets/" "${PLATFORM_EXAMPLE}"; then
    fail "${PLATFORM_EXAMPLE} 的 secrets.file.dir 未指向 Secret 挂载目录"
  fi
fi

echo "==> ENT-07: helm mounts secrets as files"
VALUES_PROD="deploy/helm/admin-frame/values-prod.yaml"
if [[ ! -f "${VALUES_PROD}" ]]; then
  fail "${VALUES_PROD} 缺失"
else
  if ! grep -q "secretName" "${VALUES_PROD}"; then
    fail "${VALUES_PROD} 未声明 secretName（密钥无法以文件形式挂载）"
  fi
  if ! grep -qE "^[[:space:]]*secretMounts:" "${VALUES_PROD}"; then
    fail "${VALUES_PROD} 缺少 secretMounts（仍只以环境变量注入密钥）"
  fi
fi

echo "==> ENT-07: no plaintext secrets in prod examples"
# 生产样例中这些字段必须留空（由 Provider 注入），出现非空值即视为明文泄露。
check_empty_field() {
  local file="$1" key="$2"
  [[ -f "${file}" ]] || return 0
  # 提取 yaml 中该键的值；去掉行尾注释与空白后必须为空或空字符串。
  local value
  value="$(grep -E "^[[:space:]]*${key}:" "${file}" | head -1 | sed -E "s/^[[:space:]]*${key}:[[:space:]]*//; s/[[:space:]]*#.*$//; s/^\"\"$//; s/^''$//" || true)"
  if [[ -n "${value}" ]]; then
    fail "${file} 的 ${key} 存在明文取值（生产必须由密钥托管注入）"
  fi
}
check_empty_field "configs/prod/modules/platform.yaml.example" "dsn"
check_empty_field "configs/prod/modules/auth.yaml.example" "secret"
check_empty_field "configs/prod/modules/identity.yaml.example" "hash_key"
check_empty_field "configs/prod/modules/identity.yaml.example" "secret_key"

if [[ ${FAIL} -ne 0 ]]; then
  echo "FAILED: ENT-07 密钥托管门禁未通过" >&2
  exit 1
fi
echo "OK: ENT-07 密钥托管门禁通过"
