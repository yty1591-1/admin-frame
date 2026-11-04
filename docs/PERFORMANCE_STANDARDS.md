# 工程化性能与容量规范（硬约束）

> **状态**：生效中（Normative）
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：已登记的关键路径微基准、列表背压与容量相关合入门槛
> **关联**：架构 §11.3 / §11.4；可靠性见 `RELIABILITY_STANDARDS.md`；分页见 `API_STANDARDS.md`
> **执行方式**：[`docs/perf/BASELINES.md`](./perf/BASELINES.md) + [`docs/perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md) + `check-perf.sh` / `check-load-baseline.sh` + Review
> **最后更新**：2026-11-04
> **变更方式**：修改本规范须同步更新 BASELINES、相关 Benchmark 与 `scripts/ci/check-perf.sh`，经架构 Owner Review

> **说明**：本专文强制**轻量、可复现**的微基准与登记制，**不**在默认 CI 跑全链路压测。全链路压测属发版/容量规划可选门禁。

---

## 1. 目标

1. **关键路径有基准**：登记在册的热点须有 `Benchmark*` 且 CI 可跑通。
2. **防回归可发现**：登记项变更须说明对延迟/分配的影响。
3. **背压默认开启**：列表分页上限等容量手段不可拆除。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| PERF-01 | 维护 [`docs/perf/BASELINES.md`](./perf/BASELINES.md) 关键路径登记表 | `check-perf.sh` |
| PERF-02 | 表中每一行必须存在对应 `BenchmarkXxx` 且 `go test -bench` 通过 | `check-perf.sh` |
| PERF-03 | 新增/重写已登记热点逻辑的 MR 须更新 BASELINES 说明（或证明无回归动机） | Review + PR 模板 |
| PERF-04 | 列表 `page_size` 上限（API-05 / REL-07）不得以提高性能为由移除 | OpenAPI + 单测 |
| PERF-05 | 禁止在无超时的热路径上引入无界重试或无界扇出（对齐 REL） | Review |
| PERF-06 | 全链路压测**不**作为默认合并门禁；已执行的基线统一登记在 [`perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md)，登记一致性由 `check-load-baseline.sh` 校验（不重跑压测） | `check-load-baseline` |
| PERF-07 | 合并请求触及已登记热点时须勾选自检 | PR 模板 |

---

## 3. 关键路径登记

`docs/perf/BASELINES.md` 列表示例字段：

| 路径 ID | 包 | Benchmark 名 | 说明 | 备注 |
|---------|----|--------------|------|------|
| `iam.authorize.CacheHit` | `internal/iam/app` | `BenchmarkAuthorize_CacheHit` | L1 命中：解码 + 清洗 + 快照复用 | 微基准 |

- 仅登记**稳定、CPU/分配敏感**的纯逻辑或本地路径；IO 密集路径用集成压测，不塞进默认 CI。
- 全链路（真实二进制 × MySQL × Redis）的到达率 / 分位 / 成功率读数登记在 [`perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md)，由 `tests/load` 与 `tests/e2e` 的执行器产出；本表不承接链路级数据。
- 删除登记项须 Owner 批准并说明替代观测手段。

---

## 4. 微基准约定

- 命名：`Benchmark` + 与业务一致的标识。
- CI：`check-perf.sh` 使用短 `-benchtime`（默认 `50ms`），验证**可运行**而非卡死精确 ns/op。
- 本地深度对比：

```bash
go test -bench=BenchmarkAuthorize_CacheHit -benchmem ./internal/iam/app/
```

- 若引入正式回归阈值（如 ns/op 上涨 >20% 阻断），须 ADR 并更新本专文与脚本；默认关闭以免环境噪声误杀。

---

## 5. 容量与背压（合入层）

| 手段 | 要求 |
|------|------|
| 分页 | `page_size` ≤ 100 |
| 超时 | 外部调用带 deadline（REL-01） |
| 连接池 / 并发 | 可配置；禁止硬编码无限 |

生产 SLO 阈值见 runbook / 观测系统，不在本专文写死数值。

---

## 6. MUST NOT

- 登记表有行但仓库无对应 Benchmark。
- 为「过性能测试」在 CI 关闭 race 或去掉分页上限。
- 把未稳定的压测脚本设为默认 PR 必红项而无环境隔离。

---

## 7. 本地与 CI

```bash
make check-perf    # PERF-01/02
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-perf.sh` |
| MR | PR 模板「性能自检」 |

---

## 8. 例外

1. **环境过慢导致 bench 超时**：可提高 `PERF_BENCHTIME`（如 `100ms`），禁止跳过登记项。
2. **平台不支持 bench**：无。Go 工具链必须可用（与 test job 相同）。
3. **大型压测**：放在夜间/发版流水线，结果链到发布单（RELS Checklist 可选勾选）。

---

## 9. Review 清单

- [ ] 触及热点已更新 BASELINES 或说明无影响
- [ ] `make check-perf` 通过
- [ ] 未削弱分页/超时等背压

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `docs/perf/BASELINES.md` | PERF-01 |
| `internal/**/_*_test.go` 中 Benchmark | PERF-02 |
| `scripts/ci/check-perf.sh` | 登记与 bench 抽检 |
| 本文件 | 规范正文 |
