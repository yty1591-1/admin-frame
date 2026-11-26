# 全链路压测基线登记（PERF-06 / ENT-16）

> **状态**：生效中（2026-11-04）
> **定位**：登记**已实际执行**的全链路压测基线，并提供可复现命令；不定义新的合并门禁
> **效力**：与 [`BASELINES.md`](./BASELINES.md)（微基准）互补——微基准管「纯逻辑不回归」，本表管「整条链路在本机 / staging 的实际水位」
> **关联**：[`PERFORMANCE_STANDARDS.md`](../PERFORMANCE_STANDARDS.md) PERF-06、[`../identity-slo.md`](../identity-slo.md) §1、[`../../tests/load/`](../../tests/load/)、[`../../tests/e2e/load_baseline_test.go`](../../tests/e2e/load_baseline_test.go)
> **执行方式**：`make load-baseline`（本机）/ `k6 run tests/load/identity_login.js`（staging）+ `make check-load-baseline`（登记一致性门禁）
> **最后更新**：2026-11-10（STG-01 四档由 STG-EQ-01 等价环境读数通过，决议与残余风险见 §4.4）
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
| LB-STG-LOGIN | tests/load/identity_login.js | STG-01 | 50 rps × 10m | 登录 p95 ≤ 300ms、p99 ≤ 800ms | 65.4 / 229.9 / 691.0 ms | 通过 | tests/load/artifacts/stg-eq-login-50rps.json | 2026-11-10 |
| LB-STG-REFRESH | tests/load/identity_login.js | STG-01 | 50 rps × 10m | 刷新 p95 ≤ 500ms | 36.3 / 98.4 / 337.8 ms | 通过 | tests/load/artifacts/stg-eq-refresh-50rps.json | 2026-11-10 |
| LB-STG-MFA | tests/load/identity_login.js | STG-01 | 50 rps × 10m | MFA 校验 p95 ≤ 500ms | 106.2 / 475.0 / 766.8 ms | 通过 | tests/load/artifacts/stg-eq-mfa-50rps.json | 2026-11-10 |
| LB-DEV-SCIM-SYNC | tests/load/scim_sync.js | DEV-01 | 10k 用户 + 50 组 × 200 成员（8 VU 并发） | 总耗时 ≤ 30 min、错误率 < 0.1%（AC-P16-SCIM-1） | 33.6 / 45.8 / 68.6 ms（总耗时 120.4s） | 通过 | tests/load/artifacts/scim-sync-10k.json | 2026-11-10 |
| LB-STG-SCIM-SYNC | tests/load/scim_sync.js | STG-01 | 10k 用户 + 50 组 × 200 成员（8 VU 并发） | 总耗时 ≤ 30 min、错误率 < 0.1%（AC-P16-SCIM-1） | 42.3 / 65.3 / 114.7 ms（总耗时 151.7s） | 通过 | tests/load/artifacts/stg-eq-scim-sync-10k.json | 2026-11-10 |

> **`STG-01` 四行的读数来源（勿误读）**：`STG-01` 的实测列来自 **STG-EQ-01** 等价环境——
> `env=staging` 完整强制校验（RS256 密钥环、IAM enforce、撤销 fail_closed、审计 Outbox）、
> 2 × `admin-api` + Envoy round-robin、真实 MySQL 8 / Redis 7，2026-11-10 按 §4.1 逐档
> 10 分钟实跑，产物文件名带 `stg-eq-` 前缀即为此意。环境规格、偏离项 D1–D5 与演练缺陷清单
> 见 §4.3。
>
> **2026-11-10 决议**：认可等价环境读数作为 `STG-01` 通过依据。它判定的是
> 「生产配置口径下 SLO 是否达标」，**不构成** staging / 生产容量结论——容量仍按
> `identity-slo.md` §2 的连接 / 内存模型核算（§6 最后一条仍然有效）。决议、残余风险与后续
> 复核项见 §4.4。


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

### 3.4 SCIM 2.0 目录同步（LB-DEV-SCIM-SYNC）

被压路径：`POST /admin/scim/v2/Users` ×10k，随后 `POST /admin/scim/v2/Groups` ×50（每组 200 成员，
成员按 `externalId` 解析）。覆盖 SCIM Bearer 鉴权 → 主体加密索引 → 账号/组事务写入 → 成员批量写入。

