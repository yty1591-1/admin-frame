# 数据资产清单与 PII 分类分级（ENT-15）

> **状态**：v1.0（2026-11-04）
> **关联**：[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)（PII 边界）、[`tenant-data-lifecycle.md`](./tenant-data-lifecycle.md)（三段式处置）、[`audit/EVENTS.md`](./audit/EVENTS.md)（保留期声明）、[`adr/0006-tenant-as-isolation-boundary.md`](./adr/0006-tenant-as-isolation-boundary.md)
>
> 定位：登记平台全部持久化数据的 PII 分类、保留期与删除边界——
> 「删除可验证」的前提是「知道有什么、在哪、多久删」。本清单与迁移脚本
> 同步演进：新增含 PII 的表必须同时登记本清单（review 检查项）。

## 分级定义

| 级别 | 定义 | 处置要求 |
|---|---|---|
| **P1 直接标识** | 可独立识别自然人的数据（姓名 / 邮箱 / 手机号 / 证件号 / 登录主体名） | 存储即摘要或加密；导出经审批；删除最彻底（含派生索引） |
| **P2 准标识** | 单独不能识别、组合可识别（IP / UA / 设备指纹 / 登录尝试记录） | 保留期 ≤ 180d；到期优先清理 |
| **P3 业务敏感** | 不标识自然人但属商业敏感（配额 / 角色 / 绑定 / 通知参数摘要） | 随租户生命周期处置；导出按合同 |
| **P0 非敏感** | 系统运行数据（状态机 / 版本 / 审计元数据） | 常规保留期 |

## 各域登记表

### identity（000001）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| identity_account | subject 明文 | — | **不落库**：仅 `subject_index`（HMAC-SHA256 确定性索引）+ `subject_ciphertext`（AES-256-GCM，AAD 绑定 account_id） |
| identity_account | subject_index / subject_ciphertext | P1 | 随租户处置物理删除（含软删行）；平台账号随账号注销流程 |
| identity_credential | 密码哈希 / MFA 密钥 | P1 | 随账号删除 |
| identity_session / session_family | 会话与刷新链 | P2 | 撤销即失效；物理删除随账号；归档表按 90d 清理 |
| identity_mfa / oauth_identity | 绑定关系 | P1 | 随账号删除 |
| identity_login_attempt | IP / UA / 结果 | P2 | ≤ 180d（爆破防护窗口外即清理） |
| identity_audit_event | 身份审计流 | P0 | 180d（审计是唯一不随租户删除的数据） |

### iam（000002 / 000013）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| iam_role / role_permission / role_data_scope | 商户角色与授权 | P3 | 随租户处置删除（ENT-15 IAM 边界，ENT-13 模板） |
| iam_role_binding | subject_id ↔ 角色绑定 | P2 | 随租户处置删除；成员停用保留关系（停用 ≠ 删除） |
| iam_change_request | 审批载荷 | P3 | 随租户处置删除；平台级审批单按 365d |

### tenant（000008 / 000011 / 000014 / 000022）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| tenant | 租户主数据 / 联系人 | P1（联系人）/ P3（其余） | 三段式处置：90d 保留 + 30d 导出窗口 + 硬删除（`tenant.data_purge` 审计） |
| tenant_member | subject_id 成员关系 | P2 | 随租户级联删除 |
| tenant_quota / tenant_invitation | 配额 / 邀请（token 摘要） | P3 / P1（email 摘要） | 随租户级联删除 |
| tenant_export_manifest | 导出证据（SHA-256） | P0 | ≥ 365d（处置证据链，不随租户删除） |

### notification（000016–000019，ENT-10）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| notification_request | 收件人引用（加盐 HMAC 摘要）/ 场景 / 状态 | P2（摘要不可逆）/ P3 | 随通知保留期；租户处置不级联（低敏、独立保留） |
| notification_delivery | message_id / 诊断 | P3 | 同上 |
| notification_template | 模板正文 | P3 | 版本化保留 |
| notification_preference / blacklist | 退订 / 黑名单（无盐 HMAC） | P2 | 保留期独立（防重订阅需要长期记忆） |

### audit（000006 / 000007）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| audit_event | 全域审计流（attrs 经脱敏，pkg/mask） | P0（脱敏后） | 180d / 365d 分级；到期「先归档后删除」（`audit.retention_purge`）；**法务保留开关**（`audit.legal_hold`）置位时只统计不删 |
| audit_export_job | 导出任务记录 | P0 | 随审计保留期 |

### featureflag（000020 / 000021，ENT-11）

| 表 | 数据 | 级别 | 保留 / 删除边界 |
|---|---|---|---|
| feature_flag | 开关与规则（tenant_ids 白名单） | P3 | 物理删除（治理动作，`featureflag.delete` 审计）；到期自动禁用 |

## DSAR（数据主体权利）路径

| 权利 | 路径 | 现状 |
|---|---|---|
| 访问 / 可携带 | 审计导出（`audit/export_job`，带审批与 TTL）+ 租户导出包（manifest 校验） | ✅ 审计导出已实现；租户导出包归 ENT-15 执行（manifest 证据已落库） |
| 删除（被遗忘） | 账号注销流程（identity 软删 → 保留期后物理删）；租户终止三段式 | ✅ 本文档 + purge 任务 |
| 保留期异议 | `tenant.retention.hold_days` 合同延长（必须留审计） | ✅ 配置化 |

## 维护规则

1. 新增含 PII 的表 / 列，PR 必须同步本清单（review 检查项；P1 字段必须给摘要化或加密方案）；
2. 保留期变更（延长）必须写明合同或法规依据；
3. 本清单与 `docs/tenant-data-lifecycle.md` 的边界表冲突时，以更严格者为准。
