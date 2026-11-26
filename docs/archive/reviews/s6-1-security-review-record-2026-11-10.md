> **归档**：本记录已于 2026-11-11 闭环并归档，只读不再维护；安全结论已沉淀到
> [`identity-authn.md`](../../identity-authn.md) §21 与 [`SECURITY_STANDARDS.md`](../../SECURITY_STANDARDS.md)，归档索引见 [`archive/README.md`](../../README.md)。

# S6-1 身份域安全评审记录（2026-11-10）

| 字段 | 内容 |
|---|---|
| 评审对象 | [`identity-authn.md`](../../identity-authn.md) §7.3 / §8.2.1 / §21 + [`threat-model.md`](../../identity/threat-model.md) |
| 评审类型 | `S6-1` 协议与安全设计评审（威胁建模、错误码与配置项冻结） |
| 组织者 | IAM 工程组 |
| 计划评审日期 | 2026-11-10 |
| 评审结论 | **通过（2026-11-11）**：设计与实现一致，未发现高危/严重问题；`SEC-F-01` / `SEC-F-02` 已修复，`C1`～`C3` 三项条件全部闭环 |
| 当前状态 | **已闭环**：§3 清单已逐条核对、§4 决议已记录、§6 已签字；`C1`～`C3` 均已完成（`C2` / `C3` 本地 kind 实测，`C1` 以 kind `af-verify` / `STG-EQ-01` 等价环境证据放行），本记录已归档 |
| 关联 | [`iam-p1-5-7-engineering-plan.md`](../plans/iam-p1-5-7-engineering-plan.md) §3.3 `S6-1`；[`SECURITY_STANDARDS.md`](../../SECURITY_STANDARDS.md) §9 |

## 1. 评审范围

| 纳入 | 说明 |
|---|---|
| OIDC 授权码 + PKCE | `identity-authn.md` §7.3，已 staging 真实 Keycloak 验收 |
| SAML 2.0 SP-initiated | §8.2.1，已 kind 等价环境验收 |
| SCIM 2.0 | §21.1，代码已交付、默认关闭 |
| M2M client credentials | §21.2，代码已交付、默认关闭 |
| 共用机制 | 令牌声明与撤销（§4.1 / §6）、审计事件（§11.1）、密钥与配置校验（§10） |
| 威胁与残留风险 | [`threat-model.md`](../../identity/threat-model.md) §3（32 条）与 §4（`RR-01`～`RR-06`） |

不在范围：密码与 MFA 的既有实现、基础设施（Redis / MySQL / K8s）基线、IAM 权限模型本身。

## 2. 评审输入

| 材料 | 位置 |
|---|---|
| 设计正文 | [`identity-authn.md`](../../identity-authn.md) |
| 威胁建模与残留风险 | [`threat-model.md`](../../identity/threat-model.md) |
| 关键实现 | `internal/identity/{app,transport/scim,transport/rest,infrastructure/oauth,infrastructure/saml}` |
| 门禁 | [`check-authn.sh`](../../../scripts/ci/check-authn.sh)、`make lint`（gosec）、`make secrets`、`make vuln` |
| 端到端用例 | `tests/integration/identity/{scim_sync_test.go,m2m_test.go}` |
| 接入与处置 | [`identity-federation-onboarding.md`](../../runbooks/identity-federation-onboarding.md)、[`identity-auth-failure.md`](../../runbooks/identity-auth-failure.md) |

## 3. 检查清单

逐条勾选，「不适用」须写理由；`【偏差】`标记的条目见 §5。

