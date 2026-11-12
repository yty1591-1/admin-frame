# ADR-0014：业务密钥由配置中心托管并采用信封加密，KEK 外部托管

- 状态：Accepted（已接受，2026-11-12 评审通过）
- 日期：2026-11-12
- 关联：[`../config-center-plan.md`](../config-center-plan.md)（规划与评审 §1.3 / §3.2 / §5.5 / §6.12）、[ADR-0013](0013-config-center-in-process-snapshot.md)（配置中心架构）、[`../ARCHITECTURE.md`](../ARCHITECTURE.md) §4.4 / §5.6、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md)（CFG-01 / CFG-02 / CFG-04）、[`../SECURITY_STANDARDS.md`](../SECURITY_STANDARDS.md)（SEC-01～SEC-16）、[`../AUDIT_STANDARDS.md`](../AUDIT_STANDARDS.md)

## 背景

配置中心 v0.2 的范围从「只接管动态非启动配置」扩大为：

1. 删除 `./configs` 下全部运行时静态配置，由配置中心接管；
2. 业务密钥（第三方 API Key、SMTP / 短信凭据、Webhook 签名密钥、业务加密 / 签名密钥等）也由配置中心托管。

密钥与普通配置的生命周期不同：需要轮换、吊销、最小权限、访问审计与泄露止血。若继续沿用 v0.1「只存 `secret_ref`、明文交给 Vault」的方案，等于密钥仍由外部平台托管，与用户「配置中心接管密钥」的要求不一致；若把密钥明文直接写入 `config_item_version`，则数据库、备份、慢查询、日志任一泄露都会导致全量失守。

同时存在两个硬边界：

- 配置中心自身启动所需的 DB / Redis 凭据、配置中心地址、CA 等不能由配置中心自己提供，否则形成自举死锁；
- 根密钥 / KEK 若与业务密文同库，信封加密失去意义。

**2026-11-12 范围补充（v0.3）**：用户明确要求不做引导配置清单与配置中心导入清单，CFG-04 只保留「配置中心 > 代码默认值」，首次启动使用默认值，密钥按字段所需规则随机生成。因此自举方式改为：配置中心自身连接参数使用代码默认值承载，空库首次启动按代码 key spec 播种默认值，并对声明了 `secret_rule` 的密钥用 `crypto/rand` 自动生成后信封加密入库；无生成规则的第三方密钥必须人工录入。

本 ADR 回答：业务密钥如何存、如何解密、如何轮换与审计，以及哪些凭据**不得**进入配置中心。

## 决策

1. **只托管业务密钥**。范围包括：第三方 API Key、SMTP / 短信凭据、Webhook 签名密钥、业务数据加密 / 签名密钥。以下**永不进入配置中心**：
   - 根密钥 / KEK；
   - 配置中心自身 DB / Redis 引导凭据；
   - 业务进程 DB / Redis 口令等基础设施口令（基础设施注入，不入配置中心）；
   - K8s 集群凭据、云账号根密钥、SSH 私钥、人员口令。
2. **信封加密，密文入库**。每个密钥版本生成独立 DEK（32 bytes，`crypto/rand`），用 AES-256-GCM 加密明文；DEK 由外部 KEK（KMS / Vault Transit / HSM）wrap 后与密文同存；KEK 永不入库。密文格式见下文。
3. **AAD 绑定上下文**。`AAD = env + ":" + namespace + ":" + name + ":" + version`；解密时 AAD 不匹配即失败，防止密文跨环境 / 跨密钥 / 跨版本搬运。
4. **密钥解析收敛到独立 `cmd/config-api`**。业务密钥的解密只在 `config-api` 内发生；`admin-api` 仅在创建 / 轮换时执行 wrap；业务进程不持有 KEK 访问凭据。建议 KMS 权限按 wrap / unwrap 拆分并分别绑定 ServiceAccount。业务进程通过 mTLS / SPIFFE 调用 `/internal/v1/configs/secrets/resolve`，由服务端完成授权校验与解密。理由：
   - 业务进程不接触 KEK，避免 KEK 访问面扩散到全部实例；
   - 不通过 HTTP 调用 `admin-api`，符合 ARCHITECTURE §4.4；`config-api` 是独立服务，按该条「拆分服务 + 消费侧接口 + ADR」路径落地；
   - 可独立限流、独立 NetworkPolicy、独立证书与独立审计。
