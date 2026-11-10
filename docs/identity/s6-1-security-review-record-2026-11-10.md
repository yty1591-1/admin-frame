# S6-1 身份域安全评审记录（待签）

| 字段 | 内容 |
|---|---|
| 评审对象 | [`identity-authn.md`](../identity-authn.md) §7.3 / §8.2.1 / §21 + [`threat-model.md`](./threat-model.md) |
| 评审类型 | `S6-1` 协议与安全设计评审（威胁建模、错误码与配置项冻结） |
| 组织者 | IAM 工程组 |
| 计划评审日期 |  |
| 当前状态 | **待评审**：清单未勾选、结论与签字为空，本文件不构成评审结论 |
| 关联 | [`iam-p1-5-7-engineering-plan.md`](../iam-p1-5-7-engineering-plan.md) §3.3 `S6-1`；[`SECURITY_STANDARDS.md`](../SECURITY_STANDARDS.md) §9 |

## 1. 评审范围

| 纳入 | 说明 |
|---|---|
| OIDC 授权码 + PKCE | `identity-authn.md` §7.3，已 staging 真实 Keycloak 验收 |
| SAML 2.0 SP-initiated | §8.2.1，已 kind 等价环境验收 |
| SCIM 2.0 | §21.1，代码已交付、默认关闭 |
| M2M client credentials | §21.2，代码已交付、默认关闭 |
| 共用机制 | 令牌声明与撤销（§4.1 / §6）、审计事件（§11.1）、密钥与配置校验（§10） |
| 威胁与残留风险 | [`threat-model.md`](./threat-model.md) §3（32 条）与 §4（`RR-01`～`RR-06`） |

不在范围：密码与 MFA 的既有实现、基础设施（Redis / MySQL / K8s）基线、IAM 权限模型本身。

## 2. 评审输入

| 材料 | 位置 |
|---|---|
| 设计正文 | [`identity-authn.md`](../identity-authn.md) |
| 威胁建模与残留风险 | [`threat-model.md`](./threat-model.md) |
| 关键实现 | `internal/identity/{app,transport/scim,transport/rest,infrastructure/oauth,infrastructure/saml}` |
| 门禁 | [`check-authn.sh`](../../scripts/ci/check-authn.sh)、`make lint`（gosec）、`make secrets`、`make vuln` |
| 端到端用例 | `tests/integration/identity/{scim_sync_test.go,m2m_test.go}` |
| 接入与处置 | [`identity-federation-onboarding.md`](../runbooks/identity-federation-onboarding.md)、[`identity-auth-failure.md`](../runbooks/identity-auth-failure.md) |

## 3. 检查清单

逐条勾选，「不适用」须写理由；`【偏差】`标记的条目见 §5。

| # | 检查项 | 依据 | 结论 |
|---|---|---|---|
| 1 | 仓库无密钥 / 私钥 / 真实 DSN / 完整令牌 | SEC-01 | [ ] |
| 2 | 令牌、断言、`client_secret`、`userName` / 邮箱未进入日志、Trace、审计正文 | SEC-05 | [ ] |
| 3 | 三端令牌域与 transport 未互相引用 | SEC-03 | [ ] |
| 4 | prod / staging 配置无 `auth.mode=demo` | SEC-04 | [ ] |
| 5 | 协议端点均需鉴权，仅健康检查匿名 | SEC-07 | [ ] |
| 6 | 无未标注的调试端点 | SEC-08 | [ ] |
| 7 | gosec 无未处置项；4 处 `nolint:gosec` 的理由成立（摘要占位 / SQL 列清单 / 审计动作名 / 权限码） | SEC-09 | [ ] |
| 8 | 高危依赖漏洞已缓解 | SEC-12 | [ ] |
| 9 | 生产入口 TLS；无 Cookie 承载令牌 | SEC-10 | [ ] |
| 10 | 越权对外表现为「不存在 / 未认证」，不泄露他人数据 | SEC-14 | [ ] |
| 11 | 敏感字段落库为 HMAC 索引 + AES-GCM 密文，密钥来自环境 / KMS | SEC-05、`pkg/crypto` | [ ] |
| 12 | 撤销、限流、M2M 签发在 prod / staging 强制 `fail_closed` | 设计 §10.1 | [ ] |
| 13 | 审计四要素（`actor_id` / `action` / `resource` / `outcome`）齐备，机器事件已带 `client_id` | 设计 §11.1 | [ ] |
| 14 | 令牌 TTL 与时间偏差有显式上限（OIDC 30s、SAML 2m、M2M 15m） | 设计 §7.3 / §8.2.1 / §21.2 | [ ] |
| 15 | 四通道开关默认 `false`，关闭时不注册路由（`404`） | 设计 §10.1 | [ ] |
| 16 | 残留风险 `RR-01`～`RR-06` 逐条确认接受或要求整改 | 威胁建模 §4 | [ ] |
| 17 | `identity.m2m` 指标清单与实现一致【偏差】 | 设计 §21.2.3 | [ ] |

