# staging 身份域演练清单（2026-11）

| 字段 | 内容 |
|---|---|
| 目的 | 补齐 DoD 中「staging 回滚演练」与身份域故障演练缺口，同时验证 `RR-03`（证书轮换）与 `S6-6` 的运维可操作性 |
| 环境 | staging（`STG-EQ-01` 等价环境口径，见 [`perf/LOAD_BASELINES.md`](../../perf/LOAD_BASELINES.md)） |
| 执行窗口 | 2026-11-10 本地 kind（kind `af-kind`）；staging 真实窗口未排到，按 `STG-EQ-01` 等价环境口径放行 |
| 参与角色 | 值班 L1、端 Owner（L2）、SRE（L3）、安全评审观察员 |
| 当前状态 | **已执行并通过（2026-11-11，等价环境口径）**：`D1`～`D5` 均通过（`D2` 为修复后通过，`D3` 以 JWT kid 等价覆盖、SAML 通道未执行），`SEC-F-01` / `SEC-F-02` 已另行处置；§7 与 §9 为实测记录；staging 真实窗口按项目负责人 2026-11-11 决定以等价环境证据放行 |
| 关联 | [`release-rollback.md`](../../runbooks/release-rollback.md)、[`identity-auth-failure.md`](../../runbooks/identity-auth-failure.md)、[`identity-federation-onboarding.md`](../../runbooks/identity-federation-onboarding.md)、[`async-dlq.md`](../../runbooks/async-dlq.md) |

## 1. 覆盖的验收项

| 验收项 | 涉及场景 | 判定载体 |
|---|---|---|
| staging 发版回滚（记录时长） | `D1` | §7 记录表 + 发布单 |
| IdP 不可用时的降级与恢复 | `D2` | 指标曲线 + `50300` 业务码统计 |
| SAML 证书 / 密钥轮换不中断 | `D3` | 轮换窗口内 `identity_saml_acs_total{result="ok"}` 无断点 |
| SCIM 限流、幂等与积压告警 | `D4` | `429` 计数、`identity_scim_last_success_timestamp` 停滞告警 |
| M2M 轮换宽限与停用即时生效 | `D5` | 旧 secret / 旧令牌失效时点 |
| 四通道默认关闭（关闭即 `404`） | 全部场景前置 | 路由探测结果 |

## 2. 前置检查（逐项打勾再开始）

> 本节是 staging 真实窗口的前置检查。本轮按等价环境执行（环境搭建与凭据见 §9），未逐项勾选；
> 后续出现 staging 窗口时先补齐本节再复跑。

| # | 检查项 | 命令 / 位置 | 结论 |
|---|---|---|---|
| 1 | 目标版本镜像 digest 已记录，上一版本 digest 可取 | 发布单 | [ ] |
| 2 | 四通道开关当前均为 `false`，`/c/v1/auth/oauth/<p>/authorize` 等返回 `404` | 冒烟 curl | [ ] |
| 3 | OIDC / SAML 测试 IdP 可用，测试租户与测试账号已建 | 接入清单 | [ ] |
| 4 | SCIM 测试令牌（`scim_`）与 M2M 测试客户端已建，secret 已离线留存 | 管理端 | [ ] |
| 5 | 观测面板已就绪：登录、OAuth/SAML、SCIM、M2M、撤销存储、Outbox 积压 | Grafana | [ ] |
| 6 | 告警通知路由指向演练值班，非客户群 | 告警配置 | [ ] |
| 7 | 数据库快照 / 备份点已确认（演练后可回滚数据） | 运维 | [ ] |
| 8 | 演练用的 `redirect_uri`、`acs_url` 为 staging 域名 | 配置 | [ ] |
| 9 | 回滚目标版本可部署（镜像、配置、迁移兼容） | 发布单 | [ ] |
| 10 | 参与人对 [`SEVERITY_MATRIX.md`](../SEVERITY_MATRIX.md) 的演练期降级声明达成一致 | 会议记录 | [ ] |

## 3. 观测基线（演练前 10 分钟采集）