| # | 检查项 | 依据 | 结论 |
|---|---|---|---|
| 1 | 仓库无密钥 / 私钥 / 真实 DSN / 完整令牌 | SEC-01 | ✅ `make secrets`（gitleaks）无命中；`check-authn`「example configs 无硬编码密钥」通过（2026-11-10 重跑 exit 0） |
| 2 | 令牌、断言、`client_secret`、`userName` / 邮箱未进入日志、Trace、审计正文 | SEC-05 | ✅ 2026-11-10 演练实测：D2 IdP 不可用仅回 `oauth provider unavailable`，不含请求参数；SCIM `detail` 不回显原文；`check-authn` 日志断言通过 |
| 3 | 三端令牌域与 transport 未互相引用 | SEC-03 | ✅ `check-layering` / `check-transport-imports` 无跨端引用 |
| 4 | prod / staging 配置无 `auth.mode=demo` | SEC-04 | ✅ `check-config`（CFG-01～CFG-08）2026-11-10 重跑 exit 0 |
| 5 | 协议端点均需鉴权，仅健康检查匿名 | SEC-07 | ✅ `check-authn` 路由断言；`/admin/scim/v2/*` 仅接受 `scim_` 令牌 |
| 6 | 无未标注的调试端点 | SEC-08 | ✅ `check-debug-endpoints` 无未标注端点；与第 5 项同口径 |
| 7 | gosec 无未处置项；4 处 `nolint:gosec` 的理由成立（摘要占位 / SQL 列清单 / 审计动作名 / 权限码） | SEC-09 | ✅ `make lint`（gosec）无未处置项；4 处豁免理由经复核成立（本轮未重跑 `make lint`，以门禁结果为准） |
| 8 | 高危依赖漏洞已缓解 | SEC-12 | ✅ `make vuln`（govulncheck）无高危/严重；中危项按依赖升级窗口处置（本轮未重跑，以门禁结果为准） |
| 9 | 生产入口 TLS；无 Cookie 承载令牌 | SEC-10 | ✅ 入口 TLS 由 Ingress / Envoy 终止；三端令牌只走 `Authorization`，无 Cookie 承载 |
| 10 | 越权对外表现为「不存在 / 未认证」，不泄露他人数据 | SEC-14 | ✅ `check-authz` 通过；SCIM 越权返回 `40300`，跨租户查询返回空集不报存在性 |
| 11 | 敏感字段落库为 HMAC 索引 + AES-GCM 密文，密钥来自环境 / KMS | SEC-05、`pkg/crypto` | ✅ `identity_account` 主体密文 + HMAC 索引；`identity.subject.keys` 仅从 Secret 注入 |
| 12 | 撤销、限流、M2M 签发在 prod / staging 强制 `fail_closed` | 设计 §10.1 | ✅ `check-config` 强制 prod/staging `fail_closed`，本地覆盖为可选项仅限 dev |
| 13 | 审计四要素（`actor_id` / `action` / `resource` / `outcome`）齐备，机器事件已带 `client_id` | 设计 §11.1 | ✅ `check-audit` 通过；`auth.m2m.*` / `auth.scim.*` 动作已注册，机器事件以 `client_id` 作 `actor_id`（2026-11-10 端到端用例实测落库） |
| 14 | 令牌 TTL 与时间偏差有显式上限（OIDC 30s、SAML 2m、M2M 15m） | 设计 §7.3 / §8.2.1 / §21.2 | ✅ 配置校验拒绝超限值；三处上限与设计一致 |
| 15 | 四通道开关默认 `false`，关闭时不注册路由（`404`） | 设计 §10.1 | ✅ `check-authn` 开关与 404 负向用例通过；`values.yaml` / `config` 默认 `false` |
| 16 | 残留风险 `RR-01`～`RR-06` 逐条确认接受或要求整改 | 威胁建模 §4 | ✅ 6 项全部接受；`RR-04` 保留为未缓解，复评时点 M6 准入（见 §4 `D1`） |
| 17 | `identity.m2m` 指标清单与实现一致【偏差】 | 设计 §21.2.3 | ✅ 偏差已修（`SEC-F-01` / `SEC-F-02`）；本地实测序列 `identity_m2m_token_total{channel,client,result,reason}` 与设计一致 |

## 4. 待决事项

| 编号 | 事项 | 选项 | 决议 |
|---|---|---|---|
| `D1` | 是否接受 `RR-01`～`RR-06` | 全部接受 / 有条件接受 / 要求整改 | **全部接受**：`RR-01`～`RR-03`、`RR-05`、`RR-06` 按原文复评时点跟踪；`RR-04` 保留为未缓解，M6 准入前复评（secret 超龄只告警不失效） |
| `D2` | `SEC-F-02` 的核对时点（见 §5） | staging 实测核对 / M6 前核对 | **已完成（2026-11-10，本地 kind 实测）**：label 序列核对 + 两条规则补齐 + `denied` 规则 pending→firing 验证；staging 侧在 M6 前复证一次 |
| `D3` | 四通道生产默认值是否维持 `enabled=false` | 维持 / 允许按客户灰度开启 | **维持 `enabled=false`**；客户灰度开启须走变更单，并附接入 Runbook 的八项验收清单 |
| `D4` | 本评审结论是否作为 M4（SCIM / M2M）验收通过的前置 | 是 / 否 | **是**：SCIM / M2M 的 M4 验收以本记录为前置，§6 条件不阻塞 M4，但阻塞 M6 |

## 5. 已发现的实现与文档偏差

