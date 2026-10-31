# identity 剩余 P2 专项修复台账（第 8.3 批次第二轮）

> 来源：[identity-enterprise-production-gap-review.md](./identity-enterprise-production-gap-review.md) §8.3
> 与 [identity-p2-batch-8-3-register.md](./identity-p2-batch-8-3-register.md) 的季度 backlog。
> 范围：24 项剩余 P2，全部纳入本轮，不做降级。
> 周期：3 个工作日的里程碑（D1 / D2 / D3），不绑定日历日期；责任为角色。

## 1. 三日排期

| 日 | 主题 | 项 |
|---|---|---|
| D1 | 可靠性与性能 | PERF-04、PERF-05、PERF-06、PERF-07、PERF-08 |
| D2 | 运维可观测与可维护性 | OPS-03、OPS-04、OPS-05、OPS-06、OPS-07、MNT-02、MNT-03、MNT-04、MNT-05、MNT-06 |
| D3 | 业务适配与回归 | BIZ-01~09；全量回归、专项报告、闭环确认 |

每项统一里程碑：M1 代码 + 单测 → M2 集成 / 配置校验 → M3 回归 → M4 报告归档。

## 2. 台账明细

| ID | 问题描述 | 复现 / 影响 | 责任角色 | 验收标准 |
|---|---|---|---|---|
| PERF-04 | `evictFamilies` 以 `limit=0` 拉取设备，SQL 归一为 20，活跃设备可超过上限 | 单账号活跃设备 >20 时淘汰不足 | identity 后端 | 活跃设备严格 ≤ `max_per_account`；分页 / 计数用例通过 |
| PERF-05 | 账号级与 IP 级登录限流共用 1 rps，共享出口 NAT 误伤；换 IP 可绕过账号桶 | 正常登录 429；账号桶退化 | identity 后端 | 账号级与 IP 级独立参数；账号桶不随 IP 变化；配置校验通过 |
| PERF-06 | OAuth 固定 10s 超时、无 provider 级熔断，全局 1 rps 过低 | IdP 抖动占用协程；正常 C 端登录 429 | identity 后端 | 超时可配置（3–5s）；provider 失败熔断；限流可配置 |
| PERF-07 | 账号列表关键词仍为内存全量扫描（S5 后上限 10000） | 大租户列表慢查询 | identity 后端 | 支持精确主体查询走索引；默认分页不扫全量；用例覆盖 |
| PERF-08 | 缺活跃会话 / 登录 QPS / Redis 调用量指标 | HPA 只能按 CPU | identity 后端 + 平台运维 | 指标注册并在 Dashboard / HPA 可用 |
| OPS-03 | 刷新 / MFA / step-up / OAuth 无时延直方图，告警无时延规则 | SLO 无法测量 | 平台运维 | 四类直方图 + P95 告警规则 |
| OPS-04 | 身份域无审计 Outbox 积压 gauge | 积压不可观测 | 平台运维 | 注册 `identity.audit` 积压 gauge + 年龄告警 |
| OPS-05 | HPA 仅按 CPU | 扩容滞后 | 平台运维 | 认证 QPS / 活跃会话作为 HPA 辅助信号 |
| OPS-06 | 运行时仍有内存回退 | 配置错误可能静默降级 | identity 后端 | prod/staging 启动日志与自检断言无内存回退 |
| OPS-07 | 短信 breaker 状态仅日志 | 通道不可用无明确指标 | 平台运维 | breaker state / 失败率指标 + 告警 |
| MNT-02 | `auth_service.go` 等大文件职责过多，DTO / 解码重复 | 变更影响面大 | identity 后端 | 至少完成 DTO / 解码收敛与核心职责拆分；文件行数下降 |
| MNT-03 | 覆盖率门禁不含 identity app/transport | 高风险编排无门槛 | identity 后端 + CI | app ≥75%、transport ≥70% 门禁生效 |
| MNT-04 | SLO 文档口径过期，MFA 无时延指标 | 容量规划失真 | identity 后端 | 文档口径与指标一致；MFA/刷新 SLI 可测 |
| MNT-05 | OAuth 超时、TOTP 参数、恢复码数量、临时口令长度固定 | 合规等级无法调整 | identity 后端 | 配置化 + 启动期边界校验 |
| MNT-06 | 归档表缺设备画像列，归档写入未复制 | 历史设备不可还原 | identity 后端 | 迁移补齐列；归档写入复制；回滚脚本齐备 |
| BIZ-01 | C 端无 `/c/v1/me` | C 端无个人中心 | identity 后端 | GET/PATCH `/c/v1/me` + 审计 + 用例 |
| BIZ-02 | 无身份绑定 / 合并，OAuth-only 注销路径弱 | 同一人多账号、资产割裂 | identity 后端 | 绑定 / 解绑端点 + 再认证注销；用例覆盖 |
| BIZ-03 | MFA 仅 A 端 TOTP，B 端无法启用，因子单一 | 高合规租户无法加强 | identity 后端 | MFA 策略按端配置；因子接口预留；B 端可选启用 |
| BIZ-04 | 密码策略全局，无租户级覆盖 | 金融租户无法更严 | identity 后端 | 租户级覆盖（带安全下限）+ 用例 |
| BIZ-05 | 错误文案硬编码英文，短信配额按 UTC | 多语言 / 跨时区偏差 | 产品 + identity 后端 | 稳定 code + 前端本地化；配额时区可配置 |
| BIZ-06 | `merchant_id` 仅 trim，库排序规则不敏感 | `M-1` 与 `m-1` 冲突 | identity 后端 | 与唯一键一致的规范化 + 迁移 + 单测 |
| BIZ-07 | 仅接受 E.164 | 本地格式输入 400 | 产品 + identity 后端 | 明确 E.164 契约为默认；可选区域规范化 |
| BIZ-08 | 并发刷新 / 登录后立即 step-up 边界策略 | 误登出 / 需等 30s | identity 后端 | REL-06 宽限 + step-up 时间步独立 / 复用策略 |
| BIZ-09 | 身份域多租户边界未明确 | 未来多商户 C 端不支持 | 产品 | 在文档明确边界与扩展模型 |