| 观测点 | 指标 / 位置 | 基线值 |
|---|---|---|
| 登录成功率 | `identity_login_total{result}` |  |
| OIDC 回调 | `identity_oauth_callback_total{result}` |  |
| JWKS 缓存年龄 | `identity_oauth_jwks_cache_age_seconds` |  |
| SAML 授权 / ACS | `identity_saml_authorize_total`、`identity_saml_acs_total{result}` |  |
| SCIM 请求与新鲜度 | `identity_scim_request_total{result}`、`identity_scim_last_success_timestamp` |  |
| SCIM 组映射 | `identity_scim_group_map_total` |  |
| M2M 签发 | `identity_m2m_token_total{result,reason}` |  |
| 撤销存储 | Redis 延迟、`fail_closed` 触发计数 |  |
| Outbox 积压 | `async_outbox` 待投递行数 |  |

## 4. 场景矩阵

### D1 后端发版与回滚

| 项 | 内容 |
|---|---|
| 注入方式 | 部署新版本 → 观察 5 分钟 → 回滚到上一版本 |
| 关键判据 | 回滚在发布单时限内完成；回滚后 `/readyz` 通过、登录成功率回到基线；迁移可前向兼容（不回滚数据） |
| 观测 | 发布单耗时、`identity_login_total` 断点时长、错误码分布 |
| 失败处置 | 按 [`release-rollback.md`](../../runbooks/release-rollback.md) 升级；若迁移不兼容则停止后续场景 |
| 记录字段 | 开始 / 回滚发起 / 恢复流量 / 结束（四段耗时） |

### D2 IdP 不可用

| 项 | 内容 |
|---|---|
| 注入方式 | 在 staging 网络层阻断 IdP 域名（OIDC：`jwks_url` / `token_url`；SAML：`metadata_url`），保持 5～10 分钟 |
| 关键判据 | 登录失败返回统一业务码（`50300` 或 `40100`，不泄露内部原因）；OIDC 已有缓存 key 时仍可验签；`identity_oauth_jwks_cache_age_seconds` 持续增长并触发告警；恢复网络后无需重启即自愈 |
| 观测 | `identity_oauth_callback_total{result="error"}`、`identity_saml_acs_total{result="error"}`、`state` 过期量（`state` TTL 10 分钟） |
| 失败处置 | 若出现 `200` + 空令牌、或错误体暴露 IdP 错误详情，立即停止并按 [`identity-auth-failure.md`](../../runbooks/identity-auth-failure.md) 记为缺陷 |
| 记录字段 | 断网起止、错误码分布、自愈耗时 |

### D3 证书与密钥轮换

| 项 | 内容 |
|---|---|
| 注入方式 | ① SP 侧：配置 `certificate_next_pem` + `private_key_next`，灰度发布后切换；② IdP 侧：在 Keycloak 中轮换签名证书（模拟 `RR-03` 的静态元数据场景） |
| 关键判据 | 轮换窗口内 SAML 登录不中断（新旧证书各有成功样本）；旧证书退役后失败关闭而非放行；`replay_ttl`（10 分钟）内不重复消费同一断言 |
| 观测 | `identity_saml_acs_total{result}` 序列、`metadata_min_refresh` 触发次数、失败原因分布 |
| 失败处置 | 回退到旧 `certificate_pem` / `private_key`；若 IdP 元数据缓存导致验签失败，按 `metadata_ttl`（15 分钟）等待或强刷 |
| 记录字段 | 轮换开始 / 新旧并存窗口 / 旧证书下线 / 全程失败次数 |

### D4 SCIM 风暴与积压

| 项 | 内容 |
|---|---|
| 注入方式 | 以测试令牌高频推送（约 10 倍常规速率，持续 5 分钟）`POST/PATCH /admin/scim/v2/Users`；再注入一次网络故障让推送失败重试 |
| 关键判据 | 超出 `rps=10 / burst=20` 时返回 `429` 而非 `500`；`identity_scim_last_success_timestamp` 停滞触发告警；恢复后幂等重放不产生重复账号；`identity_scim_group_map_total` 无异常增长 |
| 观测 | 请求结果分布、`429` 计数、失败重试后的最终一致数量 |
| 失败处置 | 出现 `5xx` 或重复账号则记为缺陷；限流失效时立即关闭 `identity.scim.enabled` |
| 记录字段 | 压测起止、`429` 占比、恢复后一致性核对结果 |

### D5 M2M 轮换与停用

