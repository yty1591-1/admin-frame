# P1-1 Argon2 移出数据库事务 · 执行报告

> 任务：按 `identity-p1-remaining-implementation-plan.md` §1 执行 P1-1。
>
> 执行日期：2026-10-31
>
> 状态：**代码与单元/回归验证完成；staging 性能压测待按验收流程执行**。

---

## 1. 目标与交付范围

| 项 | 内容 |
|---|---|
| 目标 | 密码 Argon2 校验移出数据库事务；事务内只做状态复核与写入 |
| 范围 | `internal/identity/app/auth_service.go`、`app/metrics.go`、`observability/metrics.go`、相关测试 |
| 不在范围 | 前端、登录接口契约（无新增字段）、数据库 schema（无迁移） |

---

## 2. 实施步骤与关键节点

| 节点 | 内容 | 结果 |
|---|---|---|
| N1 | 设计两阶段登录：`probeLogin` + `completeProbeInTx` | 完成 |
| N2 | 事务外完成限流、账号/凭据读取、Argon2 校验、可选重哈希 | 完成 |
| N3 | 失败路径拆为 `recordLoginFailureInTx`，事务内只写 attempt/audit/锁定 | 完成 |
| N4 | 事务内重新加载账号/凭据，复核状态与哈希 | 完成 |
| N5 | 增加登录事务耗时指标 `identity_login_tx_duration_seconds` | 完成 |
| N6 | 单元测试：事务边界、并发改密复核 | 完成 |
| N7 | 全仓构建、测试、vet、回归 | 完成 |

### 2.1 两阶段流程

```text
Login
 ├─ probeLogin（无事务）
 │   ├─ 参数/商户校验
 │   ├─ 账号级 + IP 级限流
 │   ├─ FindBySubject / Authorize
 │   ├─ FindByAccount(password)
 │   ├─ Argon2 VerifyContext
 │   ├─ 可选 Hash（透明重哈希）
 │   └─ 产出 loginProbe{failure | credentialHash | rehash...}
 └─ runTx
     ├─ 失败：recordLoginFailureInTx（attempt + audit + 可能锁定）
     └─ 成功：completeProbeInTx
          ├─ 重新加载账号 + Authorize
          ├─ 重新加载凭据并比对 hash
          ├─ 应用重哈希
          ├─ pending 激活
          ├─ 强制改密 / MFA 闸门
          └─ completeLogin（RecordSuccess + createSession + audit）
```

---

## 3. 遇到的问题与解决方案

| 问题 | 现象 | 解决方案 |
|---|---|---|
| Argon2 在事务内 | 事务连接被 19MiB/t=2 的哈希长时间占用 | 拆出 `probeLogin`，事务前完成校验 |
| 失败路径重复哈希 | 若失败后回事务重验，会再次消耗 Argon2 | `loginProbe.failure` 携带原因，事务内只写 attempt/audit |
| Probe 与事务间状态变化 | 并发改密/禁用可能签发旧凭据会话 | 事务内重载账号/凭据，比对 `credential.Hash` 并重新 `Authorize` |
| 401 拒绝需要落库 | `runTx` 默认回滚 | 沿用 `isRejected` 语义：401 错误在事务内吞掉以提交，事务外返回原错误 |
| 指标接口扩展 | 新增指标方法导致测试桩不满足接口 | 同步实现 `recordingMetrics.ObserveLoginTxDuration` |
| 未使用变量 | `probeLogin` 中多余的 `now` | 移除并复用时钟调用 |

---

## 4. 验收标准核对

| 类别 | 标准 | 结果 | 证据 |
|---|---|---|---|
| 功能 | 密码正确/错误、账号不存在、锁定、停用、无凭据、MFA、强制改密、重哈希保持原行为 | PASS | 全量 `internal/identity/app` 测试 |
| 事务边界 | Argon2 不在业务事务内执行 | PASS | `TestLoginPasswordVerifyOutsideTransaction` |
| 并发一致性 | Probe 后凭据被并发修改时拒绝登录，不签发会话 | PASS | `TestLoginRejectsCredentialChangedBetweenProbeAndTx` |
| 失败落库 | 失败路径仍在事务内写 attempt/audit；401 提交、5xx 回滚 | PASS | 既有锁定/审计测试 + 代码路径 |
| 边界 | 空 subject/密码、商户缺失/越权、账号不存在、无凭据、并发槽位超时 | PASS | 既有测试 + 两阶段测试 |
| 性能指标 | 登录 P95 ≤300ms、P99 ≤800ms；事务 P95 ≤20ms；连接池等待 P95 ≤10ms | **待 staging 压测** | 已注册 `identity_login_tx_duration_seconds`，需按 SLO 文档执行 |
| 回归 | `go test ./...` 全绿 | PASS | 全仓测试输出 |
| 静态检查 | `go build ./...`、`go vet` | PASS | 命令输出 |
| 回滚 | 回退本次提交即恢复旧路径（无 schema 变更） | PASS | 无迁移、无配置变更 |

> 性能阈值属于 staging 验收门禁，本次在本地仅验证代码路径与功能正确性；未执行 k6 压测，不应视为性能已签字通过。

---

## 5. 变更清单

### 代码

- `internal/identity/app/auth_service.go`
  - `Login` 改为两阶段
  - 新增 `loginProbe`、`probeLogin`、`recordLoginFailureInTx`、`completeProbeInTx`、`auditLoginStateChanged`
  - 新增事务耗时上报
- `internal/identity/app/metrics.go`
  - `Metrics` 新增 `ObserveLoginTxDuration`
- `internal/identity/observability/metrics.go`
  - 新增 `identity_login_tx_duration_seconds` 直方图
- `internal/identity/app/auth_two_phase_test.go`
  - 事务边界测试、并发改密复核测试
- `internal/identity/app/auth_service_test.go`
  - `recordingMetrics` 补齐新接口

### 可观测性

- `deploy/observability/grafana/dashboards/identity.json`
  - 登录耗时面板新增 `tx p95`

---

## 6. 验证记录

```text
go test ./internal/identity/app -run TestLoginPasswordVerifyOutsideTransaction -v
  PASS
go test ./internal/identity/app -run TestLoginRejectsCredentialChangedBetweenProbeAndTx -v
  PASS
go build ./...
  PASS
go test ./... -count=1
  PASS（全部包）
go vet ./internal/identity/... ./cmd/internal/identityruntime/...
  PASS
```

---

## 7. 风险与后续

| 风险 | 说明 | 应对 |
|---|---|---|
| 性能未实测 | 本地无 staging 同规格 DB/Redis 与压测工具 | 按 `docs/identity-slo.md` 执行 k6；以 `identity_login_tx_duration_seconds` p95 ≤20ms 作为门禁 |
| 状态复核窗口 | Probe 与事务间仍有毫秒级窗口 | 事务内重载 + 哈希比对；冲突返回 401，不签发会话 |
| 失败审计失败关闭 | 审计写入失败会回滚业务 | 与既有策略一致；依赖 Outbox/审计可用性 |
| 兼容性 | API/DB 无变更 | 可直接滚动发布；回滚仅需回退应用版本 |

---

## 8. 结论

P1-1 的代码改造、单元验证与全仓回归已完成，事务内不再执行 Argon2，且并发改密场景已被事务内复核拦截。剩余动作是 staging 性能压测签字，不属于代码交付阻断项。
