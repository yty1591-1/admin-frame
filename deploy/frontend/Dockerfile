# syntax=docker/dockerfile:1.7
# 前端镜像（INF-REL-03）：多阶段构建 → nginx-unprivileged 非 root 运行。
# 构建上下文为仓库根目录，便于同时取前端源码与 deploy/frontend 下的 Nginx 配置：
#   docker build -f deploy/frontend/Dockerfile --build-arg APP=admin-web -t admin-web:<tag> .
# 禁止 :latest；生产使用 digest 固定（见 docs/RELEASE_STANDARDS.md）。

ARG NODE_IMAGE=node:22.20-alpine
ARG NGINX_IMAGE=nginxinc/nginx-unprivileged:1.27-alpine

FROM ${NODE_IMAGE} AS build
ARG PNPM_VERSION=12.10.1
ENV PNPM_HOME=/pnpm \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate
WORKDIR /workspace

# 先只复制工作区清单，最大化依赖层缓存。
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml \
     frontend/.npmrc frontend/tsconfig.base.json frontend/tsconfig.json ./
COPY frontend/packages ./packages
COPY frontend/apps ./apps

RUN pnpm install --frozen-lockfile

ARG APP=admin-web
RUN test -d "apps/${APP}" \
    && pnpm --filter "@admin-frame/${APP}" build

FROM ${NGINX_IMAGE} AS runtime
ARG APP=admin-web

# 静态站点配置：缓存头与 SPA 回退集中在 nginx.conf。
COPY deploy/frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/frontend/security-headers.conf /etc/nginx/snippets/security-headers.conf
COPY --from=build /workspace/apps/${APP}/dist /usr/share/nginx/html

# nginx-unprivileged 默认 UID/GID 101；显式声明便于策略审计。
USER 101:101
EXPOSE 8080

# 健康检查使用 docker 自检；K8s 探针见 Helm Chart。
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
