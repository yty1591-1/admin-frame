#!/usr/bin/env bash
# 供应链等价演练（ENT-19，手动执行，不进 make ci）。
#
# 背景：keyless 签名依赖 GitHub Actions 的 OIDC（Fulcio 证书 + Rekor），本地无法复现。
# 本脚本用「本地 registry + 本地密钥对」把**除 OIDC 之外**的每一步真跑一遍：
#   1. 按 digest 签名 → 用同一公钥验签通过（证明 cosign 链路与 digest 语义正确）；
#   2. 换一把公钥验签必须失败（证明不是「有签名就放行」）；
#   3. 未签名 digest 必须失败；
#   4. 同一 tag 被改指向新 digest 后，旧 digest 的签名不能用于新 digest（证明为什么必须签 digest）；
#   5. SBOM attestation 可取回，载荷可解码且 bomFormat=CycloneDX（与 verify-supply-chain.sh 同口径）；
#   6. verify-supply-chain.sh 的控制流：cosign 成功→放行，失败→中止；tag 引用与通配身份在执行前即被拒。
#
# 明确不覆盖：keyless（OIDC→Fulcio→Rekor）与 GitHub OIDC 身份匹配，只能在 CI 首跑验证，
# 见 docs/release/supply-chain.md「验证边界」。
#
# 依赖：docker；首次需联网拉取 cosign / registry / alpine 镜像。
# 用法: bash scripts/ci/supply-chain-rehearsal.sh [--port 5055]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

PORT=5055
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --port) PORT="${2:?--port 需要值}"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

COSIGN_IMAGE="${COSIGN_IMAGE:-ghcr.io/sigstore/cosign/cosign:v2.4.1}"
REGISTRY_IMAGE="${REGISTRY_IMAGE:-registry:2}"
PROBE_IMAGE="${PROBE_IMAGE:-alpine:3.24}"
# 「另一份内容」的镜像：必须与 PROBE_IMAGE 不同，否则 digest 相同、用例失效。
ALT_IMAGE="${ALT_IMAGE:-alpine:3.20}"
WORK="${ROOT}/tmp-supply-chain"
CONTAINER="admin-frame-sc-rehearsal-${PORT}"
PASS=0
FAILED=0

# MSYS（Git Bash）会把 /work 这类容器内路径改写成 Windows 路径，必须关掉。
if [[ -n "${MSYSTEM:-}" ]]; then
  export MSYS_NO_PATHCONV=1
  MOUNT="$(pwd -W)"
else
  MOUNT="${ROOT}"
fi

pass() {
  echo "  PASS: $*"
  PASS=$((PASS + 1))
}
fail() {
  echo "  FAIL: $*" >&2
  FAILED=$((FAILED + 1))
}