| 编号 | 偏差 | 证据 | 建议处置 |
|---|---|---|---|
| `SEC-F-01` | ~~设计 §21.2.3 声明指标 `identity_m2m_secret_age_seconds{client}` 与超龄告警，代码未实现~~ **已修复（2026-11-10）**：新增 Gauge 并在签发时上报 secret 年龄，告警规则 `identity.m2m.secret_rotation_due`（P3）已加入 `rules/identity.yaml`，`ALERTS.md` 与 `key-rotation.md` 同步 | `internal/identity/observability/metrics.go`、`internal/identity/app/m2m.go`、`deploy/observability/prometheus/rules/identity.yaml` | 评审时确认阈值口径（默认 90 天与 `max_secret_age` 对齐） |
| `SEC-F-02` | 同节的「`result="denied"` 5 分钟突增」与「签发 p95 > 100ms」两条告警在 `rules/identity.yaml` 中**并不存在**；label 本身与代码一致（本地实测序列为 `identity_m2m_token_total{channel="admin",client="svc_dDLNp",result="issued",reason=""}` 与 `{result="denied",reason="invalid_client"}` / `{reason="invalid_scope"}`） | 本地 D5 实测 Prometheus 序列；`rules/identity.yaml` 全量检索无对应 `alert` | **已修复（2026-11-10）**：新增 `identity.m2m.denied_spike`（P2，5 分钟 denied 增量 > 10）与 `identity.m2m.latency`（P2，`identity_m2m_token_duration_seconds{result="issued"}` P95 > 100ms），`ALERTS.md` 同步登记 |

## 6. 结论与签字

| 角色 | 结论（通过 / 有条件通过 / 不通过） | 条件 / 整改要求 | 签字 | 日期 |
|---|---|---|---|---|
| 安全负责人 | 通过 | 无（`C1`～`C3` 已全部闭环） | @security-owner | 2026-11-11 |
| 研发负责人 | 通过 | 无（`C1`～`C3` 已全部闭环） | @identity-owner | 2026-11-11 |
| 运维负责人 | 通过 | 无（`C1`～`C3` 已全部闭环） | @sre-owner | 2026-11-11 |

**原通过条件的闭环情况**：

- **C1 staging 侧复证 `SEC-F-02`**：本地 kind 已实测 `identity.m2m.denied_spike` 进入 firing、P95 未误报；
  真实 staging 窗口未排到，按项目负责人 2026-11-11 决定以 `STG-EQ-01` 等价环境证据放行（§7 执行说明），
  阈值与 `reason` 分布结论可复核。
- **C2 `RR-04` 复评**：已在 2026-11-11 完成 secret 轮换演练，`identity.m2m.secret_rotation_due`
  可被值班动作闭环（[`key-rotation.md`](../../runbooks/key-rotation.md)）。
- **C3 JWKS 轮换指标快照**：已在 2026-11-11 补 `identity_oauth_jwks_cache_age_seconds` 与刷新失败计数实测快照。

> 签字形态说明：本仓库当前为单人维护场景，沿用 [`incident/RESPONSE.md`](../../incident/RESPONSE.md) §4
> 「人数不足时允许一人分角色」的既有口径，以角色责任人（`@security-owner` / `@identity-owner` /
> `@sre-owner`）会签，不虚构实名；真实责任人到岗后按 M6 前替换为实名签字，本记录结论不因此失效。

## 7. 整改跟踪

| 编号 | 整改项 | Owner | 期限 | 验证方式 | 状态 |
|---|---|---|---|---|---|
| `SEC-F-01` | 补指标 + 告警规则，并同步 `ALERTS.md` / `key-rotation.md` | IAM 工程组 | 2026-11-10 | 单测 `TestM2MObserveSecretAge` + 规则文件审查 | 已完成 |
| `SEC-F-02` | 核对 label 并补齐缺失的 denied 突增 / 签发 p95 告警 | IAM 工程组 | 待定 | 已完成：label 核对 + 两条规则补齐（`promtool` 25 条规则通过、本地 firing 验证） | 已完成 |
| `C1` | staging 侧复证 `identity.m2m.denied_spike` / `identity.m2m.latency` 的阈值与 `reason` 分布 | @identity-owner | M6 前 | staging 真实流量下规则不误报、异常可 firing | 已完成（本地 kind 等价环境，2026-11-11；staging 窗口按等价证据放行） |
| `C2` | `RR-04` 复评：secret 超龄轮换演练并核对值班动作可闭环 | @identity-owner / @sre-owner | M6 前 | 轮换演练记录 + 告警处置记录 | 已完成（本地 kind，2026-11-11） |
| `C3` | 补 JWKS 轮换的指标数值快照（计划 §13.2 缺口） | @identity-owner | M6 前 | `identity_oauth_jwks_cache_age_seconds` 与刷新失败计数快照 | 已完成（本地 kind，2026-11-11） |

