# 可观测性交付物（ENT-06）

> 覆盖三条链路：追踪后端（Collector → Tempo/Jaeger）、告警规则与 SLO、看板。
> 指标契约见 [`docs/OBSERVABILITY_STANDARDS.md`](../../docs/OBSERVABILITY_STANDARDS.md)；
> 告警 ↔ runbook 目录见 [`docs/runbooks/ALERTS.md`](../../docs/runbooks/ALERTS.md)。

## 1. 目录

```text
deploy/observability/
├── otel/collector.yaml              # OTLP 接收 + 尾部采样 + 导出 Tempo/Jaeger
├── prometheus/rules/                # api / iam / async / db / slo 规则组
├── alertmanager/alertmanager.yaml   # P1 立即呼叫、P2 工单、抑制规则
└── grafana/dashboards/              # RED / IAM / Async / DB 四张看板
```

## 2. 追踪后端与采样

### 2.1 拓扑

```text
admin/biz/c/worker ──OTLP/HTTP :4318──► OTel Collector ──OTLP/gRPC──► Tempo（或 Jaeger）
                                              │
                                              └── 自身指标 :8888（可选抓取）
```

应用侧由 [configs/prod/modules/infra.yaml.example](../../configs/prod/modules/infra.yaml.example) 的 `tracing` 段控制：

```yaml
tracing:
  enabled: true
  exporter: otlp
  endpoint: otel-collector.observability.svc.cluster.local:4318
  sample_ratio: 1.0   # 交给 Collector 尾部采样时保持 1.0
  insecure: true      # 集群内明文；跨集群/公网必须 TLS
```

生产硬校验：`env=prod` 禁止 `exporter=stdout`；`exporter=otlp` 时 `endpoint` 必填（`Config.Validate`）。

### 2.2 采样策略

| 场景 | 应用侧 `sample_ratio` | Collector | 说明 |
|---|---|---|---|
| 接入 Collector（推荐） | `1.0` | 尾部采样：错误 100%、>500ms 100%、正常 10% | 错误样本在入口不被丢弃 |
| 未部署 Collector | `0.05`–`0.1` | — | 只生成本地 TraceID 或直连后端 |
| 本地 / 单测 | 默认 | — | `exporter=none` 只生成 TraceID，不导出 |

调整容量时优先改 `collector.yaml` 的 `probabilistic.sampling_percentage` 与
`expected_new_traces_per_sec`，不要同时降低应用侧比例。

### 2.3 部署 Collector

```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  -n observability --create-namespace \
  -f deploy/observability/otel/collector.yaml   # 也可通过 --set 挂 ConfigMap
```

> 需要 Contrib 发行版（`tail_sampling`、`otlp` exporter）。Tempo 用
> `grafana/tempo` Chart；Jaeger 用 `jaegertracing/jaeger` Chart，并将
> `service.pipelines.traces.exporters` 改为 `[otlp/jaeger]`。

### 2.4 验证与保留

- `kubectl -n observability port-forward svc/otel-collector 13133:13133` 后
  `curl -sf localhost:13133` 返回 200。
- 触发一次请求，在 Tempo/Jaeger 按 `service.name` + `deployment.channel` 查询；
  断言 span 链路覆盖 HTTP → DB/Redis/外部依赖。
- 保留期建议：Tempo/Jaeger 7 天热查 + 对象存储 30 天归档；Collector 不落盘。
- Collector 不可用时应用仅丢失 span，不影响请求（导出异步 + 失败不阻塞）。

## 3. 告警规则与 SLO

### 3.1 加载规则

```bash
# Prometheus（原生或 Operator 统一入口）
promtool check rules deploy/observability/prometheus/rules/*.yaml
```

Prometheus Operator 用户可按团队约定把 `rules/*.yaml` 转为 `PrometheusRule` 资源，
或把目录挂载为 Prometheus 的 `rule_files`；`promtool` 是统一校验入口。

`Alertmanager` 配置以 `alertmanager.yaml` 为基线，接收端 URL 为占位，接入时替换。

### 3.2 SLO 定义（未确认前只记录不阻断）

| SLI | 目标 | 窗口 | 预算 |
|---|---|---|---|
| 可用性（非 5xx 占比） | ≥ 99.9% | 滚动 30 天 | 约 43 分钟/月 |
| 时延（P99） | < 1s | 滚动 30 天 | 与 RED 面板阈值一致 |

燃烧率告警：`SLOAvailabilityBurnFast`（5m+1h > 14.4x，P1）与
`SLOAvailabilityBurnSlow`（1h+6h > 6x，P2）。预算策略见
[`docs/runbooks/slo-error-budget.md`](../../docs/runbooks/slo-error-budget.md)。

### 3.3 告警 ↔ 规则 ↔ runbook 一致性

`scripts/ci/check-alerts.sh` 会解析 `ALERTS.md` 与规则文件，要求：

- 每个 P1/P2 告警 ID 在规则中存在同名 `alert_id`；
- 规则中的 `runbook_url` 指向存在的文件；
- 规则级别与目录级别一致。

## 4. 看板

| 文件 | 内容 |
|---|---|
| `red-overview.json` | QPS、5xx 比率、P99、in-flight |
| `iam.json` | 鉴权决策、缓存、加载错误、Outbox、破玻璃 |
| `async.json` | Outbox 积压、投递结果、broker、DLQ |
| `db.json` | 连接使用率、open/idle、等待耗时 |

导入：Grafana → Dashboards → Import → 上传 JSON，选择 Prometheus 数据源
（模板变量 `DS_PROMETHEUS`）。指标重命名必须同 MR 更新看板与规则。

## 5. 故障注入验证（staging）

| 场景 | 注入方式 | 期望告警 |
|---|---|---|
| 5xx 升高 | 关闭下游或注入错误路由 | `API5xxRateHigh` / `SLOAvailabilityBurnFast` |
| 连接池耗尽 | 压测并调低 `db.max_open_conns` | `DBPoolExhaustion` |
| Outbox 积压 | 停 worker 一段时间 | `AsyncOutboxBacklog` / `WorkerQueueLag` |
| 追踪断链 | 停 Collector | 无告警（预期降级），恢复后 span 继续 |
