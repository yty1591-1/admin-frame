# 技术栈清单与部署建议

> 适用范围：`admin-api` / `biz-api` / `c-api` / `worker` 四个进程与 `migrate` 一次性任务的生产部署。
> 本文所有版本与配置项均来自仓库实现（[go.mod](../go.mod)、[configs](../configs)、[internal/platform/config.go](../internal/platform/config.go)、[Dockerfile](../Dockerfile)），资源数值为建议基线，需按压测结果调整。
> 相关标准：[ARCHITECTURE.md](../docs/ARCHITECTURE.md)、[CONFIG_STANDARDS.md](../docs/CONFIG_STANDARDS.md)、[OBSERVABILITY_STANDARDS.md](../docs/OBSERVABILITY_STANDARDS.md)、[RELEASE_STANDARDS.md](../docs/RELEASE_STANDARDS.md)、[SECURITY_STANDARDS.md](../docs/SECURITY_STANDARDS.md)。

## 1. 运行形态

| 进程 | 职责 | 默认端口（configs 示例） | 是否对外 |
|---|---|---|---|
| `admin-api` | A 端管理 API、IAM、身份认证、MFA | `:8081` | 是 |
| `biz-api` | B 端商户 API | `:8082` | 是 |
| `c-api` | C 端用户 API、短信 / OAuth 登录 | `:8083` | 是 |
| `worker` | Outbox Relay 与异步消费，独占投递 | `:8090` | 否（仅探针 / 指标） |
| `migrate` | schema 迁移 CLI，唯一执行入口 | 无 | 否，一次性 Job |

端口由配置项 `addr` 决定；镜像内 `EXPOSE 8080` 仅作声明，必须与 `addr` 对齐（见 §11）。

## 2. 技术栈清单

### 2.1 语言与运行时

