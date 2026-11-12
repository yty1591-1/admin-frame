# ADR-0013：配置中心采用进程内快照与 MySQL 事实源，不引入外部配置平台

- 状态：Accepted（已接受，2026-11-12 评审通过）
- 日期：2026-11-12
- 关联：[`../config-center-plan.md`](../config-center-plan.md)（规划与评审）、[ADR-0009](0009-featureflag-inprocess-evaluation.md)（进程内评估先例）、[ADR-0014](0014-secret-management-envelope-encryption.md)（业务密钥托管）、[`../ARCHITECTURE.md`](../ARCHITECTURE.md) §4.4 / §5.6 / §17、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md)（CFG-01 / CFG-02 / CFG-04 / CFG-07）

## 背景

项目当前只有「文件 + 环境变量 + Vault」的静态配置：任何运行参数调整都需要改配置并滚动发布，无法满足以下生产诉求：

- 运行时限流阈值、分页上限、文案、白名单、通知重试次数等参数需要热更新；
- 变更需要版本、审批、灰度、回滚与审计，而不是直接改文件；
- 多实例需要收敛到同一份配置，且中心故障时业务不能停；
- 不能为了配置管理引入新的有状态平台与额外运维面。

**2026-11-12 范围变更（v0.2 / v0.3）**：用户明确要求「自研配置中心后完全舍弃 `./configs` 下所有静态配置，全部由配置中心接管；业务密钥也由配置中心接管，但必须做好密钥管理」，并进一步要求「CFG-04 只有配置中心 > 代码默认值，不做引导配置清单与配置中心导入清单，首次启动使用默认值以及密钥按字段规则随机生成」。本 ADR 随之调整：

- 从「只接管运行时可热更的非启动配置」改为「**全量接管 `configs/**` 运行时静态配置**」，配置优先级只保留「配置中心 > 代码默认值」，**不保留引导配置清单与导入清单**；
- 首次启动空库时按代码 key spec 默认值播种基线 Release；声明了 `secret_rule` 的密钥用 `crypto/rand` 按规则生成并加密入库；
- 从「密钥只存 `secret_ref`」改为「**业务密钥密文托管 + 外部 KEK 信封加密**」，由 ADR-0014 约束；
- 离线兜底从无签名本地快照改为 **Ed25519 签名快照**；
- 新增 `cmd/config-api`（M2）承担密钥解析，普通配置读仍直连 MySQL。

`ARCHITECTURE.md` §5.6 要求热更新前定义白名单并补 ADR；CFG-04 要求配置中心若引入须明确优先级。本 ADR 负责回答：自研还是引入 Nacos / Apollo / Consul / etcd，读路径如何保证可用性，以及删除静态文件后的引导与兜底。

## 决策

1. **自研模块，不引入外部配置平台**：新增 `internal/configcenter` 域，管理面挂 `admin-api`；M1 不新增独立可执行文件。M2 因业务密钥解析新增 `cmd/config-api`（仅内部解析，不承载管理面），理由见 ADR-0014；普通配置读仍直连 MySQL，达到拆分触发条件（管理 QPS 独立增长、多区域、客户端 > 500 实例、多语言接入、团队独立）再评估是否统一收敛。
2. **MySQL 为唯一事实源**：新增 `config_item` / `config_item_version` / `config_release` / `config_release_item` / `config_release_pointer` / `config_release_review`，以及 M2 的 `config_secret` / `config_secret_version` / `config_secret_grant` / `config_secret_review`；复用 `audit_event` 与 `async_outbox`，不建第二套审计 / 事件表。
3. **发布模型为不可变快照 + 指针 CAS**：一个 Release 是一组配置项版本的不可变快照；发布在单事务内更新 Release 状态、`config_release_pointer`（CAS）、Outbox 与审计。客户端永不读取半发布状态；回滚 = 以历史快照创建新 Release，不修改历史。**密钥版本不进入 Release 快照**，只按名称引用。
4. **客户端进程内读取 + 版本轮询 + 本地签名快照**：`pkg/configclient` 提供缓存、类型化读取、Watch 与签名快照落盘；`internal/configcenter/infrastructure/client` 提供 MySQL 只读源。默认 5s 轮询（1s～60s 可配，±20% 抖动），业务进程以只读账号直连 MySQL，**不通过 HTTP 调用 admin-api**（遵守 ARCHITECTURE §4.4）。中心 / DB 故障时继续使用本地签名快照；首次启动无快照时使用初始化生成的基线 Release 或代码默认值；无有效快照且 `required` 键缺失才拒绝启动。
5. **配置优先级只有「配置中心 > 代码默认值」**：删除环境变量与文件配置层，M1 起生效。首次启动空库时按代码 key spec 默认值播种基线 Release；声明了 `secret_rule` 的密钥用 `crypto/rand` 按长度 / 编码 / 算法规则生成并信封加密入库；初始化幂等、不覆盖已有值，**不产出引导配置清单与配置中心导入清单**。配置中心自身地址、环境、服务身份、CA 与 DB / Redis 连接参数使用代码默认值承载；自身口令与 KEK 访问凭据不入配置库。启动期配置（`addr`、`db.host/port/name/user`、`redis.addr`、`storage.driver`、`auth.issuer/audience/mode`、`async.*`、`iam.mode`、`http.*`）由配置中心提供但只在启动期读取，变更需重启；DB / Redis 口令由基础设施注入，不属于业务配置层。
6. **灰度只做白名单选择器**：百分比 / 租户 / 渠道 / 实例；使用 `release_id + salt + target_id` 稳定哈希，同一目标稳定命中；不做表达式、ABAC、A/B 实验。
7. **权限三权分离与审计**：`config:write`（草稿）/ `config:publish`（发布）/ `config:approve`（审批）；密钥另有 `config:secret:read/write/approve` 与服务身份 `config:secret:resolve`；生产默认双人复核，密钥操作强制双人；紧急变更走 break-glass 并强制事后审计。
8. **与 featureflag 分治**：布尔开关、kill switch、规则灰度继续归 `internal/featureflag`（ADR-0009）；配置中心只存结构化值与版本发布，不新增布尔评估语义。
9. **业务密钥信封加密（ADR-0014）**：只托管业务密钥，明文不落库；每版本独立 DEK + AES-256-GCM，KEK 放外部 KMS / Vault / HSM；业务进程不接触 KEK，统一经 `cmd/config-api` 解析并获取短租约。
10. **签名快照作为冷启动与灾备兜底**：Release 快照用 Ed25519 签名，客户端内嵌公钥验签；快照不含密钥明文，不作为事实源；验签失败拒绝装载。