cleanup() {
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || {
  echo "ERROR: 需要 docker（本地 registry + cosign 容器）" >&2
  exit 1
}

rm -rf "${WORK}"
mkdir -p "${WORK}"

echo "==> 启动本地 registry (:${PORT})"
docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
docker run -d --rm --name "${CONTAINER}" -p "${PORT}:5000" "${REGISTRY_IMAGE}" >/dev/null
for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:${PORT}/v2/" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://localhost:${PORT}/v2/" >/dev/null 2>&1 || {
  echo "ERROR: 本地 registry 未就绪（端口 ${PORT} 被占用？）" >&2
  exit 1
}

# cosign 优先用宿主机二进制；没有就用容器（容器内访问宿主 registry 用 host.docker.internal）。
if command -v cosign >/dev/null 2>&1; then
  CSIGN_HOST="localhost:${PORT}"
  cosign_run() { cosign "$@"; }
else
  CSIGN_HOST="host.docker.internal:${PORT}"
  # shellcheck disable=SC2317
  cosign_run() {
    docker run --rm -i -e COSIGN_PASSWORD=rehearsal \
      --add-host host.docker.internal:host-gateway \
      -v "${MOUNT}:/work" -w /work "${COSIGN_IMAGE}" "$@"
  }
fi
INSECURE=(--allow-http-registry --allow-insecure-registry)

echo "==> 推送探针镜像"
docker pull -q "${PROBE_IMAGE}" >/dev/null
docker tag "${PROBE_IMAGE}" "localhost:${PORT}/ent19/probe:1"
DIGEST="$(docker push "localhost:${PORT}/ent19/probe:1" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -n 1)"
[[ "${DIGEST}" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo "ERROR: 未能取得 digest" >&2
  exit 1
}
REF="${CSIGN_HOST}/ent19/probe@${DIGEST}"
echo "    probe digest: ${DIGEST}"

echo "==> 生成演练密钥对"
cosign_run generate-key-pair --output-key-prefix "tmp-supply-chain/ent19" >/dev/null
cosign_run generate-key-pair --output-key-prefix "tmp-supply-chain/other" >/dev/null

echo "==> 1) 按 digest 签名"
if cosign_run sign --key tmp-supply-chain/ent19.key --tlog-upload=false "${INSECURE[@]}" --yes "${REF}" >/dev/null 2>&1; then
  pass "sign ${DIGEST}"
else
  fail "sign ${DIGEST}"
fi

echo "==> 2) 同一公钥验签必须通过"
if cosign_run verify --key tmp-supply-chain/ent19.pub --insecure-ignore-tlog=true "${INSECURE[@]}" "${REF}" >/dev/null 2>&1; then
  pass "verify with matching public key"
else
  fail "verify with matching public key"
fi

echo "==> 3) 另一把公钥验签必须失败"
if cosign_run verify --key tmp-supply-chain/other.pub --insecure-ignore-tlog=true "${INSECURE[@]}" "${REF}" >/dev/null 2>&1; then
  fail "verify with a foreign key must fail"
else
  pass "foreign key rejected"
fi

echo "==> 4) 未签名 digest 必须失败"
# 必须用**不同内容**的镜像：同一内容 → 同一 digest → 会命中步骤 1 的签名，测不出「未签名」。
docker pull -q "${ALT_IMAGE}" >/dev/null
docker tag "${ALT_IMAGE}" "localhost:${PORT}/ent19/unsigned:1"
UNSIGNED="$(docker push "localhost:${PORT}/ent19/unsigned:1" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -n 1)"
if [[ "${UNSIGNED}" == "${DIGEST}" ]]; then
  fail "探针镜像内容相同（${PROBE_IMAGE} vs ${ALT_IMAGE}），无法构造未签名用例"
elif cosign_run verify --key tmp-supply-chain/ent19.pub --insecure-ignore-tlog=true "${INSECURE[@]}" \
  "${CSIGN_HOST}/ent19/unsigned@${UNSIGNED}" >/dev/null 2>&1; then
  fail "unsigned image must be rejected"
else
  pass "unsigned digest rejected"
fi

echo "==> 5) 同 tag 改指向新 digest 后，旧签名不能用于新 digest"
# 把 probe:1 改指向另一份内容（模拟「tag 被替换 / 供应链投毒」），再拿旧签名去验新 digest。
docker tag "${ALT_IMAGE}" "localhost:${PORT}/ent19/probe:1"
MOVED="$(docker push "localhost:${PORT}/ent19/probe:1" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -n 1)"
if [[ -z "${MOVED}" || "${MOVED}" == "${DIGEST}" ]]; then
  fail "tag 重推后 digest 未变，用例构造失败"
elif cosign_run verify --key tmp-supply-chain/ent19.pub --insecure-ignore-tlog=true "${INSECURE[@]}" \
  "${CSIGN_HOST}/ent19/probe@${MOVED}" >/dev/null 2>&1; then
  fail "moved tag must not inherit the old signature"
else
  pass "moved tag does not inherit the old signature"
fi

echo "==> 6) SBOM attestation 与载荷内容"
cat >"${WORK}/sbom-probe.cdx.json" <<'JSON'
{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "version": 1,
  "metadata": { "tools": [{ "name": "ent19-rehearsal" }] },
  "components": [{ "type": "library", "name": "alpine", "version": "3.24" }]
}
JSON
if cosign_run attest --key tmp-supply-chain/ent19.key --tlog-upload=false --type cyclonedx \
  --predicate tmp-supply-chain/sbom-probe.cdx.json "${INSECURE[@]}" --yes "${REF}" >/dev/null 2>&1; then
  pass "attest --type cyclonedx"
else
  fail "attest --type cyclonedx"
fi
ATT="$(cosign_run verify-attestation --key tmp-supply-chain/ent19.pub --insecure-ignore-tlog=true \
  --type cyclonedx "${INSECURE[@]}" --output json "${REF}" 2>&1 || true)"
PAYLOAD_B64="$(printf '%s' "${ATT}" | tr -d '\n' | sed -n 's/.*"payload":"\([^"]*\)".*/\1/p' | head -n 1)"
if [[ -n "${PAYLOAD_B64}" ]] &&
  printf '%s' "${PAYLOAD_B64}" | base64 -d 2>/dev/null | grep -q '"bomFormat"' &&
  printf '%s' "${PAYLOAD_B64}" | base64 -d 2>/dev/null | grep -q 'CycloneDX'; then
  pass "attestation payload is CycloneDX（与 verify-supply-chain.sh 同口径）"
else
  fail "attestation payload parsing（verify-supply-chain.sh 会因此拒绝发布）"
fi

echo "==> 7) verify-supply-chain.sh 控制流（假 cosign）"
FAKE_DIR="${WORK}/fakebin"
mkdir -p "${FAKE_DIR}"
cat >"${FAKE_DIR}/cosign" <<'FAKE'
#!/usr/bin/env bash
# 只用于验证脚本控制流：按 FAKE_COSIGN_EXIT 返回。
if [[ "${1:-}" == "verify-attestation" ]]; then
  printf '{"payloadType":"application/vnd.in-toto+json","payload":"%s"}\n' \
    "$(printf '{"predicate":{"bomFormat":"CycloneDX"}}' | base64 | tr -d '\n')"
  exit "${FAKE_COSIGN_EXIT:-0}"
fi
exit "${FAKE_COSIGN_EXIT:-0}"
FAKE
chmod +x "${FAKE_DIR}/cosign"

export GITHUB_REPOSITORY="acme/admin-frame"
if COSIGN_BIN=cosign PATH="${FAKE_DIR}:${PATH}" bash scripts/ci/verify-supply-chain.sh "${REF}" >/dev/null 2>&1; then
  pass "verify-supply-chain: cosign 成功时放行"
else
  fail "verify-supply-chain: cosign 成功时应当放行"
fi
if COSIGN_BIN=cosign FAKE_COSIGN_EXIT=1 PATH="${FAKE_DIR}:${PATH}" \
  bash scripts/ci/verify-supply-chain.sh "${REF}" >/dev/null 2>&1; then
  fail "verify-supply-chain: cosign 失败时必须中止"
else
  pass "verify-supply-chain: cosign 失败时中止"
fi
if COSIGN_BIN=cosign PATH="${FAKE_DIR}:${PATH}" \
  bash scripts/ci/verify-supply-chain.sh "${CSIGN_HOST}/ent19/probe:1" >/dev/null 2>&1; then
  fail "verify-supply-chain: tag 引用必须被拒绝"
else
  pass "verify-supply-chain: 拒绝 tag 引用"
fi
if COSIGN_BIN=cosign COSIGN_IDENTITY_REGEXP='.*' PATH="${FAKE_DIR}:${PATH}" \
  bash scripts/ci/verify-supply-chain.sh "${REF}" >/dev/null 2>&1; then
  fail "verify-supply-chain: 通配身份必须被拒绝"
else
  pass "verify-supply-chain: 拒绝通配身份"
fi

echo
echo "================ 演练结果 ================"
echo "PASS: ${PASS}    FAIL: ${FAILED}"
echo "未覆盖（必须由 CI 首跑验证）：keyless OIDC → Fulcio 证书 → Rekor 透明日志、GitHub 身份匹配"
echo "详见 docs/release/supply-chain.md「验证边界」"
[[ "${FAILED}" -eq 0 ]] || exit 1