| 项 | 版本 / 选型 | 说明 |
|---|---|---|
| Go | `1.26.10`（[go.mod](../go.mod#L3)） | 使用 `net/http` 方法化路由，需 Go ≥ 1.22 |
| HTTP 框架 | 标准库 `net/http` | 无 Web 框架，中间件在 [internal/platform](../internal/platform/middleware.go) |
| 请求校验 | `go-playground/validator/v10 v10.27.0` | 传输层 DTO 校验 |
| 配置 | `gopkg.in/yaml.v3 v3.0.1` + `ADMIN_FRAME_` 环境变量 | 优先级：环境变量 > 配置文件 > 默认值 |

### 2.2 数据与存储

| 项 | 版本 / 选型 | 用途 | 生产必要性 |
|---|---|---|---|
| MySQL | 8.0（utf8mb4_0900_ai_ci） | IAM、身份域、Outbox、幂等记录 | 必备 |
| PostgreSQL | `jackc/pgx/v5 v5.11.0` | `pkg/db` 与迁移工具支持的另一种关系库 | 可选（与 MySQL 二选一） |
| Redis | 7+（`redis/go-redis/v9 v9.14.0`） | 撤销快照与 jti 黑名单、MFA 票据、短信验证码、限流、IAM L2 缓存、幂等 | 生产必备 |
| 对象存储 | AWS S3 / MinIO / 阿里云 OSS / 腾讯云 COS / 华为云 OBS | 文件与导出 | 按业务，生产禁止 `memory` |
| 消息中间件 | NATS JetStream（`nats.go v1.55.0`；可选） | 跨服务/跨团队事件分发 | 默认 `async.driver=db` 无需 broker；跨团队消费时再启用 `nats` |

### 2.3 安全与认证

| 项 | 版本 / 选型 | 用途 |
|---|---|---|
| JWT | `golang-jwt/jwt/v5 v5.2.3` | Access Token 签发与验签，支持 HS256 / RS256 + `kid` 轮换 |
| 密钥派生 / 哈希 | `golang.org/x/crypto v0.57.0` | Argon2id、bcrypt |
| TOTP | 自研（`crypto/hmac` + SHA1，`pkg/totp`） | MFA 动态码 |
| 对称加密 | `crypto/aes` AES-256-GCM | TOTP 密钥加密 |
| OAuth2 / OIDC | 自研（授权码 + PKCE，`infrastructure/oauth`） | C 端第三方登录 |
| 短信 | `pkg/sms` + 阿里云驱动 | C 端验证码 |

### 2.4 可观测性

| 项 | 版本 / 选型 | 端点 / 说明 |
|---|---|---|
| 日志 | 标准库 `log/slog`（JSON） | prod 禁止 `text` |
| 指标 | `prometheus/client_golang v1.23.2` | `GET /metrics`，无鉴权，需网络隔离 |
| 链路追踪 | OpenTelemetry SDK `v1.45.0` + OTLP HTTP exporter | `tracing.exporter=otlp`，接入 Collector |
| 健康检查 | 平台内置 | `GET /healthz`（liveness）、`GET /readyz`（依赖就绪） |

### 2.5 工程与交付工具链

| 项 | 版本 / 选型 | 位置 |
|---|---|---|
| Lint | `golangci-lint v2.14.0` | `make lint`、CI `lint` job |
| 格式化 | `gofmt` + `goimports v0.30.0`（本地前缀 `github.com/admin-frame/admin-frame`） | `make fmt-check` |
| 依赖漏洞 | `govulncheck v1.1.4` | `make vuln` |
| 镜像扫描 | Trivy（CRITICAL/HIGH，`ignore-unfixed`） | [.github/workflows/image.yml](../.github/workflows/image.yml) |
| 镜像构建 | Docker Buildx，多架构 `linux/amd64,linux/arm64`，`CGO_ENABLED=0` | [Dockerfile](../Dockerfile) |
| 迁移 | 自研 `cmd/migrate`（up/down/status/version，支持 dry-run、advisory lock） | `make build-migrate` |
| 集成测试依赖 | MySQL 8.0、Redis 7、MinIO | [docker-compose.integration.yml](../docker-compose.integration.yml) |

## 3. 部署拓扑

```
                    ┌──────────── Ingress / WAF / TLS ────────────┐
                    │   /admin        /biz        /c              │
                    └──────┬────────────┬───────────┬─────────────┘
                           ▼            ▼           ▼
                     ┌──────────┐ ┌──────────┐ ┌──────────┐
                     │ admin-api│ │ biz-api  │ │  c-api   │   各 2+ 副本
                     └────┬─────┘ └────┬─────┘ └────┬─────┘
                          │            │            │
          ┌───────────────┼────────────┼────────────┼──────────────┐
          ▼               ▼            ▼            ▼              ▼
      ┌────────┐     ┌─────────┐  ┌────────┐  ┌────────────┐  ┌──────────┐
      │ MySQL  │     │ Redis   │  │ 对象存储 │  │ OTel       │  │ Prometheus│
      │ 8.0    │     │ 7       │  │ S3/OSS… │  │ Collector  │  │ + Alert   │
      └────────┘     └─────────┘  └────────┘  └────────────┘  └──────────┘
          ▲               ▲
          │               │
     ┌────┴───────────────┴────┐
     │ worker（Outbox Relay）  │  1～2 副本，独占投递
     └─────────────────────────┘
          ▲
     ┌────┴─────────────┐
     │ migrate（Job）    │  发布前一次性执行
     └──────────────────┘
```

## 4. 资源建议基线

| 组件 | 开发 / 测试 | 预发 | 生产建议 |
|---|---|---|---|
| `admin-api` / `biz-api` / `c-api` | 1 副本，0.2 CPU / 256Mi | 2 副本 | 每端 ≥ 2 副本；0.5–1 CPU / 512Mi 起，按 P95 与 HPA 调整 |
| `worker` | 1 副本 | 1 副本 | 1–2 副本；0.5–1 CPU / 512Mi；`outbox.concurrency` 2 起步 |
| MySQL | 单实例 | 2C4G，主从 | 4C8G 起，云 RDS 高可用；按写入与 Outbox 增长扩容 |
| Redis | 单实例 | 1C2G 主从 | 2C4G 主从 / 集群；撤销与限流为强依赖 |
| 对象存储 | MinIO 单节点 | 云 OSS/S3 | 云对象存储 + 生命周期规则 |
| OTel Collector | 可省略（`exporter=none`） | 1 副本 | 2 副本，0.2–0.5 CPU / 256Mi |
| Prometheus / Grafana | 可选 | 必备 | 必备，保留 ≥ 15 天 |

## 5. 关键环境变量与密钥

配置优先级为 **环境变量 > 配置文件 > 默认值**，变量名由 yaml 标签推导（前缀 `ADMIN_FRAME_`）。

| 变量 | 对应配置 | 是否密钥 | 说明 |
|---|---|---|---|
| `ADMIN_FRAME_ENV` | `env` | 否 | `local/dev/test/staging/prod`，决定严格校验 |
| `ADMIN_FRAME_SERVICE` | `service` | 否 | `admin-api/biz-api/c-api/worker` |
| `ADMIN_FRAME_CHANNEL` | `channel` | 否 | `admin/biz/c/worker`，装配期固定 |
| `ADMIN_FRAME_ADDR` | `addr` | 否 | 建议容器内统一 `:8080` |
| `ADMIN_FRAME_AUTH_MODE` | `auth.mode` | 否 | prod 必须 `jwt` |
| `ADMIN_FRAME_AUTH_JWT_ALG` / `_ISSUER` / `_PUBLIC_KEY_FILE` | `auth.jwt.*` | 部分 | RS256 用公钥文件；HS256 密钥走 `ADMIN_FRAME_AUTH_JWT_SECRET`（≥32 字节） |
| `ADMIN_FRAME_DB_DSN` | `db.dsn` | **是** | 运行时账号，最小权限 |
| `ADMIN_FRAME_REDIS_PASSWORD` | `redis.password` | **是** | 禁止写入 yaml |
| `ADMIN_FRAME_ID`/`ADMIN_FRAME_ID_NODE_ID` | `id.node_id` | 否 | 每副本唯一；`id.node_id_source=env/config/statefulset` |
| `ADMIN_FRAME_STORAGE_S3_ACCESS_KEY` / `_SECRET_KEY` | `storage.s3.*` | **是** | OSS/COS/OBS 同理按驱动替换 |
| `ADMIN_FRAME_IDENTITY_MFA_SECRET_KEY` | `identity.mfa.secret_key` | **是** | 32 字节，支持 raw/hex/base64/base64url；`admin_required=true` 时必填 |
| `ADMIN_FRAME_ASYNC_BROKER_URL` | `async.broker.url` | **是** | `async.driver=nats` 时必填；可含账号口令，禁止写入 yaml；prod/staging 必须 `tls://`/`wss://` |
| `ALIYUN_SMS_ACCESS_KEY_ID` / `ALIYUN_SMS_ACCESS_KEY_SECRET` | 短信驱动凭证 | **是** | 无 `ADMIN_FRAME_` 前缀，由驱动直接读取 |
| `<PROVIDER>_OAUTH_CLIENT_SECRET` | `identity.oauth.providers[].client_secret_env` | **是** | 配置只写变量名，值由 Secret 注入 |

密钥注入方式：Kubernetes Secret / External Secrets / Vault / 云 Secrets Manager。禁止写入镜像、仓库或 ConfigMap 明文。

## 6. 生产必配项（启动期硬校验）

以下约束由 `Config.Validate()` 强制执行，`env=prod` 或 `staging` 时不满足即启动失败。

| 配置 | 生产要求 |
|---|---|
| `auth.mode` | 必须 `jwt`；`demo` 在 `env=prod` 下直接拒绝启动 |
| `log.format` | 必须 `json` |
| `http.timeout` | 必须 > 0 |
| `tracing.exporter` | 禁止 `stdout`；可为 `none`（仅生成 TraceID）或 `otlp`（推荐接入 Collector） |
| `id.node_id` | 非 `statefulset` 来源时必须显式配置且每副本唯一；`statefulset` 时禁止手工设置 |
| `idempotency.enabled` | 为 true 时 `driver` 必须 `redis` |
| `rate_limit.enabled` | 为 true 时 `driver` 必须 `redis` |
| `iam.mode=enforce` | `iam.cache.driver=redis`；`async.enabled=true`；`async.outbox.publish_in_api=false` |
| `iam.security.allow_wildcard` | 必须 false |
| `iam.breakglass.enabled` | 启用时 `allowed_subjects` 必填 |
| `iam.approval.enabled` | `db.driver` 必须持久化，禁止 `memory` |
| `identity.revocation.failure_mode` | 必须 `fail_closed` |
| `identity.mfa.admin_required` | 必须 true；且 `identity.mfa.secret_key` 必填、`issuer` 非空 |
| `identity.sms.driver` | 禁止 `memory` |
| `channel=c` | `identity.sms.enabled` 或至少一个 `identity.oauth.providers` |
| OAuth URL | 必须 `https` |

> 以下为部署要求，代码不强制但生产必须遵守：`db.driver` 使用 `mysql`/`postgres`、`storage.driver` 禁止 `memory`、`iam.mode=enforce`、`identity.enabled=true` 时 Redis 可用且 `auth.mode=jwt`。

## 7. 部署流程

1. **构建镜像**
   - 使用 [image.yml](../.github/workflows/image.yml) 产出多架构镜像（含 `migrate`）；标签只用 `sha-*` / semver，禁止 `latest`。
   - 镜像以非 root（uid 10001）运行，`CGO_ENABLED=0`，含 `ca-certificates` 与 `tzdata`；`migrations/` 已内置于镜像 `/app/migrations`。
2. **准备配置**
   - 以 `configs/<service>.yaml.example` 为模板生成各环境配置，挂载到容器 `/app/configs/<service>.yaml`。
   - 容器内只有 `.example`，不挂载真实配置文件时进程会因找不到配置而退出。
3. **执行迁移（独立 Job）**
   - K8s 推荐使用 Helm Chart 的 `migrate.enabled=true`（post-install/post-upgrade Hook），发布命令必须带 `--atomic --wait`；失败自动回滚。
   - 手工执行时先 `-action status` 与 `-action up -dry-run` 查看计划，再 `-action up`。
   - 使用迁移专用账号与 `ADMIN_FRAME_DB_DSN`；严禁 API 进程自动迁移。
4. **启动 worker**
   - 生产 `iam.mode=enforce` 要求 `async.enabled=true` 且 API 不投递，`worker` 必须先于 API 就绪，避免 Outbox 积压。
5. **滚动发布 API**
   - 顺序：`worker` → `admin-api` / `biz-api` / `c-api`；每端 `maxUnavailable=0`、`maxSurge=1`。
6. **发布后验证**
   - `/healthz` 返回 200；`/readyz` 在 DB、Redis、对象存储可用后返回 200。
   - 执行登录 / 刷新 / 登出冒烟；确认审计事件进入 Outbox 并由 worker 投递。
   - 观察 5xx、P95、Outbox 积压、撤销检查失败率。

## 8. Kubernetes 建议

交付物见 [`helm/admin-frame`](./helm/admin-frame/README.md)（三端 API + worker + migrate Job，
含探针、HPA、PDB、NetworkPolicy、ServiceMonitor）。

| 项 | 建议 |
|---|---|
| 工作负载 | Helm 默认 Deployment；staging/prod 多副本启用 `statefulset.enabled=true`（`id.node_id_source=statefulset`） |
| 探针 | liveness `GET /healthz`（period 10s）；readiness `GET /readyz`（period 5s，失败阈值 3）；建议加 startupProbe |
| 优雅停机 | `terminationGracePeriodSeconds ≥ 30`，大于 `shutdown_timeout`（默认 15s） |
| 资源 | requests 按 §4 下限，limits 为 requests 的 1.5–2 倍；`/metrics` 不计入业务超时 |
| PDB | `minAvailable: 1`，防止滚动与节点维护同时清空副本 |
| HPA | 以 CPU 70% 或自定义 RPS 指标扩缩；**扩缩前必须保证 `id.node_id` 唯一** |
| 节点号 | 动态副本建议 `StatefulSet` + `id.node_id_source=statefulset`；否则固定副本数并手工分配 `node_id` |
| 安全上下文 | `runAsNonRoot`、`readOnlyRootFilesystem`、`drop ALL` capabilities、seccomp `RuntimeDefault` |
| 网络 | NetworkPolicy 限制出站仅 DB、Redis、对象存储、OTel Collector；`/metrics` 仅监控网段可达 |
| 配置 | 非密钥配置用 ConfigMap；密钥用 Secret / External Secrets，独立于镜像 |

## 9. 可观测性与告警

**指标（Prometheus 抓取 `/metrics`）**

| 指标类别 | 关注点 | 告警建议 |
|---|---|---|
| HTTP | QPS、P95/P99、5xx 比例 | 5xx > 1% 持续 5 分钟；P99 > 1s 持续 10 分钟 |
| Outbox | 待投递积压、最老事件年龄、投递失败数 | 积压 > 1000 或最老事件 > 5 分钟 |
| NATS（`async.driver=nats`） | `async_broker_publish_total{result=error}`、`async_broker_consume_total{result=retry/dead}`、DLQ subject 深度 | 发布失败率 > 1% 或 dead 持续增长 |
| 消费 | 失败率、重试次数、死信数 | 死信数 > 0 立即告警 |
| IAM 缓存 | 命中率、回源延迟、加载超时 | 命中率 < 80% 或回源 P95 > 200ms |
| 身份撤销 | 检查延迟、`fail_closed` 触发次数 | 任何 50300 突增立即告警 |
| 连接 | DB 连接池使用率、Redis 连接数 | 使用率 > 85% |

**日志与追踪**
- 统一 JSON 日志，保留 `trace_id`、`request_id`、`channel`；审计事件单独落到不可篡改存储。
- `tracing.exporter=otlp`，`endpoint` 指向 Collector（如 `http://otel-collector:4318`，内网可用 `insecure: true`）。
- 采样：应用侧 `sample_ratio=1.0`，由 Collector 尾部采样（错误/慢请求 100%、正常 10%）；未部署 Collector 时应用侧 5%–10%。
- `otlp` 导出失败不得阻塞业务请求（SDK 异步导出）。

**规则、SLO 与看板**

- 告警规则：[`deploy/observability/prometheus/rules`](./observability/prometheus/rules)；SLO 与燃烧率见 `slo.yaml`。
- Alertmanager：[`alertmanager.yaml`](./observability/alertmanager/alertmanager.yaml)（P1 立即呼叫、P2 工单、抑制规则）。
- 看板：[`deploy/observability/grafana/dashboards`](./observability/grafana/dashboards)（RED / IAM / Async / DB）。
- 入口说明：[`deploy/observability/README.md`](./observability/README.md)；告警 ↔ runbook：[`docs/runbooks/ALERTS.md`](../docs/runbooks/ALERTS.md)。

## 10. 可靠性与容量

| 主题 | 建议 |
|---|---|
| MySQL | 主从 / 云高可用；`max_connections` 与各 API 副本连接池上限之和留 30% 余量；慢查询阈值 200ms；Outbox 表按时间分区或定期归档 |
| Redis | 主从 + 哨兵或集群；撤销与限流共用实例时按 key 前缀隔离；`maxmemory-policy` 使用 `volatile-lru`，禁止全量淘汰导致撤销状态丢失 |
| 撤销语义 | `identity.revocation.failure_mode=fail_closed`：Redis 故障返回 50300，宁可拒绝不放行；需配套告警与容量冗余 |
| 限流语义 | `rate_limit.failure_mode=fail_open`：Redis 故障时放行，避免全站不可用，但需监控 |
| 幂等 | 启用后必须 Redis 驱动；Redis 数据丢失会产生重复写窗口，关键写接口建议同时保留数据库唯一键兜底 |
| Outbox | 单条 `lease=30s`、`batch_size=100`、`max_attempts=10`；多 worker 副本依靠租约安全认领；死信需人工/定时重放 |
| 备份 | MySQL 每日全备 + binlog（PITR ≥ 7 天）；对象存储开启版本化；脚本与 CronJob 见 [`scripts/backup`](../scripts/backup) 与 [`deploy/backup/cronjob.yaml`](./backup/cronjob.yaml)；目标 RPO ≤ 5min、RTO ≤ 60min |
| 恢复演练 | 每季度至少一次从备份恢复到独立环境，验证迁移版本与数据一致性；步骤见 [`docs/runbooks/backup-restore.md`](../docs/runbooks/backup-restore.md) |
| 发布回滚 | 镜像不可变 + semver；回滚使用上一版本镜像；数据库变更遵循「先兼容、后清理」两阶段 |

## 11. 常见部署坑（本项目实测约束）

1. **端口不一致**：Dockerfile `EXPOSE 8080` 且 `HEALTHCHECK` 固定探测 `127.0.0.1:8080`，而示例配置为 `8081/8082/8083/8090`。容器内请统一 `ADMIN_FRAME_ADDR=:8080`，或同步修改探针与 Service `targetPort`。
2. **配置文件缺失**：镜像只复制 `configs/*.yaml.example`，默认 `-config configs/admin.yaml` 不存在，必须挂载真实配置文件或显式传 `-config`。
3. **雪花 ID 冲突**：`id.node_id` 重复会生成相同 ID。多副本必须唯一；HPA 动态扩缩与手工编号不兼容。
4. **demo 鉴权**：`env=prod` 下 `auth.mode=demo` 直接启动失败，发布前先完成 JWT 配置与密钥注入。
5. **身份域启用前置**：`identity.enabled=true` 要求 `auth.mode=jwt` 且 Redis 可用；C 端还要求短信或 OAuth 至少一项。
6. **MFA 失败关闭**：`identity.mfa.admin_required=true` 时未配置 `secret_key` 会启动失败，而不是降级为无 MFA。
7. **迁移入口唯一**：不要用应用启动参数或容器 entrypoint 触发迁移；只能通过 `migrate` Job。
8. **异步前置**：`async.enabled=true` 要求非 `memory` 数据库；`iam.mode=enforce` 时 API 侧 `publish_in_api` 必须为 false，否则配置校验失败。
9. **NATS（可选）**：`async.driver=nats` 时 `broker.url` 必填且生产必须 TLS；`broker.stream`（默认 `async`）由适配器自动创建并保留 24h；`max_deliver` 超限进入 `<subject_prefix>.dlq.<topic>`，需按 runbook 重放；broker 故障时 Outbox 会重试，不会丢事件。
10. **`/metrics` 无鉴权**：只能通过 NetworkPolicy / Ingress 规则限制在监控网段。

## 12. 上线检查清单

- [ ] `env=prod`，所有 §6 硬校验项已满足（启动日志无校验错误）
- [ ] `auth.mode=jwt`，公钥/密钥已注入并完成轮换预案
- [ ] `db.driver=mysql`，迁移 Job 已成功且 `status` 为最新版本
- [ ] Redis 地址、密码、主从/集群已就绪；撤销 `fail_closed` 告警已配置
- [ ] `id.node_id` 每副本唯一，扩缩容策略与编号方式一致
- [ ] `storage.driver` 非 `memory`，密钥由 Secret 注入，桶权限最小化
- [ ] `worker` 先于 API 启动，Outbox 无积压，死信监控已接入
- [ ] 若启用 `async.driver=nats`：broker 已 TLS、stream/DLQ subject 已建、`async_broker_*` 指标已告警
- [ ] Helm Chart 已通过 `helm lint` + 三套 values `helm template`；生产镜像使用 digest
- [ ] OTel Collector 已就绪，Tempo/Jaeger 可查 `service.name`，采样策略已确认
- [ ] Prometheus 规则 + Alertmanager 已加载，P1/P2 告警可触达值班
- [ ] 备份 CronJob 已运行成功，`verify.sh` 通过，恢复演练完成并归档
- [ ] `/healthz`、`/readyz`、`/metrics` 探针与网络策略验证通过
- [ ] JSON 日志与 trace 已进入集中平台，审计事件可检索
- [ ] 备份、恢复演练、回滚镜像与 Runbook 已就绪
- [ ] 镜像无 `latest` 标签，Trivy CRITICAL/HIGH 为 0（或已批准例外）
