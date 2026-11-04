# 全链路压测基线登记（PERF-06 / ENT-16）

> **状态**：生效中（2026-11-04）
> **定位**：登记**已实际执行**的全链路压测基线，并提供可复现命令；不定义新的合并门禁
> **效力**：与 [`BASELINES.md`](./BASELINES.md)（微基准）互补——微基准管「纯逻辑不回归」，本表管「整条链路在本机 / staging 的实际水位」
> **关联**：[`PERFORMANCE_STANDARDS.md`](../PERFORMANCE_STANDARDS.md) PERF-06、[`../identity-slo.md`](../identity-slo.md) §1、[`../../tests/load/`](../../tests/load/)、[`../../tests/e2e/load_baseline_test.go`](../../tests/e2e/load_baseline_test.go)
> **执行方式**：`make load-baseline`（本机）/ `k6 run tests/load/identity_login.js`（staging）+ `make check-load-baseline`（登记一致性门禁）
> **最后更新**：2026-11-04
> **变更方式**：新增 / 修改登记行须附产物 JSON 与执行环境；删除登记行须说明替代观测手段

---

## 1. 定位与边界

| 维度 | 本表（链路压测） | [`BASELINES.md`](./BASELINES.md)（微基准） |
|---|---|---|
| 观测对象 | 真实二进制 × MySQL × Redis 的完整请求链 | 单个函数 / 纯逻辑路径 |
| 判定量 | 到达率、p50/p95/p99、成功率、错误数 | ns/op、B/op |
| 运行时机 | 手动 / nightly（**不进** `make ci`） | 每次 MR（`check-perf`） |
| 用途 | 容量规划、发布前水位对比、拐点定位 | 防逻辑层性能回归 |

**PERF-06 铁律**：全链路压测不作为默认合并门禁。因此：

- 本表与压测产物**不参与** PR 必跑流程；`check-load-baseline` 只校验「登记是否自洽」，不重新跑压测；
- 只有 `E2E_LOAD_STRICT=1` 或 staging 执行时才把时延目标升级为失败判据；
- 任何以「压测通过」为由关闭 race、去掉分页上限、放宽超时的改动一律拒绝（PERF-04 / PERFORMANCE_STANDARDS §6）。

## 2. 登记表

列语义：

- **场景 ID**：`LB-<范围>-<到达率>`，全局唯一，门禁按此对账；
- **执行器**：`文件::函数`（Go runner）或 `文件`（k6 脚本），必须真实存在；
- **环境**：`DEV-01` 等环境代号，规格见 §3 / §4；
- **目标**：判定阈值，必须与被压接口的 SLO 同源（读路径见 §3.3，身份域见 `identity-slo.md` §1）；
- **结论**：`通过` / `待执行`；`通过` 行必须附产物 JSON，`待执行` 行产物列填 `-`。

| 场景 ID | 执行器 | 环境 | 到达率 × 时长 | 目标 | 实测（p50 / p95 / p99） | 结论 | 产物 | 日期 |
|---|---|---|---|---|---|---|---|---|
| LB-LOCAL-050 | tests/e2e/load_baseline_test.go::TestLoadBaselineAdminRead | DEV-01 | 50 rps × 20s | p95 ≤ 100ms、p99 ≤ 300ms、错误 0 | 3.17 / 6.37 / 8.78 ms | 通过 | tests/load/artifacts/local-admin-read-50rps.json | 2026-11-04 |
| LB-LOCAL-100 | tests/e2e/load_baseline_test.go::TestLoadBaselineAdminRead | DEV-01 | 100 rps × 20s | p95 ≤ 100ms、p99 ≤ 300ms、错误 0 | 3.27 / 6.26 / 8.99 ms | 通过 | tests/load/artifacts/local-admin-read-100rps.json | 2026-11-04 |
| LB-LOCAL-200 | tests/e2e/load_baseline_test.go::TestLoadBaselineAdminRead | DEV-01 | 200 rps × 20s | p95 ≤ 100ms、p99 ≤ 300ms、错误 0 | 2.95 / 6.03 / 8.94 ms | 通过 | tests/load/artifacts/local-admin-read-200rps.json | 2026-11-04 |
| LB-LOCAL-500 | tests/e2e/load_baseline_test.go::TestLoadBaselineAdminRead | DEV-01 | 500 rps × 15s | p95 ≤ 100ms、p99 ≤ 300ms、错误 0 | 3.53 / 11.14 / 44.56 ms | 通过 | tests/load/artifacts/local-admin-read-500rps.json | 2026-11-04 |
| LB-STG-LOGIN | tests/load/identity_login.js | STG-01 | 50 rps × 10m | 登录 p95 ≤ 300ms、p99 ≤ 800ms | — | 待执行 | - | — |
| LB-STG-REFRESH | tests/load/identity_login.js | STG-01 | 50 rps × 10m | 刷新 p95 ≤ 500ms | — | 待执行 | - | — |
| LB-STG-MFA | tests/load/identity_login.js | STG-01 | 50 rps × 10m | MFA p95 ≤ 500ms | — | 待执行 | - | — |