| 项 | 值 |
|---|---|
| 依赖 | MySQL 8.0（`docker-compose.integration.yml`，loopback 3306）+ Redis 7（6379），全新库执行 29 个迁移 |
| 服务 | `admin-api` 真实二进制，`identity.enabled=true`、`identity.scim.enabled=true`（provider=okta）、`iam.mode=enforce` |
| 鉴权 | bootstrap 管理员首登改密 → 经 `POST /admin/v1/auth/scim/tokens` 签发 `scim_` 令牌（库内只存 SHA-256） |
| 限流 | 演练期把通道限流与 SCIM 单令牌限流都提到 5000 rps / burst 10000，避免用 429 污染吞吐读数 |
| 压测端 | k6 0.54.0（`grafana/k6` 容器），`host.docker.internal` 直连，8 VU |
| 数据量 | 从空库开始：10000 账号 + 50 组 + 10000 成员 + 10000 条 `scim:<provider>` 身份映射 |

| 指标 | 实测 |
|---|---|
| 样本 | 10050（10000 建号 + 50 建组） |
| 错误 / 成功率 | 0 / 100% |
| p50 / p95 / p99 / max | 33.6 / 45.8 / 68.6 / 1711.8 ms |
| 总耗时 | 120.4 s（上限 1800 s） |
| 吞吐 | 83.5 req/s（8 VU，单副本） |

判读：

1. **建号段约 45 s** 完成 10000 次创建（≈222 次/秒），瓶颈在 MySQL 单条事务写入，不在 Go 侧；
   同机 Docker 共享 CPU，绝对值低于独立 staging 机器，只能作为「同机前后对比」基线。
2. **组创建是长尾来源**：每组 200 成员需 200 次外部 ID 解析 + 200 条成员插入，
   单次最大 1.7 s 即出现在这里；50 组串行执行占总耗时的 60% 以上。
   若要压短总时长，应提高 `PER_GROUP` 之外的分片并发，而不是压缩建号段。
3. **幂等复跑同批次 10050 次请求全部返回 409 uniqueness**，脚本按「已存在」计为预期响应，
   `http_req_failed` 保持 0；账号 / 组 / 成员数量不变（AC-P16-SCIM-2 的 10k 规模复证）。

> **与 AC-P16-SCIM-1 的关系**：本条是**本机 staging 等价单副本**读数（DEV-01），
> 证明「功能链路在 10k 规模可完成且零错误」。生产配置口径（`env=staging` + 2 副本 + Envoy）
> 的同等演练见 §4.3（STG-EQ-01，151.7 s），并已被 2026-11-10 决议采纳为 `STG-01` 的通过依据
> （`LB-STG-SCIM-SYNC`，见 §4.4）；本机读数仍不得外推为 staging / 生产容量结论。

## 4. staging 口径（STG-01，以 STG-EQ-01 等价环境读数通过）

### 4.1 场景与通过标准

| 场景 | k6 变量 | 通过标准 | 来源 |
|---|---|---|---|
| 登录 | `SCENARIO=login` | `identity_login_duration` p95 ≤ 300 ms、p99 ≤ 800 ms；`http_req_failed < 2%` | `identity-slo.md` §1 |
| 刷新 | `SCENARIO=refresh` | `identity_refresh_duration` p95 ≤ 500 ms；轮换令牌重放必须 40100 | 同上 + SEC-04 |
| MFA | `SCENARIO=mfa` | `identity_mfa_duration` p95 ≤ 500 ms；错误码必须 40100 | 同上 |
| SCIM 目录同步 | `tests/load/scim_sync.js`（`USERS=10000 GROUPS=50`） | 总耗时 ≤ 30 min、`http_req_failed < 0.1%`（AC-P16-SCIM-1） | `identity-authn.md` §21.1 |

### 4.2 执行清单

1. 预置压测账号池：A 端 `mfa.admin_required=true` 时**每账号独立 TOTP**，账号池文件
   `ACCOUNTS_FILE`（每行 `subject,totp_secret`）交给 `identity_login.js`；单密钥 `TOTP_SECRET`
   仅适用于共用密钥的测试环境；
2. 仅压测环境临时放宽 `identity.login_rate_limit.*`、端点限流与 `identity.lockout.max_failures`
   （错误口令用例会持续累积失败计数），结束后立即恢复；
3. 每档 10 分钟，同时采集 k6 摘要与 Prometheus `identity_*` 直方图；
4. 每档结束检查：5xx = 0、`identity_token_reuse_total` 仅随重放用例增长、
   DB 连接池无等待、Redis RTT 正常；
5. 填回 §2 对应行（实测列、结论、日期），产物 JSON 落 `tests/load/artifacts/`。

命令见 [`tests/load/identity_login.js`](../../tests/load/identity_login.js) 头部注释。

