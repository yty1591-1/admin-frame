#!/usr/bin/env bash
# RELEASE_STANDARDS RELS-14：部署前验签。
#
# 目的：digest 只保证「内容不可变」，不保证「来源可信」。本脚本要求每个待部署镜像
# 同时满足三个条件，任一不满足即中止发布（fail closed）：
#   1. 引用形式为 `<image>@sha256:<64 hex>`——按 tag 验签是 TOCTOU，等于没验；
#   2. 存在由本仓库 image.yml 的 keyless 身份签出的签名（绑定 Fulcio 证书身份与 OIDC issuer）；
#   3. 存在同一 digest 上的 CycloneDX SBOM attestation，且内容是真 SBOM。
#
# 用法: bash scripts/ci/verify-supply-chain.sh <image@sha256:...> [<...> ...]
#
# 环境:
#   COSIGN_BIN                cosign 可执行文件（默认 cosign；本地演练可用包装脚本）
#   GITHUB_REPOSITORY         仓库 owner/name；未设置时必须显式给 COSIGN_IDENTITY_REGEXP
#   COSIGN_IDENTITY_REGEXP    覆盖签发身份（GHES / 镜像仓库改名时才需要）
#   COSIGN_OIDC_ISSUER        OIDC issuer（默认 GitHub Actions；非 GitHub 平台才需要改）
#   VERIFY_SBOM_CONTENT       默认 1：解码 attestation 载荷并校验 bomFormat=CycloneDX
set -euo pipefail

COSIGN_BIN="${COSIGN_BIN:-cosign}"
OIDC_ISSUER="${COSIGN_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
VERIFY_SBOM_CONTENT="${VERIFY_SBOM_CONTENT:-1}"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

# ---- 参数与身份校验（在调用 cosign 之前完成：先拒绝不可能正确的输入） ----
if [[ "$#" -eq 0 ]]; then
  die "至少需要一个 <image>@sha256:<digest> 参数（RELS-14）"
fi

if [[ -n "${COSIGN_IDENTITY_REGEXP:-}" ]]; then
  identity="${COSIGN_IDENTITY_REGEXP}"
elif [[ -n "${GITHUB_REPOSITORY:-}" ]]; then
  # 默认身份精确到「本仓库的 image.yml 在某分支/tag 上」：不接受别的仓库，
  # 也不接受本仓库其他 workflow（否则 PR 构建产物也能骗过发布验签）。
  identity="^https://github\\.com/${GITHUB_REPOSITORY}/\\.github/workflows/image\\.yml@refs/(heads|tags)/"
else
  die "无法确定签发身份：未设置 GITHUB_REPOSITORY，也没有显式给出 COSIGN_IDENTITY_REGEXP"
fi

# 通配身份等于不校验身份（`.*` 可以匹配任何人的任何 workflow），必须拒绝。
case "${identity}" in
  '' | . | '.*' | '^.*$')
    die "COSIGN_IDENTITY_REGEXP 过于宽松（'${identity}'），拒绝把身份校验降级为「只要有签名」"
    ;;
esac
if [[ "${identity}" == *".*"* ]]; then
  die "COSIGN_IDENTITY_REGEXP 含 '.*'（'${identity}'）：签发身份必须具体到本仓库的 image.yml"
fi

[[ -n "${OIDC_ISSUER}" && "${OIDC_ISSUER}" == https://* ]] ||
  die "COSIGN_OIDC_ISSUER 必须是 https issuer，当前为 '${OIDC_ISSUER}'"

for ref in "$@"; do
  [[ "${ref}" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]] ||
    die "镜像引用必须是 <image>@sha256:<64 hex>，当前为 '${ref}'（按 tag 验签会被 TOCTOU 绕过）"
done

command -v "${COSIGN_BIN}" >/dev/null 2>&1 ||
  die "${COSIGN_BIN} 不在 PATH 中；发布环境必须安装钉扎版本的 cosign（RELS-13/SB-06）"

echo "==> 校验签发身份: ${identity}"
echo "==> 校验 OIDC issuer: ${OIDC_ISSUER}"

fail=0
for ref in "$@"; do
  short="${ref%%@*}:${ref##*@sha256:}"
  echo "==> ${ref}"

  # 1) 镜像签名（keyless：证书由 Fulcio 签发，身份绑定到 image.yml 的 workflow）
  if ! out="$("${COSIGN_BIN}" verify "${ref}" \
    --certificate-identity-regexp "${identity}" \
    --certificate-oidc-issuer "${OIDC_ISSUER}" 2>&1)"; then
    echo "ERROR: ${short} 缺少可信签名（RELS-14）" >&2
    printf '%s\n' "${out}" >&2
    fail=1
    continue
  fi
  echo "    OK: signature (keyless)"

  # 2) SBOM attestation（同一 digest 上，且类型必须是 CycloneDX）
  if ! att="$("${COSIGN_BIN}" verify-attestation --type cyclonedx "${ref}" \
    --certificate-identity-regexp "${identity}" \
    --certificate-oidc-issuer "${OIDC_ISSUER}" --output json 2>&1)"; then
    echo "ERROR: ${short} 缺少可信的 CycloneDX SBOM attestation（RELS-13/14）" >&2
    printf '%s\n' "${att}" >&2
    fail=1
    continue
  fi
  echo "    OK: attestation type=cyclonedx"

  # 3) 载荷内容：签名正确但 SBOM 为空（或根本不是 CycloneDX）不能算通过
  if [[ "${VERIFY_SBOM_CONTENT}" == "1" ]]; then
    payload_b64="$(printf '%s' "${att}" | tr -d '\n' | sed -n 's/.*"payload":"\([^"]*\)".*/\1/p' | head -n 1)"
    if [[ -z "${payload_b64}" ]]; then
      echo "ERROR: ${short} 的 attestation 输出中找不到 DSSE payload，无法确认 SBOM 内容（RELS-13）" >&2
      fail=1
      continue
    fi
    payload="$(printf '%s' "${payload_b64}" | base64 -d 2>/dev/null || true)"
    if [[ -z "${payload}" ]]; then
      echo "ERROR: ${short} 的 attestation payload 无法解码（RELS-13）" >&2
      fail=1
      continue
    fi
    if ! printf '%s' "${payload}" | grep -q '"bomFormat"' ||
      ! printf '%s' "${payload}" | grep -q 'CycloneDX'; then
      echo "ERROR: ${short} 的 attestation 不是 CycloneDX SBOM（缺 bomFormat/CycloneDX）" >&2
      fail=1
      continue
    fi
    echo "    OK: SBOM payload is CycloneDX"
  fi
done

if [[ "${fail}" -ne 0 ]]; then
  echo "supply chain verification failed: 拒绝部署（RELS-14）" >&2
  exit 1
fi
echo "OK: supply chain verified (${#} image(s))"