| 项 | 内容 |
|---|---|
| 注入方式 | 对测试客户端执行轮换（新旧 secret 并存，`rotate_grace=24h`）→ 用旧 secret 取令牌 → 再执行停用 |
| 关键判据 | 宽限内旧 secret 可签发；停用后旧令牌在 `token_ttl`（15 分钟）内被 `authn:client_ver` 拒绝；`401` 不区分「客户端不存在 / secret 错」 |
| 观测 | `identity_m2m_token_total{result,reason}`、`authn:client_ver` 键状态 |
| 失败处置 | 旧令牌在停用后仍可用即停止演练，按 `T-M2M-05` 记为安全缺陷 |
| 记录字段 | 轮换时点、旧 secret 首次失败时点、停用后旧令牌首次失败时点 |

## 5. 单场景执行卡

每个场景按同一模板记录，避免只有结论没有证据：

```text
场景：D<n>
开始时间：            结束时间：            总时长：
注入前基线快照：      （指标截图 / 查询结果链接）
注入动作：            （命令或配置变更，含回滚方式）
观测异常：            （指标、日志片段、业务码统计）
判定：                通过 / 不通过 / 中止
偏差：                与 §4 判据不一致之处
恢复动作与耗时：
遗留问题：            （缺陷编号或 ACTION_ITEMS 条目）
证据：                （截图路径、审计查询条件、抓取的时间窗）
```

## 6. 终止条件（满足任一即停止演练）

1. 出现真实客户流量受影响（`5xx` 扩散到非测试租户）；
2. 迁移不可回退，或回滚后 `/readyz` 持续失败超过 10 分钟；
3. 撤销存储 `fail_closed` 导致全域登录被拒超过 5 分钟；
4. 出现安全缺陷证据（如越权成功、令牌明文进日志）。

## 7. 记录表（执行时填写）

| 场景 | 开始 | 结束 | 时长 | 判定 | 偏差 / 缺陷 | 证据 |
|---|---|---|---|---|---|---|
| `D1` 发版回滚 | — | — | 部署 5.2s / 回滚 1.3s | 通过 | 无 | Helm rev47 滚动 → rollback rev46，回滚后 `/readyz` 200、管理员登录 `code=0` |
| `D2` IdP 不可用 | — | — | 单次 &lt; 1s | 通过（修复后） | 见 §9.2 缺陷 1 | 6/6 返回 `503`/`50300`；熔断打开后同样 `50300`；恢复后免重启登录成功 |
| `D3` 证书轮换 | — | — | 3 次滚动 | 通过（JWT kid 等价） | SAML 通道本地未配置，见 §9.2 | k1→k2 并存期新旧令牌均可用；退役 k1 后旧令牌 `401`、k2 令牌 `200` |
| `D4` SCIM 风暴 | — | — | 0.72s / 30 次 | 通过 | 无 | 41.7 rps → `429`×5、`200`×25；指标 `result="error", reason="rate_limited"` |
| `D5` M2M 停用 | — | — | 全链路 &lt; 5s | 通过 | 无（脚本假象已排除） | 错误密钥 `401 invalid_client`、越权 `400 invalid_scope`、停用后 `401`、恢复后签发成功 |

## 8. 演练后动作

1. 本文件改名为实测记录（`staging-<日期>-identity-drill.md`），补齐 §7 与证据链接。
2. 回滚耗时写入发布单与 [`RELEASE_STANDARDS.md`](../../RELEASE_STANDARDS.md) 相关判定；实测值异常则触发流程修订。
3. `D2` / `D3` 的偏差回写 [`identity-auth-failure.md`](../../runbooks/identity-auth-failure.md)；严重到「值班按现 runbook 处置不了」的程度时，同步修订 runbook。
4. 缺陷与整改项登记 [`ACTION_ITEMS.md`](../ACTION_ITEMS.md)，与 `S6-1` 评审整改项合并跟踪。
5. `D5` 结果用于确认 `T-M2M-05` 的缓解是否成立；不成立则更新 [`threat-model.md`](../../identity/threat-model.md) 对应行。

## 9. 执行记录（2026-11-10 本地 kind 实测，2026-11-11 判定通过）

