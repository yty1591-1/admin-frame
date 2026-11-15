#!/usr/bin/env bash
# ENT-07 T4：密钥托管门禁。
#
# 覆盖 docs/production-readiness-implementation-plan.md §7.2 的 check-secret-provider：
#   1. 密钥 Provider 抽象存在，且已接入启动路径（防「写了抽象但没人用」的死代码）；
#   2. 生产样例配置必须声明托管来源，不得依赖隐式环境变量注入；
#   3. 生产制品必须把 Secret 挂载为文件（环境变量会进入 /proc 与子进程环境）；
#   4. 生产样例不得出现密钥明文；
#   5. Vault 生产实现存在，且以 HTTP 实现（不引入 Vault SDK，绕开 DEP-06 依赖评审）；
#   6. Provider 故障必须 fail fast —— Chain 只在 ErrNotFound 时继续。
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
if ! grep -q "applySecrets" internal/platform/config/secrets.go; then
  fail "internal/platform/config/secrets.go 未实现 applySecrets（Provider 未接入配置解析）"
fi
if ! grep -q "c.resolveSecrets()" internal/platform/config/defaults.go; then
  fail "internal/platform/config/defaults.go 的 normalize 未调用 resolveSecrets（Provider 不会被启动路径执行）"
fi

echo "==> ENT-07: vault production provider (T3)"
if [[ ! -f "internal/platform/secrets/vault.go" ]]; then
  fail "internal/platform/secrets/vault.go 缺失（无生产密钥托管实现）"
else
  for sym in "func NewVaultProvider" "func (p *VaultProvider) Get" "X-Vault-Token"; do
    if ! grep -qF "${sym}" internal/platform/secrets/vault.go; then
      fail "internal/platform/secrets/vault.go 缺少 ${sym}"
    fi
  done
  # 刻意不引 Vault SDK：T3 用 HTTP 实现，新增直接依赖须先走 DEP-06 评审。
  if grep -qE '"github\.com/hashicorp/(vault|go-retryablehttp)' internal/platform/secrets/*.go; then
    fail "internal/platform/secrets 引入了 Vault SDK；新增强制依赖须先走 DEP-06 依赖评审"
  fi
fi
if ! grep -qF "SecretProviderVault" internal/platform/config/secrets.go; then
  fail "internal/platform/config/secrets.go 未注册 vault 来源（生产实现无法被配置启用）"
fi

echo "==> ENT-07: provider failure must fail fast"
# Provider 不可用时禁止回落到环境变量：Chain 只允许在 ErrNotFound 时继续。
if ! grep -qF "errors.Is(err, ErrNotFound)" internal/platform/secrets/chain.go; then
  fail "chain.go 未按 ErrNotFound 判定回落（其它错误可能被静默吞掉）"
fi
# 403 / 5xx 与「密钥不存在」必须区分，否则鉴权失败会被降级成回落。
if ! grep -qF "errVaultNotFound" internal/platform/secrets/vault.go; then
  fail "vault.go 未区分 404 与鉴权/传输错误（403/5xx 会被误判为密钥缺失）"
fi
# 上述保证必须有用例守住，否则改实现时无人发现。
for testName in TestVaultProviderForbiddenDoesNotFallBack TestVaultProviderMissingObjectFailsFast; do
  if ! grep -qF "func ${testName}" internal/platform/secrets/vault_test.go 2>/dev/null; then
    fail "缺少 fail-fast 用例 ${testName}"
  fi
done

echo "==> ENT-07: managed secret source declared in platform defaults / Helm"
# M3：configs/ 样例已删除；生产托管来源改查代码默认值与 Helm 挂载。
if ! grep -qE 'SecretProviderFile|providers:.*"file"|ProviderFile|"file"' \
  internal/platform/config/secrets.go internal/platform/config/defaults.go; then
  fail "platform secrets 未声明 file Provider（生产不得只靠隐式环境变量）"
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

echo "==> ENT-07: local prod-*.yaml must not reuse published samples"
# configs/prod-<role>.yaml 不入库，但常被开发者当作「生产配置」复制使用。
# 若存在则扫描仓库内已公开的演练密钥与本地 DSN，避免把样例值带进真实部署。
shopt -s nullglob
for file in configs/prod-*.yaml; do
  for sample in \
    "7a1ec0f4290851c052cee9fb3b972bc5a8c14297c65e4301bb320b3e6f426454" \
    "a0c1663bfcdb9f923a4d7171709b6b0d36ab2298053a36f4c26cf7a3b5c17c8a" \
    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
    "ca24256120caa7c42ccaa14f43aa56c8a3d72b4b3cd2ac88b51ed2492101d753" \
    "fd51b4775c2cc81269accb4e1c511c7f4393d159927c95c8" \
    "AFPRODMNFN5QQZBGXY" \
    "878d6f395e595e146fd395418bfe4d9cdf0a596cda8ecb88f85b4d95de402c77" \
    "root:root@tcp(localhost:3306)"; do
    if grep -qF "${sample}" "${file}"; then
      fail "${file} 含仓库已公开的演练密钥 / 本地 DSN；生产必须由密钥托管注入"
    fi
  done
done

if [[ ${FAIL} -ne 0 ]]; then
  echo "FAILED: ENT-07 密钥托管门禁未通过" >&2
  exit 1
fi
echo "OK: ENT-07 密钥托管门禁通过"