## 3. 每日同步模板

- 已完成：ID / 提交 / 验证命令与结果。
- 正在处理：ID / 当前里程碑 / 预计下一步。
- 阻塞：ID / 阻塞原因 / 需要协调的角色 / 临时措施。
- 风险接受：仅在产品明确接受时记录，不默认降级。

## 4. 完成记录（第二轮闭环）

| ID | 状态 | 提交 | 验证 |
|---|---|---|---|
| PERF-04 | 已完成 | `5fb3ccc` | 设备淘汰分页；`go test ./internal/identity/...` |
| PERF-05 | 已完成 | `6fe34b4` | 账号 / IP 双桶；`login_guard_test.go` |
| PERF-06 | 已完成 | `e16c091` | OAuth 超时 + provider 熔断；`oauth_test.go` |
| PERF-07 | 已完成 | `38db43b` | subject 精确索引查询；`account_service_test.go` |
| PERF-08 / OPS-03 / OPS-04 / OPS-05 / OPS-07 | 已完成 | `992ce77` | 指标 + 告警 + HPA；`check-test-gates` |
| OPS-06 | 已完成 | `18ed140` | prod/staging 启动断言；`runtime` 单测 |
| MNT-02 | 已完成 | `5e51ade` | DTO / 审计拆分；`auth_service.go` 2004→1786 行 |
| MNT-03 | 已完成 | `abb1215` | TEST-06 门禁：app ≥75%、路由 ≥70%、handler 基线 ≥15% |
| MNT-04 | 已完成 | `9d3415a` | SLO 口径与指标对齐 |
| MNT-05 | 已完成 | `e4c0317` | TOTP / 恢复码 / 临时口令配置化 + 边界校验 |
| MNT-06 | 已完成 | `01a2ea4` | 迁移 000019 + 归档复制 + down |
| BIZ-01 | 已完成 | `49861c9` | `GET/PATCH /c/v1/me` + OpenAPI 4.1.0 |
| BIZ-02 | 已完成 | `56ff177` | 绑定 / 解绑 / 回调 + state 账号绑定；OpenAPI 4.2.0 |
| BIZ-03 | 已完成 | `d0ad831` | MFA 端别策略 + 票据支持 B 端 + 因子预留 |
| BIZ-04 | 已完成 | `d71da14` | 商户级密码策略（只能收紧）+ 单测 |
| BIZ-05 | 已完成 | `b3d0318` | 稳定 code 契约 + 短信配额时区 |
| BIZ-06 | 已完成 | `fe2d194` | 商户 ID 规范化 + 迁移 000020 |
| BIZ-07 | 已完成 | `5fbefbe` | E.164 默认 + 可选地区补全 |
| BIZ-08 | 已完成 | `0e5d5ed` | 登录后 step-up 复用宽限 |
| BIZ-09 | 已完成 | `b06311a` | 多租户边界与扩展模型文档 |

## 5. 变更记录

| 日期 | 内容 |
|---|---|
| 第二轮启动 | 24 项全部纳入；D1 性能与可靠性、D2 运维与可维护性、D3 业务适配与回归 |
| D1 收口 | PERF-04～08、OPS-03/04/05/07 完成并提交 |
| D2 收口 | OPS-06、MNT-02～06 完成并提交 |
| D3 收口 | BIZ-01～09 完成并提交；全量回归与专项报告归档 |
