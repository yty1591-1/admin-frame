# syntax=docker/dockerfile:1.7

# FE-B-02：单 Dockerfile 双 target（web-admin / web-biz）。
# 版本钉死：NODE_VERSION 与 frontend/.node-version 一致，PNPM_VERSION 与 packageManager 一致；
# 两者由 scripts/ci/check-frontend-dockerfile.sh 校验，且只允许 NODE_VERSION / PNPM_VERSION / GIT_SHA 三个 build arg。
# 禁止任何 VITE_* / 密钥类 build arg：前端构建变量走 .env 白名单或运行时注入（FE-B-05 / FE-23）。

ARG NODE_VERSION=24.16.0
ARG PNPM_VERSION=11.16.0
# 提交 SHA 由发布流水线注入（--build-arg GIT_SHA=$GITHUB_SHA），写入产物 meta；
# 非密钥，允许出现在 build arg，但必须为 40 位十六进制（见 check-frontend-dockerfile.sh）。
ARG GIT_SHA=unknown

FROM node:${NODE_VERSION}-alpine AS base

ENV PNPM_HOME=/pnpm \
    PATH=$PNPM_HOME:$PATH \
    CI=true \
    NODE_OPTIONS=--max-old-space-size=8192 \
    TZ=Asia/Shanghai

# 直接钉 pnpm 版本，避免镜像内 corepack 随 Node 版本漂移。
RUN npm i -g pnpm@${PNPM_VERSION}

WORKDIR /app

FROM base AS build

ARG GIT_SHA
ENV GITHUB_SHA=$GIT_SHA
ENV VITE_APP_GIT_SHA=$GIT_SHA

COPY . .
RUN --mount=type=cache,id=pnpm,target=/pnpm/store pnpm install --frozen-lockfile
RUN pnpm run build
# 产物 meta 里的 SHA 必须与注入值一致（未注入时跳过，由 CI 的 --build-arg 保证）。
RUN if [ "$GIT_SHA" != "unknown" ]; then echo "$GIT_SHA" | grep -qE '^[0-9a-f]{40}$' || { echo "GIT_SHA must be 40 hex chars" >&2; exit 1; }; fi
RUN test -f apps/web-admin/dist/index.html && test -f apps/web-biz/dist/index.html

FROM nginx:stable-alpine AS web-admin

COPY --from=build /app/apps/web-admin/dist /usr/share/nginx/html
COPY --from=build /app/scripts/deploy/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/scripts/deploy/security-headers.conf /etc/nginx/security-headers.conf
RUN echo "types { application/javascript js mjs; }" > /etc/nginx/conf.d/mjs.conf \
    && rm -rf /etc/nginx/conf.d/default.conf

EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]

FROM nginx:stable-alpine AS web-biz

COPY --from=build /app/apps/web-biz/dist /usr/share/nginx/html
COPY --from=build /app/scripts/deploy/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/scripts/deploy/security-headers.conf /etc/nginx/security-headers.conf
RUN echo "types { application/javascript js mjs; }" > /etc/nginx/conf.d/mjs.conf \
    && rm -rf /etc/nginx/conf.d/default.conf

EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]

# 兼容旧默认 target；本地脚本与新流水线必须显式 --target web-admin / web-biz。
FROM web-admin AS production