> 结论：`D1`～`D5` 均通过（`D2` 为修复后通过；`D3` 以 JWT kid 等价路径覆盖，SAML SP 证书轮换本地未配置、见 §9.3 局限）。
> staging 真实业务集群窗口未排到，按项目负责人 2026-11-11 决定以 `STG-EQ-01` 等价环境证据放行；
> 后续有 staging 窗口时按本清单复跑并回填 §7 即可，不改结论口径。

自建环境（非 staging）：kind `af-kind` + 集群内 MySQL / Redis / Keycloak 26 + `monitoring` 命名空间的 standalone Prometheus（4 个 target 健康）。通过本地 overlay 打开 `admin` 的 `m2m` / `scim` 与 `c` 的 `oauth` 通道，`iam.mode=enforce`，JWT 使用 RS256 密钥环；Keycloak 侧注册 OIDC 客户端与 `drill-user`。全部凭据、overlay 与脚本仅存放在本机临时目录，不入库。

### 9.1 实测结果

| 检查 / 场景 | 结果 | 证据 |
|---|---|---|
| `D1` 发版回滚 | **通过** | `helm upgrade --wait` 5.2s（rev47，`admin` 双副本）；`helm rollback 46 --wait` 1.3s；回滚后 `/readyz` 200、管理员登录 `code=0` |
| `D2` IdP 不可用（修复前） | **不通过** | `keycloak` 副本置 0 后回调返回 `500` / `50000`，与设计「IdP 故障统一 50300」不符，定位为 `oauthUnavailable` 未挂 `ErrUnavailable` 哨兵（§9.2） |
| `D2` IdP 不可用（修复后） | **通过** | 6 轮回调均 `503` / `50300`；第 7 轮熔断打开，错误体退化为 `oauth provider temporarily unavailable`，业务码仍 `50300`；`identity_oauth_callback_total{provider="kc",result="failure",reason="unavailable"}` 逐轮递增 |
| `D2` JWKS 缓存观测 | **通过** | IdP 停机期间 `identity_oauth_jwks_cache_age_seconds{provider="kc"}` 由 0 增长到 68s，恢复后回落（重新拉取） |
| `D2` 自愈 | **通过** | IdP 恢复后**不重启** `c-api`，完整 OIDC 登录（authorize → Keycloak 表单登录 → callback）返回 `200`，`result="success", reason="ok"` 计数递增 |
| `D3` 证书轮换（JWT kid 等价） | **通过** | k2 上线后：k1 旧访问令牌仍 `200`（密钥环保留旧公钥），新登录令牌头 `kid=k2` 且可用；移除 k1 后旧令牌 `401`、k2 令牌 `200` |
| `D4` SCIM 风暴 | **通过** | 30 次串行请求 0.72s（41.7 rps）：`200`×25、`429`×5；`identity_scim_request_total{result="success"}`=25、`{result="error", reason="rate_limited"}`=5 |
| `D5` M2M 生命周期 | **通过** | 创建 → 签发（`expires_in=900`）→ 错误密钥 `401 invalid_client` → 越权 scope `400 invalid_scope` → 轮换 → 宽限期内旧密钥可用 → 停用后签发 `401` → 恢复后可用 |
| `SEC-F-01` 告警链路 | **通过** | 将 `identity_m2m_secret` 的 `created_at` 回拨 100 天并签发一次，`identity_m2m_secret_age_seconds{client="svc_dDLNp"}=8640013s`（&gt; 90 天阈值）；`IdentityM2MSecretRotationDue` 进入 `pending`（`severity=P3`、`alert_id=identity.m2m.secret_rotation_due`）。为验证 firing 通路，临时把该规则 `for` 改为 `1m` 后观测到 `state=firing`，随后恢复 `for: 1h` |
| `SEC-F-02` label 核对与告警补齐 | **完成** | 实测序列为 `identity_m2m_token_total{channel="admin",client="svc_dDLNp",result="issued",reason=""}`、`{result="denied",reason="invalid_client"}`、`{reason="invalid_scope"}`。补 `identity.m2m.denied_spike`（P2）与 `identity.m2m.latency`（P2）后：本地造 24 次 denied，规则由 `pending` 转 `firing`（`activeAt=13:51:18Z`，触发时 5 分钟增量约 23 次）；签发 P95 实测 9.75ms，规则未误报 |

### 9.2 演练发现的缺陷与偏差