> **staging 三行为什么是「待执行」**：k6 场景需要 staging 环境窗口、压测账号（含 TOTP 绑定）
> 与临时放宽的登录限流，属于运维窗口动作，不是代码交付项。登记为 `待执行` 而不是填入
> 估算值——本表的任何数字都必须来自真实产物，禁止外推。

## 3. 本机基线（DEV-01）

### 3.1 环境规格

| 项 | 值 |
|---|---|
| 主机 | Windows 11，12 vCPU，本机 SSD |
| Go | go1.26.10（`runtime.NumCPU()` = 12） |
| 数据库 | MySQL 8（docker `admin-mysql`，loopback `127.0.0.1:3306`） |
| 缓存 | Redis 7（docker `admin-redis`，loopback `127.0.0.1:6379`，db 15） |
| 服务 | `admin-api` 真实二进制（`go build` 产物，`-config` 加载 YAML） |
| 压测端 | 同机 Go 客户端，keep-alive 连接池 = `2 × 到达率`，不走代理 |
| 数据量 | 迁移后的空租户表 + 引导管理员会话（读路径为分页 20 条） |

> 同机压测的读数是**相对基线**：只用于「同一台机器、同一套依赖」的前后对比，
> 不得与 staging / 生产绝对值比较；跨机器比较请以 p95 量级（十倍差以内）定性判断。

### 3.2 被压路径

`GET /admin/v1/tenants?page_size=20`，覆盖链路的绝大部分固定成本：

```
HTTP 中间件链（trace / metrics / body limit / timeout）
  → JWT 校验（签名 + 过期 + channel 断言）
  → IAM 权限快照（内存 L1 命中）→ RequirePermission
  → 数据范围过滤（authz ScopeWhere）→ 仓储 SQL（MySQL）
  → 信封序列化 + 分页元数据
```

**为什么不压登录**：密码登录的 Argon2 成本使结论随 CPU 核数剧烈波动（同 QPS 下
机器差一倍、p99 差数倍），不适合作为「同机可比」的基线；登录 / 刷新 / MFA 的 SLO
由 staging 的 k6 场景负责（§4）。

### 3.3 判读

| 到达率 | 样本 | 成功率 | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 50 rps | 1000 | 100% | 3.17 ms | 6.37 ms | 8.78 ms | 11.5 ms |
| 100 rps | 1995 | 100% | 3.27 ms | 6.26 ms | 8.99 ms | 15.8 ms |
| 200 rps | 4000 | 100% | 2.95 ms | 6.03 ms | 8.94 ms | 27.3 ms |
| 500 rps | 7476 | 100% | 3.53 ms | 11.14 ms | 44.56 ms | 164.0 ms |

结论：

1. **50～200 rps 区间近似平权**：p95 稳定在 6 ms 量级，说明该区间内没有排队，
   单副本余量充足；这也验证了鉴权链路（快照缓存 + 数据范围谓词）没有隐藏的锁竞争。
2. **拐点在 200→500 rps 之间**：500 rps 时 p95 从 6 ms 升至 11 ms、p99 升至 44 ms，
   max 出现 164 ms 的离群值；形态是「尾部拉长而非整体抬升」，指向调度抖动
   （12 vCPU 上客户端与服务端争抢）而不是服务端队列积压。
3. **应用层不是当前瓶颈**：读路径在 500 rps 仍满足目标。容量规划应按
   「DB 连接预算 + 副本数 + Redis 单 jti 校验」推进（`identity-slo.md` §2），
   而不是继续优化 Go 侧。
