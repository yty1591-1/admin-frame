# 看板与可观测性验证（ENT-06 AC-ENT-06-3）

看板 JSON 入库后从未在真集群验证过：面板能打开，但里面的 PromQL 可能引用了不存在的指标、
写错的标签，或者对应指标根本没被写入过——运维看到的是**空面板**，而不是「这个指标不存在」。

本文记录验证方法、2026-11-03 的实测结果，以及本轮发现并修掉的 3 个缺陷。

## 1. 验证方法

```bash
# 环境：kind + 单体 Prometheus + 单体 Grafana + admin-frame（values-kind.yaml）
bash scripts/observability/install-kind-prometheus.sh
bash scripts/observability/install-kind-grafana.sh

# 造流量（看板依赖这些指标才有数据）
#   RED：http_server_requests_total
#   IAM：鉴权决策 / 快照缓存 / 角色变更
#   Async：Outbox 投递结果
#   DB：连接池（任意请求都会经过）

# 逐个面板查询真实 Prometheus
python3 scripts/observability/check-dashboards.py
```

`check-dashboards.py` 把每张看板的每个面板查询丢给真实 Prometheus，判定三档：

| 判定 | 含义 | 处理 |
|---|---|---|
| `ok` | 查询有数据点 | 通过 |
| `empty` | 查询合法但无数据点 | 失败（除非面板声明 `expect-empty` 标签） |
| `error` | 查询本身报错（PromQL 非法 / 指标不存在） | 失败 |

`expect-empty` 标签用于「健康态本就无数据」的面板（失败计数器、未启用域的面板），
让「空面板」与「预期为空」可区分——否则健康系统会一直报红。

退出码：`0` 全通过；`1` 有查询报错；`2` 有非预期空面板。

## 2. 静态门禁（CI 可跑，不需要集群）

```bash
python3 scripts/ci/dashboard_metrics_check.py   # 已并入 scripts/ci/check-observability.sh
```

校验看板引用的每个指标名在 Go 代码或告警规则里有定义，挡住「指标改名 → 看板变空」
的漂移。直方图按 Prometheus 惯例允许 `<name>_bucket` / `_sum` / `_count`。

## 3. 2026-11-03 实测结果

环境：kind `af-verify`（单节点）、单体 Prometheus v3.1.0（抓 4 个应用 Pod）、
单体 Grafana 11.6.0（provisioning 导入仓库里的 5 张看板 + Prometheus 数据源）。

```
汇总：共 37 个面板查询，错误 0，空数据 0，预期空（expect-empty 标签）16
```

- Grafana 数据源健康检查：`status=OK`（"Successfully queried the Prometheus API"）
- 5 张看板全部加载：`af-red` / `af-db` / `af-iam` / `af-async` / `af-identity`
- 三端 RED、IAM 授权结果、Outbox / worker 指标均有数据（AC-ENT-06-3 达成）

## 4. 本轮发现并修掉的缺陷

### 4.1 P1：IAM 快照缓存的观测器没接线，缓存面板恒为空

`cmd/internal/iamruntime` 构造 `iampcache.Config` 时没有设置 `Observer`，
缓存退化为 `NopObserver`：`iam_authz_cache_total` 从未被写入，
看板「快照缓存命中（layer/result）」面板永远没有数据。

修复：构造缓存时注入 `iamMetrics`（缓存装配移到指标创建之后）。

### 4.2 P1：shadow 模式不记录鉴权决策，决策面板恒为空

`internal/security/authz.go` 的 shadow 分支只比较「IAM 判定 vs 静态表」并记录差异，
从不调用 `OnDecision`：`iam_authz_decisions_total` 恒为空，
看板「鉴权决策（allow/deny/unavailable）」面板永远没有数据——而这正是切换 enforce
之前最需要看的面板（只看「差异数」不足以判断切换风险）。

修复：shadow 分支同样记录 IAM 侧判定（allow / deny），并补单测
（`TestAuthorizerShadowRecordsDecision` / `TestAuthorizerShadowRecordsAllow`）。

### 4.3 P2：RED 看板的 5xx 比率在无错误时是空面板

原表达式 `sum(rate(...5xx)) / clamp_min(sum(rate(...)), 1e-9)` 在没有 5xx 时
分子没有序列，整个面板空白——而「0%」才是健康态该显示的样子。

修复：分子用 `or (0 * sum by (service) (rate(http_server_requests_total[5m])))` 兜底，
没有错误时按 service 显示 0%。

## 5. 尚未覆盖

- **AC-ENT-06-4（trace 追到 DB）**：应用只对 HTTP 入站 / 出站打了 span，
  `pkg/db` 直接用 `sql.Open`，**没有 SQL 层的 span**，因此「从入口追到 DB」目前做不到。
  需要先决策：引入 `otelsql` 依赖（需过 DEP 评审），还是自研 `database/sql` driver 包装
  （无新依赖，约 200 行）。确认后再补 Collector + 后端（Jaeger / Tempo）的链路验证。
- **身份域看板**：本轮 kind 环境未启用身份域（需要 `values-identity-enabled.yaml`
  与 identity 密钥），相关面板按「预期空」标注，未在启用身份域的环境复核。
