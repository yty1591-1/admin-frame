# ENT-11 特性开关与灰度（Feature Flag）领域规划

> **状态**：规划 v1.0（待评审）
> **关联**：[`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md)（ENT-11）、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §17.3（feature flag 硬约束）、[`production-readiness-gaps.md`](./production-readiness-gaps.md)（缺口 #ENT-11）、[`rbac-iam.md`](./rbac-iam.md)（权限点与快照）、[`docs/audit/EVENTS.md`](./audit/EVENTS.md)（审计登记）、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)
> **规划日期**：2026-11-04
> **目标读者**：后端 / 平台装配 / 安全 Review / SRE

---

## 0. 结论摘要（TL;DR）

1. **定位**：`internal/featureflag` 是**运行时功能开关与灰度评估服务**——flag 注册、规则匹配、百分比灰度、kill switch、到期治理；评估为进程内纯计算，**不引入任何外部 flag 平台**（LaunchDarkly / Unleash / Nacos，零新增依赖，对齐 ENT-07 Vault 的落地逻辑）。
2. **与静态配置的边界（铁律）**：`configs/*.yaml` 管**进程级部署形态**（`iam.mode` / `async.enabled` / `db.driver`，变更需重启、全集群一致）；feature flag 管**业务功能级**（秒级、按渠道 / 租户 / 百分比差异化）。禁止用 flag 替代部署配置，也禁止用重启变更替代灰度放量。
3. **评估语义**：`IsEnabled(ctx, key, EvalContext{Channel, TenantID, SubjectID}) bool`——kill switch（`enabled=false`）无条件优先；其后规则按序匹配（渠道 → 租户集合 → 百分比），首个命中生效；均未命中回退默认值。**只做布尔开关，不做多 variant / A-B 实验**（明确不做）。
4. **百分比灰度**：按 `(flag_key, subject_id)` 稳定哈希分桶（SHA-256 取模 10000），同主体在配比不变时**命中结果稳定**（不闪断）；上调配比只增不减（分桶单调性由哈希空间天然保证）。
5. **秒级生效**：进程内 TTL 缓存（kill switch 面 5s、规则面 30s）；**不做**跨副本推送失效（复杂度不值——判据是"秒级开关"，5s 已满足；需要更强时效的 kill 场景用部署配置回滚）。
6. **到期治理（对齐 §17.3「无长期遗留 flag」）**：flag 必须带 `expires_at` 或显式标记 `permanent`（仅限常驻 kill-switch 类）；到期由 worker 任务自动禁用并写元审计；`check-featureflag.sh` 门禁扫描代码中的幽灵 key 与无到期 flag。
7. **任务拆解**：FF-01～FF-08 共 8 个任务包；首个真实调用方 = 租户邀请邮件灰度开关（ENT-10 已有功能按租户放量），端到端锚点。

---

## 1. 现状与差距

### 1.1 已就绪资产（本域复用，不重建）

| 资产 | 位置 | 本域用法 |
|------|------|----------|
| 权限点与目录 | `internal/iam`（perm.go / catalog / 权限种子迁移范式） | `featureflag:read` / `featureflag:write` 权限点四处同步（perm / catalog / seed / PERMISSIONS.md） |
| 数据范围原语 | `internal/shared/authz`（ENT-12） | flag 管理为平台级资源，不需要租户 scope；规则中租户集合校验用 `domain` 层自验 |
| 审计出口 | `internal/shared/audit`（Outbox 优先 + 双出口回退） | `featureflag.*` 变更审计，事务内 fail closed（对齐 tenant 域 emit 模式） |
| 后台任务注册 | `platform.Options.Background`（worker / 注册阶段） | 到期自动禁用任务（对齐 audit.retention_purge 模式） |
| 观测范式 | `internal/*/observability`（prometheus 指标族、低基数标签） | `featureflag_*` 指标族 |
| 工程范式 | `internal/tenant` 五层 + CI 门禁 + 集成测试基线 | 直接对齐 |

### 1.2 缺口（本域要交付的）

| # | 缺口 | 现状证据 |
|---|------|----------|
| 1 | 无运行时 flag 服务 | `internal/` 无 featureflag 包；`production-readiness-gaps.md` #ENT-11 |
| 2 | 功能级灰度只能靠发版 | 高风险逻辑回退 = 回滚镜像（分钟级），不满足「秒级开关」判据 |
| 3 | 无灰度放量载体 | `ARCHITECTURE.md` §17.3 要求 channel / merchant_id / 百分比灰度，全仓无实现 |
| 4 | 无 flag 生命周期治理 | 无到期清理、无遗留 flag 台账与门禁 |
| 5 | 首个真实调用方缺位 | ENT-10 邀请邮件为全量硬编码启用，无法按租户放量 |

### 1.3 依赖与边界

- **依赖 ENT-01**（已完成）：权限点与鉴权链路就绪。
- **复用 ENT-12**：authz 原语已就绪；flag 管理是平台级操作，走 `RequirePermission` 权限点即可，不涉租户数据范围。
- **明确不做**：多 variant / A-B 实验平台、前端 SDK 与边缘评估、外部配置中心对接、实时推送失效（pub/sub）、按请求级动态 targeting 表达式语言、flag 评估结果落库（走指标）。

---

## 2. 领域能力清单

### A. Flag 域模型

- `key`：`<module>.<feature>` 小写点分（如 `tenant.invitation_email`）；正则锁定，门禁对账。
- `enabled`：**kill switch**——false 时无条件禁用，优先级高于一切规则（「关闭即回退」的落点）。
- `default_enabled`：规则全部未命中时的回退值（默认 false——flag 必须**默认关闭**，§17.3）。
- `rules`：有序规则数组，首个命中生效；单 flag 上限 32 条（防御配置爆炸）。
- `version`：乐观锁；变更 `version+1`，并发写冲突返回 409。
- `expires_at` / `permanent`：到期治理二选一；`permanent=true` 仅限常驻 kill-switch 类并须在描述中说明理由。
- 状态机（`enabled` × `expired`）：`enabled` / `disabled` / `expired`（到期任务置入，等价 disabled 但留痕可辨）；快照测试锁定迁移白名单。

### B. 规则与评估

- 规则三维度（对齐 §17.3）：`channel`（A/B/C）、`tenant_ids`（显式集合）、`percentage`（0–10000，万分比精度）。
- 评估顺序：kill switch → 规则按序（channel → tenant_ids → percentage）→ `default_enabled`。
- 百分比哈希：`SHA-256(flag_key + ":" + subject_id)` 取模 10000 与阈值比较；subject_id 为空时百分比规则不命中（fail closed，不落随机数——评估必须可复现）。
- 规则校验：percentage 越界、tenant_ids 含空串、channel 枚举外 → 拒绝写入（40000）。

### C. 评估端口与缓存

- `domain.Evaluator` 端口：`IsEnabled(ctx, key, EvalContext) (bool, error)`；**消费方定义接口**（对齐 tenant `InvitationNotifier` 先例），业务域不 import featureflag 包内部。
- 两级缓存：kill switch 面 TTL 5s（判据「秒级开关」）、规则+默认值面 TTL 30s；flag 变更后最迟 TTL 生效，管理端响应中标注 `effective_within`。
- 缓存实现：进程内 `sync.Map` + 定时惰性过期；不跨副本失效。

### D. 管理用例（A 端）

- CRUD + 启停 + 删除：全走审计（`featureflag.create / update / enable / disable / delete / expire`）；变更审计记录 `attrs.fields` 与前后值摘要。
- 幂等：创建同 key 返回 40900 冲突（uk 兜底）。
- 数据范围：平台级资源，不接 `enforceDataScope`（ENT-12 语义不适用于 flag）；越权面由 `featureflag:write` 权限点承担。

### E. 到期治理

- worker 周期任务（对齐 `audit.retention_purge`）：`expires_at < now` 且仍 enabled 的 flag 置 `expired`，写 `featureflag.expire` 元审计（actor_type=system）。
- 遗留台账：管理端列表按「永久 flag」「30 天内到期」「已过期」分组；`check-featureflag.sh` 断言非 permanent flag 必须带 expires_at。

### F. 多租户与安全

- 评估面向全部端（B 端业务 app 同样按租户灰度）；管理面仅 A 端（权限点分离）。
- B 端**不提供**任何 flag 枚举 / 查询端点（不泄露功能面）。
- 规则中 tenant_ids 为显式白名单集合——不接受通配符（防误操作全量放开；全量放开 = percentage 10000 或 default_enabled）。

### G. 可观测性与运维

- 指标：`featureflag_eval_total{flag, result}`（评估量与命中分布）、`featureflag_expired_total`（到期任务）、`featureflag_cache_hit_ratio`（缓存健康）。全部标签低基数（flag key 是有限集合，门禁守护）。
- runbook：`docs/runbooks/feature-flag.md`（kill switch 应急、缓存时效说明、评估异常排查）。
- 门禁 `check-featureflag.sh` 入 make ci：①key 命名正则 ②审计动作 ↔ EVENTS.md 对账 ③幽灵 key 扫描（代码字面量必须来自登记的 keys 常量）④非 permanent 必带 expires_at。

### H. 业务域集成契约

- 业务域定义消费端口（如 `tenant/app` 定义 `FeatureEvaluator`），runtime 装配时注入 featureflag 实现；未装配时注入 noop（全走 default_enabled=false 的静态语义，保持最小部署形态可运行）。
- flag key 常量登记在各域 `keys.go`（如 `internal/tenant/app/flagkeys.go`），门禁扫描字面量。

---

## 3. 数据模型（迁移 000020 起）

| 表 | 关键列 | 说明 |
|----|--------|------|
| `feature_flag` | key（uk）、description、enabled、default_enabled、rules_json、version、permanent、expires_at、created_by、created_at / updated_at | 单表；rules_json 含有序规则数组；无删除物理行（软删= disabled + 审计留痕，删除动作仅允许对已禁用 flag 执行且为物理删除） |

- 全部含 `created_at / updated_at`；`version` 乐观锁（CAS 更新）。
- 迁移遵循 `check-migrations.sh` 门禁（幂等回填不需要——全新表）。
- 评估结果不落库（走指标）；不建评估日志表。

## 4. 代码布局（对齐 tenant / notification 五层范式）

```text
internal/featureflag/
├── domain/         # Flag、Rule、EvalContext、状态机 Transitions()、key 正则、错误、Evaluator 与仓储接口
├── app/            # FlagService（管理 + 审计）、EvaluationService（评估 + 缓存）、ExpireService（到期任务）、Observer 端口
├── infrastructure/ # MySQL 持久化、稳定哈希（hash.go）、内存缓存
├── observability/  # metrics.go（featureflag_* 指标族）
└── transport/admin # A 端 CRUD / 启停 / 删除 / 遗留台账
cmd/internal/featureflagruntime/runtime.go   # 装配（对齐 notificationruntime 模式）
```

- 分层：`app` 只依赖 `domain` + 端口；哈希与缓存细节在 `infrastructure`（check-layering）。
- 消费方（如 tenant）通过自定义端口接口消费，runtime 注入——业务域不 import featureflag 内部（规划 5.1.3：「不得读取业务域内部状态」，同理不反向耦合）。

## 5. 任务拆解（FF-01 ～ FF-08）

| ID | 任务包 | 关键产出 | 验收要点 |
|----|--------|----------|----------|
| FF-01 | 域模型与迁移 | 迁移 000020（feature_flag 表）；domain 层模型 + 状态机 + Transitions() 快照测试 + memory 仓储 + key 正则 | 状态机矩阵测试；check-migrations 过 |
| FF-02 | 规则评估引擎 | 规则匹配（渠道/租户集合/万分比）、SHA-256 稳定哈希分桶、评估顺序与 fail closed 语义 | 稳定命中单测（同主体同结果）；分桶单调性（配比上调只增不减）属性测试 |
| FF-03 | 管理用例与到期任务 | FlagService（CRUD + 启停 + 审计 + 版本 CAS）；ExpireService（worker 周期任务 + 元审计） | 并发写 409；变更全入审计；到期自动禁用可测 |
| FF-04 | 评估端口与缓存 | Evaluator 端口 + 两级 TTL 缓存（5s/30s）+ noop 实现 + Observer 指标埋点 | 缓存命中指标；kill switch 5s 内生效（单测时钟注入验证） |
| FF-05 | API 面 | admin.yaml 8.1.0：CRUD / 启停 / 删除 / 遗留台账；权限点 `featureflag:read/write` 四处同步（perm/catalog/seed 000021/PERMISSIONS.md） | check-openapi 双向对账；iam/catalog 门禁过；B 端无任何 flag 端点 |
| FF-06 | 可观测性与门禁 | featureflag_* 指标族；runbook；check-featureflag.sh 入 make ci（key 正则 / EVENTS 对账 / 幽灵 key / 到期断言） | 指标标签低基数；门禁四项断言全绿 |
| FF-07 | 首批调用方接入 | tenant 邀请邮件灰度开关（`tenant.invitation_email`）：tenant app 定义消费端口 + runtime 注入 + 默认 noop 语义 | 端到端：flag 禁用 = 邀请不发送（落库照旧）；按租户灰度放量可验证 |
| FF-08 | ADR 与收尾 | ADR-0009（进程内评估 + TTL 缓存 vs 外部 flag 平台）；规划文档状态更新；全仓验证 | 全仓 CI 绿；app 覆盖率对齐 tenant 基线（≥80%） |

**顺序与依赖**：FF-01 → 02/03（可并行）→ 04 → 05/06 → 07 → 08。FF-07 是端到端验收锚点。

## 6. 整体验收标准（ENT-11 关闭条件）

- [ ] **秒级开关**：kill switch 生效延迟 ≤ 5s（TTL 上限），评估指标可观测（单测时钟注入 + 集成验证）。
- [ ] **关闭即回退**：`enabled=false` 无条件优先于一切规则（属性测试 + 快照）。
- [ ] **灰度正确**：百分比同主体稳定命中；配比上调只增不减；subject 缺失不命中（fail closed）。
- [ ] **审计完整**：create/update/enable/disable/delete/expire 六动作全部入审计且对账 EVENTS.md。
- [ ] **无长期遗留 flag**：非 permanent 必带 expires_at；到期任务自动禁用；门禁扫描幽灵 key。
- [ ] **默认关闭**：新建 flag `default_enabled=false` 且规则为空（防御性默认，API 层拒绝 default_enabled=true 的裸创建）。
- [ ] **调用方解耦**：消费方通过自定义端口 + noop 注入；业务域不 import featureflag 内部（check-layering）。

## 7. 风险与开放问题

| # | 风险/问题 | 建议默认 |
|---|-----------|----------|
| 1 | 评估路径性能（每请求多次评估） | 两级缓存 + 指标观测；规范上只允许在高风险/灰度路径使用，禁止热路径无缓存直查 |
| 2 | TTL 30s 是否满足「秒级」判据 | kill switch 单独 5s 面；更强的时效需求（安全热修）走部署回滚，不放大 flag 复杂度 |
| 3 | 百分比上调时主体「掉出」体验 | 分桶哈希保证配比上调只增不减；下调/清零属主动回滚，闪断可接受并在 runbook 说明 |
| 4 | flag key 滥用（自由字符串散落） | key 常量登记 + 门禁幽灵 key 扫描（FF-06）；正则锁定命名 |
| 5 | 与静态配置边界混淆 | 本文 §0.2 决策表 + ADR-0009；评审时逐个新增 flag 核对归属 |
| 6 | 永久 flag 滋生 | permanent 需描述中写明理由；台账分组暴露；门禁只放行 kill-switch 类 |
| 7 | 多环境一致性（staging/prod flag 状态漂移） | flag 数据属 DB 状态不随 values 渲染；发布 checklist 增加「新 flag 初始状态核对」项 |

---

## 附：静态配置 vs Feature Flag 决策表

| 场景 | 归属 | 理由 |
|------|------|------|
| `iam.mode` / `async.enabled` / `db.driver` | 静态配置 | 进程装配形态，变更需重启且全集群一致 |
| 渠道驱动选型（smtp vs memory） | 静态配置 | 装配期决策（ADR-0008） |
| 邀请邮件按租户放量 | Feature flag | 租户级差异、秒级回退 |
| 新结算逻辑试点 | Feature flag | 百分比灰度、kill switch |
| 依赖韧性参数（超时/重试/熔断阈值） | 静态配置（ENT-13 接管） | 数值型调参不是布尔开关，且需可配置审计而非 flag 规则 |
| 紧急全量止血 | 部署回滚 / flag kill switch（若该功能已有 flag） | 无 flag 的新功能止血 = 回滚镜像；这正是「高风险逻辑默认带 flag」的动机 |