5. **服务授权默认拒绝**。`config_secret_grant` 以 `env + namespace + service_identity + secret` 四元组授权，`permission=resolve`；禁止通配符；人不可调用解析端点；grant 变更强制审计。
6. **短租约，不落盘**。解析返回 `lease_expires_at`，默认 5 分钟、上限 15 分钟；高风险密钥可配置 60 秒或 `no-cache`。客户端只做内存缓存，进程退出即失效；不支持跨进程共享缓存或全量导出。
7. **生命周期与双人审批**。密钥创建、轮换、吊销、授权、回滚激活均需双人审批（提交人 ≠ 审批人）；轮换保留旧版本 grace（默认 24 小时，可配 ≤ 7 天）；`dual_version=1` 的验签类密钥在解析时同时返回 active 与 grace 版本，保证轮换期旧签名可验证；吊销后旧租约 ≤ TTL 失效；历史版本不可变。
8. **明文旁路全面禁止**。列表 / 详情 / 导出 / 日志 / 指标 / 追踪 / 错误体 / 前端响应 / 签名快照均不得出现明文；导出只允许元数据；排障不得提供 `curl` 式明文接口。
9. **密钥独立于配置 Release**。密钥版本不写入 `config_release_item` 与签名快照；配置只能按密钥名称引用。需要「配置 + 密钥」联动时由发布编排显式排序，并在 runbook 说明「配置回滚 ≠ 密钥回滚」。
10. **KEK 轮换与备份分离**。KMS / Vault 支持 `key_id` 多版本；后台任务逐条 `unwrap(old) → wrap(new)` 重包裹 DEK；密文随 MySQL 备份，KEK 由 KMS / Vault 备份，两者分离存放；恢复演练至少一次。
11. **代码默认值与首次启动初始化**。配置中心自身的地址、环境、服务身份、CA、DB / Redis 连接参数走代码默认值（部署编排承载），自身口令与 KEK 访问凭据不入配置库；空库首次启动按代码 key spec 默认值播种基线 Release，不维护引导配置清单与导入清单。初始化幂等、不覆盖已有值。
12. **字段密钥生成规则**。密钥字段必须声明 `secret_rule`：`min_len` / `max_len` / `encoding`（base64url / hex）/ `algorithm` / `prefix` / `auto_generate`。首次启动仅对 `auto_generate=true` 的字段用 `crypto/rand` 生成，长度与编码必须满足规则；生成即版本 1、加密入库并审计。无生成规则或 `auto_generate=false` 的字段必须人工录入，`required` 且缺失时 fail-fast；生成值永不回显、不落日志，只返回指纹。

## 密文格式

`config_secret_version` 关键列：

```text
algorithm   = "aes-256-gcm"
nonce       = 12 bytes（随机）
ciphertext  = AES-256-GCM(DEK, plaintext, AAD)
AAD         = env + ":" + namespace + ":" + name + ":" + version
wrapped_dek = KEK.wrap(DEK)
kek_key_id  = KEK 版本标识（支持轮换；不返回业务）
value_hash  = SHA-256(plaintext)[:8]（指纹，仅用于比对，不可逆）
```

**解析流程**：

```text
Service → mTLS / SPIFFE → config-api
  ├─ 校验服务身份 + grant
  ├─ 读取 config_secret（status=active）与 active_version
  ├─ 读取 config_secret_version（status=active|grace）
  ├─ DEK 缓存命中？否 → KMS.unwrap(wrapped_dek, kek_key_id) → 缓存 DEK
  ├─ AES-256-GCM 解密（AAD 必须匹配）→ 校验 value_hash
  ├─ 生成租约（默认 5m，上限 15m）
  └─ 返回 {name, version, value, lease_expires_at}；审计 config.secret.resolved
```

## 备选方案