## 4. 待决事项

| 编号 | 事项 | 选项 | 决议 |
|---|---|---|---|
| `D1` | 是否接受 `RR-01`～`RR-06` | 全部接受 / 有条件接受 / 要求整改 |  |
| `D2` | `SEC-F-02` 的核对时点（见 §5） | staging 实测核对 / M6 前核对 |  |
| `D3` | 四通道生产默认值是否维持 `enabled=false` | 维持 / 允许按客户灰度开启 |  |
| `D4` | 本评审结论是否作为 M4（SCIM / M2M）验收通过的前置 | 是 / 否 |  |

## 5. 已发现的实现与文档偏差

| 编号 | 偏差 | 证据 | 建议处置 |
|---|---|---|---|
| `SEC-F-01` | ~~设计 §21.2.3 声明指标 `identity_m2m_secret_age_seconds{client}` 与超龄告警，代码未实现~~ **已修复（2026-11-10）**：新增 Gauge 并在签发时上报 secret 年龄，告警规则 `identity.m2m.secret_rotation_due`（P3）已加入 `rules/identity.yaml`，`ALERTS.md` 与 `key-rotation.md` 同步 | `internal/identity/observability/metrics.go`、`internal/identity/app/m2m.go`、`deploy/observability/prometheus/rules/identity.yaml` | 评审时确认阈值口径（默认 90 天与 `max_secret_age` 对齐） |
| `SEC-F-02` | 同节的「`result="denied"` 5 分钟突增」与「签发 p95 > 100ms」两条告警在 `rules/identity.yaml` 中**并不存在**；label 本身与代码一致（本地实测序列为 `identity_m2m_token_total{channel="admin",client="svc_dDLNp",result="issued",reason=""}` 与 `{result="denied",reason="invalid_client"}` / `{reason="invalid_scope"}`） | 本地 D5 实测 Prometheus 序列；`rules/identity.yaml` 全量检索无对应 `alert` | **已修复（2026-11-10）**：新增 `identity.m2m.denied_spike`（P2，5 分钟 denied 增量 > 10）与 `identity.m2m.latency`（P2，`identity_m2m_token_duration_seconds{result="issued"}` P95 > 100ms），`ALERTS.md` 同步登记 |

## 6. 结论与签字

| 角色 | 结论（通过 / 有条件通过 / 不通过） | 条件 / 整改要求 | 签字 | 日期 |
|---|---|---|---|---|
| 安全负责人 |  |  |  |  |
| 研发负责人 |  |  |  |  |
| 运维负责人 |  |  |  |  |

## 7. 整改跟踪

| 编号 | 整改项 | Owner | 期限 | 验证方式 | 状态 |
|---|---|---|---|---|---|
| `SEC-F-01` | 补指标 + 告警规则，并同步 `ALERTS.md` / `key-rotation.md` | IAM 工程组 | 2026-11-10 | 单测 `TestM2MObserveSecretAge` + 规则文件审查 | 已完成 |
| `SEC-F-02` | 核对 label 并补齐缺失的 denied 突增 / 签发 p95 告警 | IAM 工程组 | 待定 | 已完成：label 核对 + 两条规则补齐（`promtool` 25 条规则通过、本地 firing 验证） | 已完成 |
| （按 §4 决议追加） |  |  |  |  |  |

## 8. 通过后的文档动作

1. [`threat-model.md`](./threat-model.md) §6 填写评审日期、结论、整改项，状态改为「已评审」。
2. 计划 §3.3 `S6-1` 行改为「已完成」，并在 §13.2 补安全评审结论与整改闭环情况。
3. 若 `D3` 允许客户灰度开启，同步更新 [`identity-federation-onboarding.md`](../runbooks/identity-federation-onboarding.md) 的启用前置。
4. §7 的整改项登记到 [`ACTION_ITEMS.md`](../incident/ACTION_ITEMS.md)。

## 9. 未通过处理

- 存在「不通过」：不修改设计结论，先按 §7 立整改项，复评通过后才可改 `S6-1` 状态。
- 仅 `SEC-F-01` 未闭环：可给「有条件通过」，条件为 M6 准入前修复或设计修订，且 `RR-04` 在威胁建模中保留为未缓解。

## 相关链接

- [身份域威胁建模](./threat-model.md)
- [身份认证设计](../identity-authn.md)
- [安全基线](../SECURITY_STANDARDS.md)
- [IAM 生产化整改工程规划](../iam-p1-5-7-engineering-plan.md)
- [M0 立项会签单](../iam/m0-signoff-record-2026-11-10.md)
- [文档地图](../README.md)
