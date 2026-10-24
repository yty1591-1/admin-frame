# 追踪链路（OTel Collector / Tempo / Jaeger）

> 关联制品：[`deploy/observability/otel/collector.yaml`](../../deploy/observability/otel/collector.yaml)；
> 采样策略见 [`deploy/observability/README.md`](../../deploy/observability/README.md) §2。

## 症状

- 应用 `tracing.exporter=otlp` 正常，但后端查不到新 span。
- Collector `/healthz`（:13133）失败、内存限流触发、导出队列满。
- Trace 只到 HTTP 层，缺少 DB/Redis/下游 span。
- 采样率异常（Span 量骤降或暴涨）。

## 影响

- 追踪缺失**不影响业务请求**（导出异步、失败不阻塞），但会显著降低排障效率。
- SLO/告警仍可通过指标与日志工作；跨服务定位依赖 trace_id 关联。
- 长期断链会导致审计/合规要求的可追溯性下降。

## 排查

1. Collector 健康：`curl -sf http://otel-collector:13133`；查看 `memory_limiter` 与 `sending_queue` 日志。
2. 应用端：确认 `tracing.enabled=true`、`exporter=otlp`、`endpoint` 指到 Collector:4318。
3. 采样：确认应用侧 `sample_ratio=1.0`（交给尾部采样），Collector 策略是否误改。
4. 后端：Tempo/Jaeger 健康与存储容量；查询 `{service.name="admin-api"}`。
5. 跨服务：检查 W3C `traceparent` 是否透传（网关/反向代理不得剥离）。

## 缓解与恢复

| 场景 | 动作 |
|---|---|
| Collector 不可用 | 应用自动降级（丢 span）；恢复 Collector 后无需重启应用 |
| 队列/内存打满 | 临时下调 tail 采样百分比或扩容 Collector 副本 |
| 后端不可查 | 切换 `exporters` 为备用 Jaeger，重启 Collector |
| 采样配置错误 | 回滚 `collector.yaml`；应用侧比例保持 1.0 |
| 断链 | 检查代理是否透传 `traceparent`；确认中间件顺序为 tracing → RequestID |

## 升级

- 追踪缺失影响 P1 排障 → 升级 L2（平台 Owner）并在事件中标注。
- Collector 持续崩溃或后端数据丢失 → L3（SRE/架构 Oncall）。
- 涉及采样策略变更 → 需 Review 后发布，禁止线上直接改。

## 相关链接

- 配置：[`collector.yaml`](../../deploy/observability/otel/collector.yaml)
- 代码：[`pkg/tracing`](../../pkg/tracing)、[`internal/platform/bootstrap.go`](../../internal/platform/bootstrap.go)
- 规范：[`docs/OBSERVABILITY_STANDARDS.md`](../OBSERVABILITY_STANDARDS.md)
- 看板：Grafana `Admin Frame / RED 总览`
