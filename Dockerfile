# syntax=docker/dockerfile:1
# RELS：禁止 :latest；基础镜像钉扎 minor；生产推送推荐锁定 digest。

FROM golang:1.26-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD=admin-api
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
  -o /out/app ./cmd/${CMD}

FROM alpine:3.20 AS runtime
RUN adduser -D -u 10001 appuser \
  && apk add --no-cache ca-certificates tzdata wget \
  && mkdir -p /app/configs
WORKDIR /app
COPY --from=builder /out/app /app/app
COPY configs/*.yaml.example /app/configs/
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/app"]

FROM runtime AS admin-api
FROM runtime AS biz-api
FROM runtime AS c-api
FROM runtime AS worker