> 执行说明（2026-11-11，本地 kind `af-verify`，`STG-EQ-01` 等价环境口径；staging 真实窗口未执行，
> 按项目负责人 2026-11-11 决定以等价环境证据放行，后续有窗口时按同一脚本复跑即可）：
>
> - `C1`：10 分钟窗口 `reason` 分布为 `issued=2.03`、`denied{invalid_client}=18.25`、
>   `denied{invalid_scope}=19.27`；`IdentityM2MTokenDeniedSpike` 由 pending 进入 firing（`for: 5m`，
>   `alert_id=identity.m2m.denied_spike`）。`IdentityM2MTokenLatencyP95High` 全程 inactive：
>   无 `issued` 样本时 P95 为 `NaN`，不会因空窗口误报。
>   staging 复证使用 [`scripts/observability/c1-m2m-alert-reverify.sh`](../../../scripts/observability/c1-m2m-alert-reverify.sh)
>   （造量 + 观测 + 出证据，不改规则；输出可直接粘贴到本表）。
> - `C2`：secret 年龄 526.75s / 587.64s 时 `IdentityM2MSecretRotationDue` 正常 pending→firing
>   （演练将阈值临时下调为 `>= 5`、`for: 1m` 以缩短验证窗口，结束后已恢复 `>= 90 * 24 * 3600`、`for: 1h`，
>   `duration=3600` 校验通过）；按 [`key-rotation.md`](../../runbooks/key-rotation.md) 轮换（`POST .../secrets`，
>   新 secret 43 字符、`fingerprint=sha256:2d2d7b665b15`、旧 secret 宽限至 `2026-11-11T13:31:04Z`），
>   用新 secret 签发 200 后 `identity_m2m_secret_age_seconds` 回落到 3.94s，告警随阈值恢复转为 inactive。
>   结论：`RR-04` 的「只告警不失效」在值班动作下可闭环（轮换 → 新 secret 签发 → 指标归零 → 告警消除）。
> - `C3`：重建本地 Keycloak realm 后执行一次 `authorize → callback`（HTTP 200）触发刷新，快照为
>   `identity_oauth_jwks_cache_age_seconds{provider="kc"}=58.01`、
>   `identity_oauth_jwks_refresh_total{provider="kc",result="success"}=1`（`result="failure"` 无序列），
>   对应 P1 `IdentityOIDCJWKSCacheStale` 与 P2 `IdentityOIDCJWKSRefreshFailing` 均未触发。

## 8. 通过后的文档动作

1. ✅ 已执行（2026-11-10）：[`threat-model.md`](../../identity/threat-model.md) §6 已填评审日期、结论、整改项；2026-11-11 条件闭环后，威胁建模状态已改为「已评审并通过」。
2. ✅ 已执行（2026-11-10）：计划 §3.3 `S6-1` 行改为「已完成」，版本升 v1.8，§13.2 补安全评审结论与 `C1`～`C3` 条件。
3. 不适用：`D3` 决议维持 `enabled=false`，无需修改 [`identity-federation-onboarding.md`](../../runbooks/identity-federation-onboarding.md) 的启用前置。
4. 不适用：`SEC-F-01` / `SEC-F-02` 已完成并在 §7 记录；`C1`～`C3` 是 M6 前提条件，统一在 §7 跟踪，不再重复登记 [`ACTION_ITEMS.md`](../../incident/ACTION_ITEMS.md)，避免双台账。
5. ✅ 已执行（2026-11-11）：`C1`～`C3` 全部闭环，本记录结论改为「通过」，按归档判据移入 `docs/archive/reviews/`。

## 9. 未通过处理

- 存在「不通过」：不修改设计结论，先按 §7 立整改项，复评通过后才可改 `S6-1` 状态。
- 仅 `SEC-F-01` 未闭环：可给「有条件通过」，条件为 M6 准入前修复或设计修订，且 `RR-04` 在威胁建模中保留为未缓解。

## 相关链接

- [身份域威胁建模](../../identity/threat-model.md)
- [身份认证设计](../../identity-authn.md)
- [安全基线](../../SECURITY_STANDARDS.md)
- [IAM 生产化整改工程规划](../plans/iam-p1-5-7-engineering-plan.md)
- [M0 立项会签单](./m0-signoff-record-2026-11-10.md)
- [文档地图](../../README.md)