### 4.3 STG-01 通过依据：STG-EQ-01 等价环境实测（2026-11-10）

**环境规格**（生产规格单机等价，`env=staging` 走完整强制校验）

| 项 | 值 |
|---|---|
| 拓扑 | 2 × `admin-api`（节点号 0/1，同机不同端口）+ Envoy round-robin 入口（模拟 staging Ingress → Service） |
| 配置口径 | `env=staging`：RS256 + 密钥环（`kid=stg-1`，公/私钥文件挂载）、IAM `enforce` + Redis 快照、撤销 `fail_closed`、审计 Outbox（`publish_in_api=false`）、幂等与限流走 Redis、MFA `admin_required=true` |
| 数据库 | MySQL 8.0（container，loopback 3306），独立库 `admin_frame_stg`，29 个迁移 |
| 缓存 | Redis 7（container，loopback 6379，`db=14`，键前缀 `admin-frame/stg-eq/`） |
| 压测端 | k6 0.54.0（`grafana/k6`，`--network host`），`ramping-arrival-rate` 1m → 50 rps → 10m → 1m |
| 账号池 | 2000 个账号，SCIM 建号 + 每账号独立 TOTP；口令由夹具按生产 Argon2id 编码注入（偏离项 D3） |

| 场景 | 样本 | 错误 / 成功 | p50 / p95 / p99 / max | 结论 | 产物 |
|---|---|---|---|---|---|
| 登录 50 rps × 10m | 33149 | 0 / 100% | 65.4 / 229.9 / 691.0 / 2049.1 ms | 通过（p95 ≤ 300、p99 ≤ 800） | `stg-eq-login-50rps.json` |
| 刷新 50 rps × 10m | 23742 | 0 / 100% | 36.3 / 98.4 / 337.8 / 1058.5 ms | 通过（p95 ≤ 500） | `stg-eq-refresh-50rps.json` |
| MFA 50 rps × 10m | 33144 | 0 / 100% | 106.2 / 475.0 / 766.8 / 2122.0 ms | 通过（p95 ≤ 500） | `stg-eq-mfa-50rps.json` |
| SCIM 10k 用户 + 50 组 × 200 成员 | 10050 | 0 / 100% | 42.3 / 65.3 / 114.7 / 2689.5 ms | 通过（151.7 s ≤ 1800 s） | `stg-eq-scim-sync-10k.json` |

配套证据：

- 两个副本的 `http_server_requests_total` **无任何 5xx 分桶**（只有 200/201/204/400/401/404/409）；
  `identity_revocation_store_errors_total{op=token_load|account_load|session_load}` 全 0；
- 负载在副本间分布均匀（登录直方图计数 57540 / 59839，差 4%），说明 Envoy 轮询与会话无关状态
  （撤销快照、幂等、限流）都落在共享 Redis 上；
- SCIM 幂等复跑：同批次 10050 次创建全部 409（`identity_scim_request_total{reason="409"}` 精确
  +10050），21.5 s 完成、0 错误，账号 / 组 / 成员计数不变（AC-P16-SCIM-2 的 10k 规模复证）；
- 落库核对：12001 账号（2000 池 + 权威管理员 + 10000 SCIM）、50 组、10000 成员、
  12000 条 `scim:<provider>` 身份映射。

判读：

1. **登录 p95 229.9 ms 的构成**：1 次 Argon2id（19 MiB / 2 迭代，约 40–60 ms）+ 约 10 次
   DB/Redis 往返 + 本机 12 vCPU 上「2 副本 + MySQL + k6 + Envoy + 压测端」争抢。跨机绝对值不可
   与本机比较，但**同一环境的余量**（p95 距 300 ms 还差 23%）可用于回归判断。
2. **MFA 是四档里最贴线的一档**（p95 475 ms / 目标 500 ms）：每次校验都要解 TOTP 密文 →
   验证 → `last_used_step` CAS 写回，且在 50 rps 下与登录共用同一个 Argon2 并发池。若后续要在
   同一硬件上提高 MFA 到达率，应先扩副本或提高 `identity.password.max_concurrent`，而不是放宽阈值。
3. **刷新 p95 98.4 ms 明显低于登录**：刷新不碰 Argon2，只做一次性轮换（Redis + 一次 SQL 更新），
   印证 §2.1 的「登录成本主要是口令哈希」判断。
4. **SCIM 段比 DEV-01 慢 26%（151.7 s vs 120.4 s）**：本环境多了 RS256 验签、Redis 撤销读、
   IAM 权限快照与 Envoy 一跳；仍远低于 30 min 上限，说明目录同步的瓶颈依旧是 MySQL 单条事务，
   不是鉴权链。

