# 压测执行器（ENT-16）

本目录只放**执行器**，基线数值与判定口径统一登记在
[`docs/perf/LOAD_BASELINES.md`](../../docs/perf/LOAD_BASELINES.md)。登记与产物的一致性
由 `make check-load-baseline` 校验（已入 `make ci`），压测本身**不进**合并门禁（PERF-06）。

| 执行器 | 目标环境 | 压什么 | 运行方式 |
|---|---|---|---|
| [`identity_login.js`](./identity_login.js) | staging（生产同规格） | 登录 / 刷新 / MFA 的时延 SLO（`docs/identity-slo.md` §1） | `k6 run tests/load/identity_login.js -e BASE_URL=... -e ACCOUNTS_FILE=/data/accounts.csv -e PASSWORD=...`（或 `LOGIN_SUBJECTS` + `TOTP_SECRET`）；`-e SCENARIO=login\|refresh\|mfa`、`-e ARTIFACT=tests/load/artifacts/<name>.json` |
| [`../e2e/load_baseline_test.go`](../e2e/load_baseline_test.go) | 本机（真实二进制 × MySQL × Redis） | 已认证读路径（鉴权 + 权限快照 + SQL + 序列化） | `make load-baseline` |
| [`scim_sync.js`](./scim_sync.js) | staging（生产同规格） | SCIM 2.0 目录同步吞吐与错误率（AC-P16-SCIM-1：10k 用户 + 50 组 ≤ 30 min） | `k6 run tests/load/scim_sync.js -e BASE_URL=... -e SCIM_TOKEN=...`；`-e USERS=10000 -e GROUPS=50 -e PER_GROUP=200` 可调，`-e ARTIFACT=tests/load/artifacts/scim-sync-10k.json` 让脚本按登记表结构落产物（不传则只打印摘要） |

## 登录类脚本的三条硬约束（生产规格 A 端）

`mfa.admin_required=true` 的 A 端不是「一个密码就能登录」的环境，脚本必须按下列口径运行，
否则读数与阈值都会失真（STG-EQ-01 演练中这三条全部踩过）：

1. **一账号一 TOTP**：`ACCOUNTS_FILE` 每行 `subject,totp_secret`，脚本按主体取密钥；
   单密钥的 `TOTP_SECRET` 只在共用密钥的测试环境使用。
2. **刷新前必须过 MFA**：受 MFA 保护的登录只返回 `MFAChallenge`，拿不到刷新令牌；
   `refresh` 场景的登录段会先完成一次 TOTP 校验，但**只对 refresh 请求计时**。
3. **预期拒绝不计入失败率**：错误口令 / 不存在账号 / 错误动态码 / 已轮换令牌重放都返回
   `40100`，脚本用 `http.expectedStatuses(200, 401)` 把它们排除在 `http_req_failed` 之外；
   同一账号在同一 30s 时间步内被并发校验会被服务端按 TOTP 重放拒绝，单独计入 `skipped`。

## 两条路线的分工

- **k6（staging）**：回答「生产规格下 SLO 是否达标」。密码登录的 Argon2 成本与 CPU 强相关，
  只有固定环境才有可比性；因此登录 / 刷新 / MFA 只登记 staging 读数。
- **Go runner（本机）**：回答「同一台机器上，这次改动是否让读路径变慢」。固定到达率、
  丢弃预热样本、产物 JSON 落盘，便于前后对比；硬件差异只影响绝对值，不影响同机回归判断。
`scim_sync.js` 分两段执行：场景内并发建用户（`shared-iterations`），组在 `teardown` 里串行创建。
组必须后置——成员按 `externalId` 解析，与用户创建并发会引用尚未建出的账号而返回
`400 invalidValue`，产生假失败。总同步耗时 = 场景耗时 + teardown 耗时；幂等复跑时
`201` 变成 `409 uniqueness`，脚本用 `http.expectedStatuses(201, 409)` 把「已存在」排除在
`http_req_failed` 之外。

## staging 演练夹具（[`staging/`](./staging)）

生产规格 A 端（`mfa.admin_required=true`）需要「每账号独立 TOTP」，手工造账号池不现实，
因此把预置夹具纳入仓库：

| 文件 | 作用 |
|---|---|
| [`staging/provision.py`](./staging/provision.py) | 引导管理员改密 + MFA 绑定、签发 SCIM 令牌、批量建号并逐账号绑定 TOTP、导出 `accounts.csv` |
| [`staging/seedpw/main.go`](./staging/seedpw/main.go) | 用生产同款 Argon2id 编码把压测口令写入 `identity_credential`（SCIM 账号按设计无本地口令） |
| [`staging/envoy.yaml`](./staging/envoy.yaml) | 两副本 round-robin 入口，模拟 staging Ingress → Service |

令牌与 TOTP 密钥只落 `tmp/stg-eq/state.json`（`tmp*` 已 gitignore），不进仓库。
完整命令与偏离项见 [`docs/perf/LOAD_BASELINES.md`](../../docs/perf/LOAD_BASELINES.md) §4.5。

## 产物

`artifacts/*.json` 是登记表的证据（`scenario` / `rate_rps` / `p50_ms` / `p95_ms` / `p99_ms` /
`success_rate` / `errors` / 环境指纹）。**每个文件都必须被登记表引用**，否则门禁按孤儿产物拦截；
反之，登记表里「结论=通过」的行没有产物也会被拦截。
