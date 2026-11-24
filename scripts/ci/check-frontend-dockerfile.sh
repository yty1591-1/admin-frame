#!/usr/bin/env bash
# FE-B-02：前端镜像 Dockerfile 静态校验。
#
# 校验项：
#   1. 只允许 NODE_VERSION / PNPM_VERSION / GIT_SHA 三个 build arg，禁止 VITE_* 与密钥类参数；
#   2. 版本钉死且与 frontend/.node-version、packageManager 一致；
#   3. 存在 web-admin / web-biz 双 target，各自把 dist 拷进 nginx html；
#   4. 拷贝 security-headers.conf 且不 COPY 任何 .env*（FE-B-05）；
#   5. frontend/.dockerignore 忽略 node_modules / dist / .env。
#
# 用法: bash scripts/ci/check-frontend-dockerfile.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DF="${ROOT}/frontend/scripts/deploy/Dockerfile"
DI="${ROOT}/frontend/.dockerignore"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

[[ -f "${DF}" ]] || fail "missing ${DF}"
[[ -f "${DI}" ]] || fail "missing ${DI}"

# 1. build arg 白名单。
mapfile -t args < <(grep -oE '^ARG [A-Z0-9_]+' "${DF}" | awk '{print $2}' | sort -u)
for name in "${args[@]}"; do
  case "${name}" in
    NODE_VERSION | PNPM_VERSION | GIT_SHA) ;;
    *) fail "未登记的 Dockerfile ARG: ${name}（只允许 NODE_VERSION / PNPM_VERSION）" ;;
  esac
done
[[ ${#args[@]} -gt 0 ]] || fail "Dockerfile 未声明 NODE_VERSION / PNPM_VERSION"

if grep -qiE '^ARG .*(VITE_|SECRET|PASSWORD|TOKEN|PRIVATE_KEY|ACCESS_KEY)' "${DF}"; then
  fail "禁止把前端构建变量或密钥作为 build arg（FE-B-05 / FE-23）"
fi

# 2. 版本钉死且与仓库一致。
node_version="$(sed -n 's/^ARG NODE_VERSION=\([0-9][0-9.]*\).*$/\1/p' "${DF}" | head -n1)"
pnpm_version="$(sed -n 's/^ARG PNPM_VERSION=\([0-9][0-9.]*\).*$/\1/p' "${DF}" | head -n1)"
[[ -n "${node_version}" ]] || fail "NODE_VERSION 缺少钉死默认值"
[[ -n "${pnpm_version}" ]] || fail "PNPM_VERSION 缺少钉死默认值"

grep -qE '^ENV GITHUB_SHA=\$GIT_SHA$' "${DF}" || fail "build stage 未把 GIT_SHA 透传给 GITHUB_SHA（产物 meta 需要提交号）"
grep -qE '^ENV VITE_APP_GIT_SHA=\$GIT_SHA$' "${DF}" || fail "build stage 未把 GIT_SHA 透传给 VITE_APP_GIT_SHA（turbo 缓存键，FE-B-01）"
grep -qE '^ARG GIT_SHA=unknown$|^ARG GIT_SHA=[0-9a-f]{40}$' "${DF}" || fail "GIT_SHA 默认值必须为 unknown 或 40 位十六进制"

if grep -qE '^ARG GIT_SHA=[0-9a-f]+' "${DF}"; then
  sha="$(sed -n 's/^ARG GIT_SHA=\([0-9a-f]*\)$/\1/p' "${DF}" | head -n1)"
  [[ ${#sha} -eq 40 ]] || fail "GIT_SHA 若给定默认值必须为 40 位十六进制"
fi

expected_node="$(tr -d '[:space:]' <"${ROOT}/frontend/.node-version")"
expected_pnpm="$(grep -oE 'pnpm@[0-9][0-9.]*' "${ROOT}/frontend/package.json" | head -n1 | cut -d@ -f2)"
[[ "${node_version}" == "${expected_node}" ]] || fail \
  "NODE_VERSION=${node_version} 与 frontend/.node-version=${expected_node} 不一致"
[[ "${pnpm_version}" == "${expected_pnpm}" ]] || fail \
  "PNPM_VERSION=${pnpm_version} 与 packageManager pnpm@${expected_pnpm} 不一致"

# 3. 双 target 与产物拷贝。
for app in web-admin web-biz; do
  grep -qE "^FROM [^ ]+ AS ${app}$" "${DF}" || fail "缺少 target: ${app}"
  grep -qF "COPY --from=build /app/apps/${app}/dist /usr/share/nginx/html" "${DF}" ||
    fail "${app} target 未把 dist 拷进 nginx html"
  grep -qE '^RUN test -f apps/web-(admin|biz)/dist/index\.html' "${DF}" ||
    fail "build stage 缺少 dist 存在性断言"
done

grep -qF 'security-headers.conf /etc/nginx/security-headers.conf' "${DF}" ||
  fail "未拷贝 security-headers.conf 到 /etc/nginx/"

# 4. 禁止把 .env 打进镜像。
if grep -qiE '^COPY .*\.env' "${DF}"; then
  fail "禁止 COPY .env* 进镜像（FE-B-05）"
fi

# 5. 构建上下文忽略清单。
for pattern in node_modules dist .env; do
  grep -qxF "${pattern}" "${DI}" || fail "frontend/.dockerignore 缺少忽略项: ${pattern}"
done

echo "OK: Dockerfile 双 target、版本钉死与 build arg 白名单校验通过"

# 6. 发版流水线（FE-B-03）：前端镜像进 image.yml matrix，tag 禁 latest，digest 进 release 清单。
IY="${ROOT}/.github/workflows/image.yml"
[[ -f "${IY}" ]] || fail "missing ${IY}"

for app in web-admin web-biz; do
  grep -qE "^          - cmd: ${app}$" "${IY}" || fail "image.yml matrix 缺少前端镜像 ${app}"
done
grep -qE '^            context: frontend$' "${IY}" || fail "image.yml 前端镜像 context 必须为 frontend"
grep -qF 'dockerfile: frontend/scripts/deploy/Dockerfile' "${IY}" ||
  fail "image.yml 前端镜像未指向 frontend/scripts/deploy/Dockerfile"
grep -qF 'GIT_SHA=${GITHUB_SHA}' "${IY}" || fail "image.yml 未把 GIT_SHA 传入前端镜像构建"
grep -qE '^            latest=false$' "${IY}" || fail "image.yml metadata 必须禁 latest（RELS-01）"
grep -qE '^            type=sha,format=short,prefix=sha-$' "${IY}" || fail "image.yml 缺少 sha- 标签"
grep -qE '^            type=semver,pattern=\{\{version\}\}' "${IY}" || fail "image.yml 缺少 semver 标签"
grep -qF 'for cmd in admin-api biz-api c-api worker migrate web-admin web-biz; do' "${IY}" ||
  fail "release 清单未收集前端镜像 digest（images.json 必须含 web-admin/web-biz）"
grep -qE 'format: cyclonedx' "${IY}" || fail "image.yml 缺少 CycloneDX SBOM"

echo "OK: image.yml 前端镜像、标签规则与 digest 收集校验通过"