**偏离项（相对 STG-01 生产规格，逐条列明以便复核）**

| 编号 | 偏离 | 原因 | 影响 |
|---|---|---|---|
| D1 | 通道限流 5000 rps / burst 10000 | 50 rps × 多端点会先被 200 rps 打 429 | 只影响 429 是否出现，不影响时延分布 |
| D2 | `identity.lockout.max_failures=10000` | 错误口令用例 10 分钟产生约 6000 次失败 | 锁定属安全控制，不参与时延判定 |
| D3 | 压测账号口令由夹具写入 `identity_credential` | SCIM 建号按设计为纯 SSO（无本地口令），管理员重置口令返回 404 `password credential not found` | 登录链路本身仍走真实校验；账号/映射由 SCIM 生产路径创建 |
| D4 | 单机 + loopback 依赖，未接对象存储 / OTel Collector | 无独立 staging 集群窗口 | 与 SLO 无关（本次链路不触达 storage/tracing 导出） |
| D5 | Envoy 一跳经 Docker 网关（`192.168.65.254`）回环到宿主两个副本 | 单机模拟 Ingress | 增加约 1 ms 固定成本，已在 p50 中体现 |

**演练暴露的真实缺陷（已修）**

1. `identity_login.js` 用 `process.env` 读环境变量——k6 里 `process` 未定义，脚本**从未能启动**；
   改为 `__ENV`（与 `scim_sync.js` 一致）。这正是「登记了执行器但没真正跑过」的典型信号。
2. TOTP 计算把 JS 数组直接传给 `crypto.hmac`，k6 抛 `invalid type []interface{}`；
   改为 `ArrayBuffer` 后 50% 成功用例才真正通过。
3. `http_req_failed` 把「错误口令 / 不存在账号 / 错误动态码 / 重放」这 30% 的预期 40100 计为失败，
   阈值 `< 2%` 恒不可达；改用 `http.expectedStatuses(200, 401)`。
4. 刷新场景原先每次 refresh 都重新登录 + 校验 MFA（3 倍请求量），首轮 p95 1322 ms「不达标」——
   是**测量方法错误而不是服务端劣化**；改为按 VU 分片维持轮换中的会话后 p95 98.4 ms。
5. 同一账号在同一 30 s 时间步内被并发校验会被服务端按 TOTP 重放拒绝（`last_used_step` 单调递增，
   属正确行为）；脚本把这类响应单列为 `skipped`，不计入 `errors`。刷新档 skipped 11526、
   MFA 档 2692，均为该原因 + 重放用例整链撤销后的重新登录等待。

**产品侧已知约束（非缺陷，记录以便运维）**：SCIM 账号是纯 SSO 身份，没有本地口令凭据，
`POST /admin/v1/auth/accounts/{id}/password/reset` 对其返回 404。若 IdP 不可用，这类账号无法
break-glass 登录；需要本地兜底时应由管理员改用非 SCIM 账号，或在后续版本增加「为 SSO 账号
补设本地口令」的特权操作。

### 4.4 决议记录、残余风险与后续复核

**决议（2026-11-10，项目负责人确认）**：STG-01 的登录 / 刷新 / MFA / SCIM 四档，
采用 STG-EQ-01 等价环境读数判定为**通过**，不再等待独立 staging 集群窗口。

- **决议范围**：仅限「生产配置口径下 SLO 是否达标」这一判定（`identity-slo.md` §1 的时延与成功率）。
- **不在范围内**：容量结论、容量规划、发布前性能回归对比——这三类仍要求独立环境，
  且必须走 §5 的「同环境同到达率」口径与 `identity-slo.md` §2 的连接 / 内存模型核算。
- **证据**：产物文件即 `stg-eq-*.json`（含 samples / errors / success_rate / p50–p99），
  文件名保留 `stg-eq-` 前缀，便于任何时候回溯「数字从哪台机器来」。

**残余风险（已识别，接受但不隐瞒）**