| 方案 | 不选理由 |
|------|----------|
| 密钥明文写入 `config_item_version` | 库 / 备份 / 慢查询 / 日志任一泄露即全量失守；无法满足最小权限与审计 |
| 只存 `secret_ref`，继续由 Vault 托管 | 与用户「配置中心接管业务密钥」的范围不一致；密钥生命周期仍割裂在两套系统 |
| 业务进程直连 KMS / Vault 解密 | KEK 访问面扩散到全部实例；无法统一授信、限流与审计；泄露影响面大 |
| 把 KEK 与密文同库存放 | 信封加密失效；库泄露即可解密全部业务密钥 |
| K8s Secret 作为业务密钥存储 | 无版本 / 审批 / 轮换 / 细粒度授权 / 访问审计；只适合基础设施口令 |
| 云厂商 Secrets Manager | 厂商锁定、跨云不一致；控制台 / 审批与本项目 IAM 割裂；用户要求自研接管 |
| 独立密钥管理微服务（MVP） | 新增服务与运维面；当前规模可由 `config-api` 单工作负载承担，达到拆分条件再评估 |

## 后果

**正面**：

- 业务密钥纳入配置中心统一生命周期：创建、审批、轮换、吊销、授权、审计、到期提醒。
- 数据库泄露不等于明文泄露：需要同时攻破 MySQL 与 KMS / Vault / HSM。
- 业务进程不接触 KEK；密钥访问收敛到单一工作负载，可独立限流、隔离与审计。
- 轮换与吊销可快速止血；旧版本 grace 保证业务不中断。
- 与现有 IAM、审计、Outbox、Helm、OTel 体系一致。

**负面 / 约束**：

- 新增 `cmd/config-api` 工作负载、mTLS / SPIFFE 证书、KMS / Vault 依赖与密钥 runbook。
- 密钥解析引入网络依赖：`config-api` 或 KMS 故障时，缓存未命中的密钥不可用；必须按 `required` 分级降级。
- 轮换需要调用方支持双版本 / 优雅刷新；不支持动态密钥的第三方系统仍需人工窗口。
- 密钥审计量增加；需控制日志字段与保留成本。
- 无法防住「超级管理员 + KMS 权限」串通；需组织控制、双人审批与 key ceremony。

**守卫**：

- `check-configcenter.sh`：密钥端点权限、密文列、明文扫描、审计事件、OpenAPI、迁移成对。
- 单元 / 集成测试必须断言：无 KEK 无法解密、AAD 不匹配失败、列表 / 详情 / 导出 / 日志 / 快照无明文、无 grant 解析被拒绝；空库首次启动密钥按规则生成且只生成一次；无规则 `required` 字段缺失 fail-fast。
- 安全评审：KMS 权限最小化、`config-api` 网络策略、证书轮换、密钥泄露 runbook。
- SLO：密钥解析可用性 ≥ 99.95%；P99 < 50ms（稳态）；轮换生效 ≤ 2 × TTL。
- 密钥泄露 / 误删 / 轮换失败均登记安全事件，按 `docs/incident/` 流程复盘。

## 后续行动

1. ~~本 ADR 转「已接受」前，完成 `docs/config-center-plan.md` §10.2 的 `Q9`～`Q12` 与安全会签。~~ 已完成（2026-11-12）：ADR 转「已接受」，Q9 / Q12 已决，Q10 选型在 M2 实施前关闭。
2. ~~回写 `SECURITY_STANDARDS.md`（密钥明文不入库、信封加密、KEK 外部）与 `CONFIG_STANDARDS.md`（键分类、业务密钥边界、CFG-04 仅「配置中心 > 代码默认值」、首次启动默认值与密钥生成规则）。~~ 已完成（2026-11-12）。
3. 新增 `docs/runbooks/secret-management.md` 与 `docs/runbooks/key-ceremony.md`。
4. M2 前完成 KEK 选型（Vault Transit / 云 KMS / HSM）与 `config-api` mTLS / SPIFFE 证书方案。
5. M2 完成后复评是否需要把普通配置读也收敛到 `config-api`。