## 备选方案

| 方案 | 不选理由 |
|------|----------|
| Nacos / Apollo | 新增外部平台、HA、数据库、升级、凭据与安全面；控制台 / 灰度 / 审计能力与现有 IAM / 审计体系重叠；与 ADR-0009「能用既有设施满足就不新增组件」冲突 |
| Consul / etcd | 强一致 KV 与 Watch 成熟，但没有版本、审批、审计、灰度模型；仍需自建管控面；运维与容量成本高 |
| Redis 作为事实源 | 持久化与事务弱，故障可能丢配置；无法承载版本 / 审批 / 审计 |
| 纯文件 / ConfigMap | 无热更、无灰度、无审计、无权限；变更仍需发布；用户已要求删除 |
| 独立 config-api 服务（M1 承载全部配置读） | 普通配置读新增网络单点与部署成本；M1 不需要 |
| 独立 config-api 服务（M2 仅密钥解析） | **选中**：密钥解密必须收敛到能访问 KEK 的工作负载；符合 ARCHITECTURE §4.4 的拆分服务路径 |

## 后果

**正面**：

- 零新增有状态组件；复用 MySQL / Redis / Outbox / 审计 / IAM / Helm / OTel。
- 普通配置读路径无新增网络单点：进程内 `Get` P99 < 1ms；快照拉取 P99 < 50ms；中心故障时业务继续运行。
- 删除 `configs` 后仍有首次启动默认值初始化 + 签名快照兜底，不再依赖运行时文件与引导配置清单。
- 发布原子、可回滚、可审计；灰度与审批满足企业级变更治理。
- 业务密钥与普通配置统一治理；密文入库、KEK 外部，库泄露不等于明文泄露。

**负面 / 约束**：

- M2 新增 `config-api` 工作负载与 KMS / Vault 依赖；密钥解析在缓存未命中时依赖该服务。
- 客户端变更收敛为秒级（默认 5s，P99 ≤ 30s），不是毫秒级；需要毫秒级的安全热修仍走部署回滚。
- 每个业务进程需要只读 DB 连接（建议 ≤2 个 / 实例），500 实例以上需启用 Redis 缓存或拆读副本。
- 新增配置域与密钥域运维资产：告警、看板、runbook、key ceremony、演练与值班培训。
- 配置中心无法回滚外部副作用；runbook 必须要求评估「配置回滚 ≠ 业务补偿」。

**守卫**：

- `check-configcenter.sh`：键分类、首次启动默认值初始化、权限点、审计事件、OpenAPI、迁移成对、文档链接。
- `check-no-static-config.sh`：禁止新增 `configs/*.yaml`，防止删除后回退。
- 读路径与密钥解析 SLO 99.95%；发布时效 99% ≤ 10s；错误预算消耗 ≥ 50% 冻结非必要变更。
- 任何「先上线后补审计 / 回滚」的例外须架构 + 安全书面同意，并登记整改项。

## 后续行动

1. ~~本 ADR 转「已接受」前，完成 `docs/config-center-plan.md` §10.2 待决问题与会签；ADR-0014 同步转「已接受」。~~ 已完成（2026-11-12）：ADR-0013 / ADR-0014 均转「已接受」，§1.1 的 8 个前置条件已关闭。
2. ~~回写 `CONFIG_STANDARDS.md` CFG-01 / CFG-02 / CFG-04 与配置中心章节；CFG-04 只保留「配置中心 > 代码默认值」，删除引导 / 导入清单；回写 `ARCHITECTURE.md` §4.4 / §5.6 / §17。~~ 已完成（2026-11-12）。
3. 更新 `.trae/rules/git-commit-message.md` 与 `.cursor/rules` 镜像，新增 `configcenter` scope。
4. 按 `config-center-plan.md` §8.1 的 M0～M3 执行；M1 完成全量配置接管与首次启动初始化，M2 完成业务密钥托管与按规则生成，M3 删除剩余 `configs` 文件并演练。
5. M2 完成后复评普通配置读是否收敛到 `cmd/config-api`。