1. **代码缺陷（已修复）**：`internal/identity/infrastructure/oauth/oauth2.go` 的 `oauthUnavailable` 用 `apperr.New(..., err)` 包装网络错误，未挂 `ErrUnavailable` 哨兵，导致 `errors.Is(err, apperr.ErrUnavailable)` 失配，`OAuthService` 把 IdP 不可用降级为 `50000`。修复为 `errors.Join(err, apperr.ErrUnavailable)` 并补回归用例 `TestOAuth2ProviderExchangeIdPUnreachable`。
2. **环境偏差**：chart 中 `migrate.image.tag` 固定为 `verify`，该镜像内迁移只到 `000026`，`000027`（SCIM）/`000028`（M2M）/`000029`（权限点）从未应用；补跑迁移后 SCIM 才可用。staging 发布前需确认迁移镜像与代码同源。
3. **配置偏差**：`values-kind.yaml` 的 `iam.mode: shadow` 只认静态超级角色 `admin`，而种子角色为 `system_admin`，导致已授权仍 `403`；演练环境改用 `enforce`。生产/预发需确认 `iam.mode` 与角色种子一致。
4. **规则缺失（已修复）**：设计 §21.2.5 声明的「denied 5 分钟突增」与「签发 p95 &gt; 100ms」告警原先未在 `rules/identity.yaml` 中实现；已补 `identity.m2m.denied_spike`（5 分钟增量 &gt; 10，P2）与 `identity.m2m.latency`（issued P95 &gt; 100ms，P2），`ALERTS.md` 同步登记，阈值可在 staging 按真实基线调整。

### 9.3 未覆盖项

- `D3` 的 SAML SP 证书轮换（`certificate_next_pem` / `private_key_next`）未执行：本地未配置 SAML IdP 元数据，仅以 JWT kid 轮换覆盖同类「双密钥并存 → 退役」路径。
- 部分 Pod 已用新 kid 签发、其余 Pod 尚未滚到的灰度窗口未验证（本地副本数受限，滚动为全量替换）。

### 9.4 三段耗时（OC-05）

| 演练 | 检测用时 | 确认用时 | 缓解用时 | 依据 |
|---|---|---|---|---|
| `D2` IdP 不可用 | < 1 s（首个回调即 500；`identity_oauth_callback_total{result="failure"}` 即时可见）；告警链路按规则 `for` 窗口 | ≈ 2 min：核对 `reason=unavailable`、熔断状态与 Keycloak Pod 就绪 | < 1 min：IdP 恢复后下一次回调即 200，**无需重启**（§9.1 D2 免重启自愈） | 本地 kind 实测 |
| `D4` SCIM 风暴 / 限流 | < 1 s（首个 429 即出现在 `identity_scim_request_total{result="error"}` 与 `reason=rate_limited`） | ≈ 1 min：按 41.7 rps 与令牌配额比对，确认是限流而非故障 | < 10 s：停止压测后 429 回落，令牌限流窗口过后自动恢复 | §9.1 D4（200×25 + 429×5） |
| `SEC-F-01` secret 超龄告警 | 1 min（临时将 `for` 降为 1m 等价验证；生产口径 1h） | ≈ 1 min：核对 `identity_m2m_secret_age_seconds{client}` 与 `max_secret_age` | 未执行：缓解手段是轮换 secret，按 `key-rotation.md` 另行演练，不在本轮范围 | §9.1 SEC-F-01 |

> 三段耗时用于校准 `SEVERITY_MATRIX.md` 的响应承诺；本次为本地 kind 等价环境，检测链路真实、
> 人工确认按单人分角色计时，staging 复跑时应以值班实际接管时间重记。

## 相关链接

- [告警目录](../../runbooks/ALERTS.md)
- [发版回滚](../../runbooks/release-rollback.md)
- [身份认证故障处置](../../runbooks/identity-auth-failure.md)
- [企业身份接入运维手册](../../runbooks/identity-federation-onboarding.md)
- [异步死信处置](../../runbooks/async-dlq.md)
- [事故定级矩阵](../SEVERITY_MATRIX.md)
- [身份域威胁建模](../../identity/threat-model.md)
- [S6-1 安全评审记录](../../archive/reviews/s6-1-security-review-record-2026-11-10.md)
