# syntax=docker/dockerfile:1
# RELS：禁止 :latest；基础镜像钉扎 minor；生产推送推荐锁定 digest。

# 构建阶段固定在 BUILDPLATFORM（runner 原生架构），按 TARGETOS/TARGETARCH 交叉编译，
# 多架构（amd64/arm64）构建无需逐架构 QEMU 编译 Go 代码。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine3.24 AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD=admin-api
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
  -o /out/app ./cmd/${CMD}

FROM alpine:3.24 AS runtime
RUN adduser -D -u 10001 appuser \
  && apk add --no-cache ca-certificates tzdata wget \
  && mkdir -p /app/configs
WORKDIR /app
COPY --from=builder /out/app /app/app
COPY configs/*.yaml.example /app/configs/
COPY migrations /app/migrations
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/app"]

FROM runtime AS admin-api
FROM runtime AS biz-api
FROM runtime AS c-api
FROM runtime AS worker
# 迁移 Job 专用镜像（ENT-05 T7）；与 API 共用运行时基座与迁移文件。
FROM runtime AS migrate
# 首个管理员开通 Job 专用镜像（identity-authn §7.2.1 D4）；只用于一次性 seed。
FROM runtime AS identity-seed
