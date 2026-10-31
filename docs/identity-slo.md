# 身份域 SLO 与容量基线

> 本文定义身份域（internal/identity）的生产 SLO、容量公式与压测方法。  
> 指标来源：`internal/identity/observability` 注册的 `identity_*` 指标 + 平台 `http_server_*`。

## 1. SLO

| SLI | 目标 | 测量方式 | 告警 |
|---|---|---|---|
| 登录成功率 | ≥ 99.5%（排除 429/503 后的业务失败） | `1 - failure/(success+failure+challenge)` | `IdentityLoginFailureRateHigh` |
| 登录 P95 | ≤ 300 ms | `identity_login_duration_seconds` p95 | API P99 告警联动 |
| 登录 P99 | ≤ 800 ms | p99 | API P99 告警联动 |
| 刷新成功率 | ≥ 99.9% | `identity_refresh_total` | `IdentityRefreshFailureRateHigh` |
| 刷新 P95 | ≤ 150 ms | HTTP 指标按路由 | API P99 |
| MFA 校验 P95 | ≤ 200 ms | MFA verify 指标 + HTTP | API P99 |
| 令牌重放 | 0 次/5m | `identity_token_reuse_total` | `IdentityTokenReuseDetected` |
| 撤销存储错误 | 0 次/5m | `identity_revocation_store_errors_total` | `IdentityRevocationStoreErrors` |
| 可用性 | 99.9%/月 | 5xx 比率 + 依赖健康 | `API5xxRateHigh` |

> 登录成功率口径：故意排除限流与依赖不可用造成的失败，避免把攻击流量计入业务 SLO；安全侧另用 token reuse / lockout 告警。

## 2. 容量公式

### 2.1 密码登录

- 单次登录约 10 次 DB/Redis 往返 + 1 次 Argon2（约 19 MiB / 2 次迭代）。
- 每副本并发上限 `C = identity.password.max_concurrent`（默认 2×CPU）。
- 内存预算 ≈ `C × 19 MiB + Go/HTTP 常驻`；HPA 扩容后必须同步核算。
- DB 连接预算：`副本数 × pkg/db MaxOpenConns(默认 25)`，必须小于 MySQL `max_connections` 的 60%。

### 2.2 Redis

- 每个已认证请求 2–3 次 Redis 读：账号快照、会话快照、jti。
- Redis 故障时账号/会话快照回退 DB；熔断 10s 内跳过 Redis，避免超时放大。
- 账号快照回源频率 ≈ `活跃账号数 × 副本数 / state_ttl`；`state_ttl` 默认 30s，生产应观察 DB 回源 QPS。

### 2.3 数据保留

- 每轮清理上限 = `batch_size × max_batches_per_run`（默认 1000 × 50 = 50,000 行/轮）。
- 日吞吐 ≈ `每轮上限 × (24h / cleanup_interval)`；必须大于日新增 rotated 会话 + 失败登录尝试。
- 清理依赖 000008 索引；未执行索引时吞吐会随表增长退化。

## 3. 压测方法

1. 在 staging 使用生产同规格 DB/Redis，开启 `identity.enabled=true`、`mfa.admin_required=true`（A 端）。
2. 场景：
   - 密码登录：正确密码 70%、错误密码 20%、不存在账号 10%。
   - 刷新：有效令牌 90%、已轮换令牌 10%（验证重放路径）。
   - MFA：TOTP 成功 / 错误各 50%。
3. 采样：每档 10 分钟，记录 `identity_login_duration_seconds` p50/p95/p99、5xx、DB 连接池等待、Redis RTT。
4. 通过标准：P95/P99 满足第 1 节 SLO；无连接池等待；Redis 故障注入时账号/会话校验仍可用（jti 按 fail_closed 拒绝）。
5. 故障注入：
   - 停 Redis：验证 DB 回退、熔断与告警；确认 Pod 不被 readiness 批量摘流。
   - 制造登录失败：验证 `IdentityLockoutSpike` 与锁定审计。
   - 重放刷新令牌：验证整链撤销与 `IdentityTokenReuseDetected`。

## 4. 容量评审清单

- [ ] `password.max_concurrent` 与 Pod 内存 limit 匹配
- [ ] 副本数 × DB 连接池 < MySQL 上限 60%
- [ ] `revocation.state_ttl` 下 DB 回源 QPS 可接受
- [ ] 清理日吞吐 ≥ 日新增会话 + 登录尝试
- [ ] 登录/刷新/MFA P95/P99 达标
- [ ] Redis 故障注入通过
