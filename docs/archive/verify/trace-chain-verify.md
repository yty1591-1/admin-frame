# 链路验证：入口 → DB（ENT-06 AC-ENT-06-4）

> **归档**：本文件已于 2026-11-07 归档，只读不再维护；现行结论以 [`OBSERVABILITY_STANDARDS.md`](../../OBSERVABILITY_STANDARDS.md) 为准，归档索引见 [`archive/README.md`](../README.md)。

> **结论**：达成。kind 集群上实测一次 `GET /admin/v1/audit/events` 的 trace 为
> `GET /admin/v1/audit/events` → `sql.conn.query`（`db.system=mysql`，带 SQL 语句），
> 父子关系正确、可从入口追到 DB。
>
> **验证日期**：2026-11-03　**环境**：kind `af-verify`（v1.34.3）+ Jaeger 1.57 + OTel Collector Contrib 0.104.0

---

## 1. 验证方法

```bash
# 1) 部署 trace 后端（Jaeger + Collector，配置由 deploy/observability/otel/collector.yaml 派生）
bash scripts/observability/install-kind-otel.sh

# 2) 应用开启 OTLP 导出
helm upgrade af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  --set config.auth.mode=demo --set image.tag=v5 \
  --set config.tracing.exporter=otlp \
  --set config.tracing.endpoint=otel-collector.monitoring.svc.cluster.local:4318 \
  --set config.tracing.insecure=true -n admin-frame

# 3) 造一次带 DB 查询的请求
kubectl -n admin-frame exec deploy/af-kind-admin-frame-admin -- \
  wget -qO /dev/null --header='Authorization: Bearer admin.t6' \
  "http://127.0.0.1:8080/admin/v1/audit/events?from=...&to=..."

# 4) 断言链路
python3 scripts/observability/check-trace-chain.py --service admin-api --route /admin/v1/audit/events
```

判定条件（`check-trace-chain.py`）：同一条 trace 内存在 `span.kind=server` 的入口 span、
存在 `sql.*` 且带 `db.system` 的 `client` span，且后者沿 `references` 上溯可到达入口 span。

## 2. 实测输出

```text
检查 6 条 trace（service=admin-api，route≈/admin/v1/audit/events）
PASS: trace b8fb9856cf592ea342e790464d104c1a 入口 → DB 链路成立
- GET /admin/v1/audit/events (7.7ms)  []
  - sql.conn.query (0.0ms)  [mysql]  SELECT event_id, action, actor_id, actor_type, channel, merchant_id,
	resource_t…
```

修复前同一条链路的入口 span 名为 `GET unknown`，Jaeger 中 37 个入口 span 全部同名，
按路由检索与聚合完全失效（见 §3.4）。

## 3. 过程中发现并修复的缺陷

只有真跑才会暴露的四类问题，全部已修并补测试/门禁：

### 3.1 Collector 启动即崩：`retry` 不是合法键

```
error decoding 'exporters': error reading configuration for "otlp/tempo":
  '' has invalid keys: retry
```

`deploy/observability/otel/collector.yaml` 里 exporter 用了 `retry:`，Collector 0.104 只认
`retry_on_failure:`。**这份配置此前从未被真正加载过**，按此上线 Collector 会直接
CrashLoopBackOff，连带全链路 trace 丢失。已改为 `retry_on_failure` 并加注释说明。

### 3.2 Collector 二次绑定 8888 端口

```
Serving metrics {"address": "0.0.0.0:8888"}
Serving metrics {"address": ":8888"}
error: listen tcp 0.0.0.0:8888: listen: address already in use
```

`service.telemetry.metrics.readers[].pull.exporter.prometheus` 与 Collector 的默认监听
同时生效，导致同进程绑两次。已改用 `metrics.address: 0.0.0.0:8888`。

### 3.3 DB 层没有任何 span（本体缺口）

`pkg/db` 直接 `sql.Open`，Query/Exec 不产生 span，`AC-ENT-06-4` 的「追到 DB」无从谈起。

- 新增 `db.OpenTraced(ctx, cfg, tp)`：`tp` 非空时用 `github.com/XSAM/otelsql` 包装驱动，
  SQL 调用产生 CLIENT span（`db.system` / `db.name` / 语句）；
  `tp` 为空（worker、迁移）仍是裸 `database/sql`，行为不变。
- span 父节点取自调用方 context，`HTTP 入口 span → SQL span` 由此串成一条链。
- 选 otelsql 而非自研 driver 包装：包装器必须精确转发 `CheckNamedValue` /
  `ResetSession` / `IsValid` / `ColumnConverter`，漏一个就静默改变驱动行为
  （pgx 依赖 `CheckNamedValue` 直通原生类型），这类错误只在真库上暴露。
- 单测：`pkg/db/tracing_test.go` 用最小 stub 驱动验证「有 tp → 产出 `sql.conn.query`
  且父节点等于调用方 span」「无 tp → 一个 span 都不产生」。

### 3.4 入口 span 全部叫 `GET unknown`

`pkg/tracing` 在 `next.ServeHTTP` 之后读 `req.Pattern` 回填名字，但 `middleware.Timeout`
用 `r.WithContext(ctx)` 复制了请求，`ServeMux` 只把 Pattern 写在「传给它的那份请求」上，
外层永远读不到 → 名字退化为 `GET unknown`，`http.route` 属性为空。

- 新增 `tracing.RoutePatternMiddleware()`，**放在中间件链最内层**（紧贴 mux），
  匹配完成后直接把 Pattern 写回当前 span 的名称与 `http.route`。
- `internal/platform/httpserver/pipeline.go` 末尾注册；`pkg/tracing/route_test.go` 同时锁定正例
  （名字为 `GET /admin/v1/audit/events`）与反例（不加中间件时确为 `GET unknown`），
  避免以后有人「顺手删掉这个看起来没用的中间件」。

## 4. 已知噪声（未处理，留作后续）

指标采集路径（`async` / `iam` / `identity` 的 Outbox GaugeFunc）会用
`context.Background()` 查库，otelsql 因此为每次采集生成**根 span**（无父节点）。
表现是 Jaeger 的 trace 列表被大量 `sql.*` 根 trace 占据，按 service 检索时很容易
看不到请求 trace；`check-trace-chain.py` 因此默认按 `operation=GET <route>` 检索。

可选收敛手段（需评估，不在本轮范围）：给 `openSQL` 加 `otelsql.WithSpanFilter`，
跳过无有效父 span 的查询；或把这些采集查询挪到独立的、不带 tracing 的连接池。

## 5. 未覆盖

- **外部 HTTP 依赖 span**：`pkg/httpclient` 已有出站埋点，但本次验证的
  `/admin/v1/audit/events` 不调用外部服务，链路中未出现 `http.client` span。
  如需覆盖，应在 staging 触发一次调用外部依赖的接口（如短信 / OAuth）。
- **worker 进程**：worker 未初始化 TracerProvider，SQL 不采集 span（本次刻意保持）。
