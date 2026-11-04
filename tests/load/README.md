# 压测执行器（ENT-16）

本目录只放**执行器**，基线数值与判定口径统一登记在
[`docs/perf/LOAD_BASELINES.md`](../../docs/perf/LOAD_BASELINES.md)。登记与产物的一致性
由 `make check-load-baseline` 校验（已入 `make ci`），压测本身**不进**合并门禁（PERF-06）。

| 执行器 | 目标环境 | 压什么 | 运行方式 |
|---|---|---|---|
| [`identity_login.js`](./identity_login.js) | staging（生产同规格） | 登录 / 刷新 / MFA 的时延 SLO（`docs/identity-slo.md` §1） | `k6 run tests/load/identity_login.js -e BASE_URL=...` |
| [`../e2e/load_baseline_test.go`](../e2e/load_baseline_test.go) | 本机（真实二进制 × MySQL × Redis） | 已认证读路径（鉴权 + 权限快照 + SQL + 序列化） | `make load-baseline` |

## 两条路线的分工

- **k6（staging）**：回答「生产规格下 SLO 是否达标」。密码登录的 Argon2 成本与 CPU 强相关，
  只有固定环境才有可比性；因此登录 / 刷新 / MFA 只登记 staging 读数。
- **Go runner（本机）**：回答「同一台机器上，这次改动是否让读路径变慢」。固定到达率、
  丢弃预热样本、产物 JSON 落盘，便于前后对比；硬件差异只影响绝对值，不影响同机回归判断。

## 产物

`artifacts/*.json` 是登记表的证据（`scenario` / `rate_rps` / `p50_ms` / `p95_ms` / `p99_ms` /
`success_rate` / `errors` / 环境指纹）。**每个文件都必须被登记表引用**，否则门禁按孤儿产物拦截；
反之，登记表里「结论=通过」的行没有产物也会被拦截。
