# ADR-0007：租户成员 ↔ IAM 角色绑定联动

日期：2026-11-04　状态：已接受
关联：ADR-0006（租户即隔离边界）、ENT-09 T-18（P1-8）、`docs/iam/PERMISSIONS.md`

## 背景

`tenant_member.role`（owner / admin / member）目前只是一个标签：加成员不创建
IAM 绑定，成员在 B 端拿不到任何权限；反过来 IAM 绑定也不校验成员关系，
两套数据完全平行（P1-8）。ENT-09 验收要求「新增成员后主体获得与角色匹配的
最小权限；移除/停用后权限立即失效」——必须选一条联动路径并实现。

## 备选方案

1. **写时同步**：成员变更时同步创建 / 撤销 IAM 绑定，授权判定仍走 IAM 快照；
2. **读时反查**：IAM 解析权限时反查 `tenant_member` 表，把成员角色折算成权限；
3. **双写但不保证一致**：成员变更时尽力同步，失败仅告警（弱一致）。

## 决策

采用**方案 1（写时同步）+ 同库事务复用 + 平台级模板角色**：

### 1. 写时同步而非读时反查

- 反查会把 `tenant_member` 变成 IAM 授权热路径的依赖：每次鉴权多一跳跨域读，
  缓存失效要打通两个域，且「成员表有行但已停用」的边界要在快照层重新处理一遍；
- 写时同步复用 IAM 既有模型——绑定生命周期（撤销 / 过期 / 审计 / 快照失效）
  全部在 IAM 内闭环，租户域只是绑定的一个触发源；
- 联动语义收敛为一个幂等操作：**每次成员变更后调用 `SyncRole(目标角色)`，
  由 IAM 侧实现「多退少补」**——比维护 N 个事件方法（added / removed /
  role_changed …）更难写错。

### 2. 同库事务复用（真原子，fail closed）

两域共用同一 MySQL 实例，且 platform UoW 支持「已在事务中则复用」
（`iam/infrastructure/persistence/uow.go`）。租户域的成员变更事务
（`WithinTx` / `Mutate`）内调用绑定同步，**任一侧失败整体回滚**：

- 「成员已加、绑定失败」比「加成员失败」更危险——运营以为交付完成，
  实际用户毫无权限，故障要到用户投诉才暴露；
- 同步失败回滚成员变更 = fail closed，重试即可，不存在中间态。

### 3. 商户级模板角色（随租户创建物化）

映射：`owner → tenant_owner`、`admin → tenant_admin`、`member → tenant_member`。

- **平台级模板不可行**：`ck_iam_role_tenant` CHECK 约束规定 biz 角色必须
  `scope_type='merchant' AND merchant_id <> ''`——「平台级 biz 角色」在库层
  直接被拒。这是刻意的库层防线（禁止用空 merchant_id 表示全部商户），
  不应绕过；
- 改为**随租户创建物化**三个模板角色（与 T-24 配额物化同模式）：
  `iam_role(channel='biz', scope_type='merchant', merchant_id=<租户id>,
  code='tenant_owner' / 'tenant_admin' / 'tenant_member', role_type='system')`，
  在租户 `Create` 事务内由 `RoleSyncer.EnsureTemplates` 写入（幂等）；
- 绑定落库维度为
  `(channel=biz, subject_type=user, subject_id, merchant_id=<租户id>, role_id=<模板角色ID>)`；
- 存量租户由迁移 `000015` 回填：为每个已存在租户补三个模板角色
  （确定性 ID `tpl-{tenant_id}-{role}`，幂等）+ 授予模板权限 +
  为在册成员补绑定；
- 权限演进按商户走 IAM 既有流程（每商户可自行调整模板角色的权限，
  也可用自建角色 `role_type=custom` 叠加；模板同步只增删模板角色的绑定，
  不碰 custom）；
- 模板权限取最小集：`tenant_owner` 含变更发起与商户内审批，
  `tenant_admin` 含变更发起与只读，`tenant_member` 仅基础只读——
  细化属各业务域权限演进，本 ADR 只锁定映射机制。

### 4. 同步点与语义

| 成员动作 | 同步行为 |
|---|---|
| Add（新增） | SyncRole(角色) → 绑定模板角色 |
| Add（复活停用成员） | SyncRole(角色) → Save 幂等重绑（原有过期/撤销绑定刷新生效） |
| SetRole | SyncRole(新角色) → 旧模板解绑 + 新模板绑定 |
| SetStatus(disable) | SyncRole(nil) → 解绑（幂等，reason 记录成员停用） |
| SetStatus(enable) | SyncRole(角色) → 重绑 |
| 邀请接受（T-14） | 走 Add 路径，天然覆盖 |
| 租户停用 / 终止 | 不在此同步——会话撤销（D-02）与守卫（T-05）已阻断，绑定保留供恢复 |

### 5. 端口与依赖方向

```
internal/tenant/app        定义 RoleSyncer 端口（接口反转，不 import iam）：
                             EnsureTemplates(ctx, tenantID)——Create 时物化模板角色
                             SyncRole(ctx, tenantID, subjectID, role *MemberRole)——nil 即解绑
cmd/internal/tenantruntime 实现 rolesyncAdapter（组合 iam 的 Binding/Role 仓储）
```

与 `TenantGuard` / `TenantMembership` 同模式：依赖只在 cmd 装配层汇合，
域与域之间零 import。`Syncer == nil` 时跳过同步（内存模式 / 单测），
生产装配必须注入——与 Membership 端口相同的 fail-closed 约定。

## 后果

- 成员与绑定的强一致由同库事务保证；跨库（未来分库）时需迁移到 Outbox
  异步同步，届时验收从「立即失效」放宽为「秒级失效」——现在不预做；
- 绑定审计（授权 / 解绑）由 IAM 侧既有 `iam.binding.*` 记录承载，
  租户域不重复记；成员审计仍走 `tenant.member.*`；
- 模板角色权限的演进走 IAM 既有变更流程（审批单），与租户生命周期解耦；
- 新增迁移 `000015`（模板角色种子），纯增量。