4. **读路径目标（p95 ≤ 100 ms / p99 ≤ 300 ms）的取值依据**：`API_STANDARDS.md`
   同步读接口预算，叠加上表实测 3 倍以上余量；它是「明显劣化」的判据，不是容量上限。

## 4. staging 基线（STG-01，待执行）

### 4.1 场景与通过标准

| 场景 | k6 变量 | 通过标准 | 来源 |
|---|---|---|---|
| 登录 | `SCENARIO=login` | `identity_login_duration` p95 ≤ 300 ms、p99 ≤ 800 ms；`http_req_failed < 2%` | `identity-slo.md` §1 |
| 刷新 | `SCENARIO=refresh` | `identity_refresh_duration` p95 ≤ 500 ms；轮换令牌重放必须 40100 | 同上 + SEC-04 |
| MFA | `SCENARIO=mfa` | `identity_mfa_duration` p95 ≤ 500 ms；错误码必须 40100 | 同上 |

### 4.2 执行清单

1. 预置压测账号（`LOGIN_SUBJECTS`），MFA 场景须已绑定 TOTP（`TOTP_SECRET`）；
2. 仅压测环境临时放宽 `identity.login_rate_limit.*` 与端点限流，结束后立即恢复；
3. 每档 10 分钟，同时采集 k6 摘要与 Prometheus `identity_*` 直方图；
4. 每档结束检查：5xx = 0、`identity_token_reuse_total` 仅随重放用例增长、
   DB 连接池无等待、Redis RTT 正常；
5. 填回 §2 对应行（实测列、结论、日期），产物 JSON 落 `tests/load/artifacts/`。

命令见 [`tests/load/identity_login.js`](../../tests/load/identity_login.js) 头部注释。

## 5. 复现与判定

```bash
# 本机基线（默认 50 rps × 30s，预热 5s）
E2E_MYSQL_DSN='<user>:<pass>@tcp(127.0.0.1:3306)/<db>?parseTime=true&loc=UTC' \
E2E_REDIS_ADDR='127.0.0.1:6379' \
make load-baseline

# 指定到达率 / 时长 / 产物路径，并把时延目标升级为失败判据
E2E_LOAD_RATE=200 E2E_LOAD_DURATION=60s E2E_LOAD_STRICT=1 \
E2E_LOAD_ARTIFACT=tests/load/artifacts/local-admin-read-200rps.json \
make load-baseline
```

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `E2E_LOAD_BASELINE` | — | `1` 启用（`make load-baseline` 已注入），否则用例跳过 |
| `E2E_LOAD_RATE` | `50` | 目标到达率（rps） |
| `E2E_LOAD_DURATION` | `30s` | 采样时长（不含预热） |
| `E2E_LOAD_WARMUP` | `5s` | 预热时长，样本丢弃 |
| `E2E_LOAD_ARTIFACT` | 空 | 产物 JSON 路径；空则只写测试日志 |
| `E2E_LOAD_STRICT` | 空 | `1` 时时延不达标即失败（staging / 发布前对比用） |

**回归判定口径**：

- 比较**同环境、同到达率、同数据量**的两次产物；跨环境只做量级比较；
- p95 恶化 > 30% 视为可疑，须在 MR 说明原因或定位到具体提交；
- p99 / max 受调度与 GC 影响大，单次离群不作为结论，连续两次同向恶化才升级；
- 成功率 < 99% 或出现任意 5xx，直接判定失败并保留产物排障。

## 6. MUST NOT

- 登记表写数字但仓库无对应产物 JSON（`check-load-baseline` 拦截）；
- 把压测塞进 `make ci` / PR 必跑流程（PERF-06）；
- 用「未跑过的估算值」填实测列；
- 为提高到达率关闭超时、放宽限流后忘记恢复，或改压测脚本阈值使其「自证通过」；
- 用本机读数直接推导生产容量（生产按 `identity-slo.md` §2 的连接 / 内存模型核算）。

## 7. 维护

- 新增场景：先落执行器（Go runner 或 k6 脚本）→ 跑出真实产物 → 再登记行；
- 依赖升级（MySQL / Redis / Go 大版本）后重跑 50 与 200 两档，刷新 §3.3 表；
- 容量相关结论移交 `ENT-22`（容量与成本治理）时，以本表产物为输入，不重新发明口径。