| 风险 | 来源 | 影响面 | 后续动作 |
|---|---|---|---|
| 同机 CPU 争抢导致尾部偏高 | 应用 / MySQL / k6 / Envoy 共用 12 vCPU | p95 绝对值偏保守（真实 staging 只会更好），不会掩盖劣化 | 真实集群首跑时做一次同口径对照 |
| 依赖未做网络隔离 | loopback 访问 MySQL/Redis | 少了跨机 RTT 与网络抖动 | 同上 |
| 未接对象存储 / OTel 导出 | 单机无 MinIO / Collector | 与本次链路无关（SLO 不触达） | 依赖接入后补一次冒烟档 |
| 限流与锁定为演练取值（D1/D2） | 50 rps 会先被 200 rps 打 429 | 只影响 429 是否出现 | 恢复生产取值后跑一次确认档，证明限流不把 SLO 打穿 |
| 账号池 2000、TOTP 重放导致 skipped | 同账号同 30 s 时间步并发校验 | 样本量略低于请求量（刷新档 23742/35268） | 真实 IdP 侧账号池更大时可自然降低 |

**后续复核项（不阻断本次通过）**

1. 真实 staging 集群（StatefulSet + Ingress + MinIO + Collector）首跑同四档，与本表做量级对照；
2. 限流 / 锁定恢复生产值后的确认档（`E2E_LOAD_STRICT` 口径，不达标即失败）；
3. 依赖 MySQL / Redis / Go 大版本升级时，按 §7 重跑 50 rps 档并刷新本节读数。

### 4.5 复现命令

```bash
# 1) 独立库 + 迁移
docker exec -i admin-frame-integration-mysql-1 mysql -uroot -proot \
  -e "CREATE DATABASE IF NOT EXISTS admin_frame_stg CHARACTER SET utf8mb4"
./bin/migrate -driver mysql -dir migrations -action up \
  -dsn 'app:app@tcp(127.0.0.1:3306)/admin_frame_stg?parseTime=true&loc=UTC&charset=utf8mb4'

# 2) RS256 密钥环（挂载文件，不入库）
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out tmp/stg-eq/jwt-private.pem
openssl rsa -pubout -in tmp/stg-eq/jwt-private.pem -out tmp/stg-eq/jwt-public.pem

# 3) 两副本 + Envoy 入口（配置见 configs/dev/staging-drill-{0,1}.yaml，均不入库）
ADMIN_FRAME_ID_NODE_ID=0 ./bin/admin-api -config configs/dev/staging-drill-0.yaml &
ADMIN_FRAME_ID_NODE_ID=1 ./bin/admin-api -config configs/dev/staging-drill-1.yaml &
docker run -d --name af-stg-envoy --add-host host.docker.internal:host-gateway \
  -p 8080:8080 -v "$PWD/tests/load/staging/envoy.yaml:/etc/envoy/envoy.yaml:ro" \
  envoyproxy/envoy:v1.36.7 -c /etc/envoy/envoy.yaml

# 4) 账号池：SCIM 建号 → 夹具注入 Argon2id 口令 → 逐账号绑定 TOTP → 落账号池文件
python tests/load/staging/provision.py admin
python tests/load/staging/provision.py accounts 2000
python tests/load/staging/provision.py csv        # → tmp/stg-eq/accounts.csv

# 5) 四档压测（每档 12 分钟含爬坡；SCIM 约 2.5 分钟）
k6 run tests/load/identity_login.js -e BASE_URL=http://127.0.0.1:8080 \
  -e ACCOUNTS_FILE=/data/accounts.csv -e PASSWORD="$PASSWORD" -e SCENARIO=login \
  -e RATE=50 -e DURATION=10m -e ARTIFACT=/scripts/artifacts/stg-eq-login-50rps.json
k6 run tests/load/scim_sync.js -e BASE_URL=http://127.0.0.1:8080 -e SCIM_TOKEN=scim_...
```

演练夹具随仓库分发：[`tests/load/staging/provision.py`](../../tests/load/staging/provision.py)（账号池与
管理员 MFA 预置）、[`staging/seedpw`](../../tests/load/staging/seedpw/main.go)（生产同款 Argon2id
编码注入）、[`staging/envoy.yaml`](../../tests/load/staging/envoy.yaml)（两副本轮询入口）。
令牌与 TOTP 密钥只写 `tmp/stg-eq/state.json`（`tmp*` 已被 `.gitignore` 忽略），不入库。


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
- 用本机读数直接推导生产容量（生产按 `identity-slo.md` §2 的连接 / 内存模型核算）；
  注意区分：§4.4 的决议只允许等价环境读数用于 **SLO 达标判定**，容量结论永远需要独立环境。

## 7. 维护

- 新增场景：先落执行器（Go runner 或 k6 脚本）→ 跑出真实产物 → 再登记行；
- 依赖升级（MySQL / Redis / Go 大版本）后重跑 50 与 200 两档，刷新 §3.3 表；
- 容量相关结论移交 `ENT-22`（容量与成本治理）时，以本表产物为输入，不重新发明口径。
