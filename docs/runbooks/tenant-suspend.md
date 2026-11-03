# 租户停用 / 不可用（B 端统一 403）

> 关联：ENT-09、ADR-0006、`docs/audit/EVENTS.md`（`tenant.*`）、`docs/iam/PERMISSIONS.md`（`tenant:write`）

## 症状

- B 端任意接口返回 `403` + 业务码 `40300`，文案 `tenant is not available`；
- 或 B 端返回 `503` + 业务码 `50300`，文案 `tenant status unavailable`；
- A 端租户列表里该商户 `status` 为 `pending` / `suspended` / `terminated`，或根本没有该行；
- 审计中可查到 `tenant.suspend` / `tenant.terminate`（含操作人与 `reason`）。

## 影响

- 该商户全部已登录用户在守卫生效后立即无法访问 B 端（读与写同时阻断）；
- A 端与 C 端不受影响：它们的 Principal 没有 `merchant_id`，不经过租户守卫；
- 其它商户不受影响（守卫按 `merchant_id` 逐请求判定，缓存键也是商户维度）。

## 排查

1. **先确认租户状态**（不要先查代码）：

   ```sql
   SELECT id, name, status, status_reason, version, suspended_at, terminated_at
   FROM tenant WHERE id = '<merchant_id>';
   ```

2. **区分三种成因**：
   - 无此行 → 租户未登记（新商户漏建，或存量回填未执行）；
   - `suspended` / `terminated` → 有意停用，查审计 `tenant.suspend` / `tenant.terminate` 的 `reason` 与操作人；
   - `pending` → 建了但没开通，补 `POST /admin/v1/tenants/{id}/activate`。
3. **区分 403 与 503**：403 表示「明确不可用」；503 表示守卫**无法判定**（数据库不可达 / 查询超时）。
   503 不要去改租户状态，先查数据库与连接池——守卫是 fail closed，查不到就不放行。
4. **确认生效时延**：状态缓存 TTL 为 30s（`DefaultStatusTTL`）。停用后最坏 30s 内所有副本生效，
   不满足「停用 ≤ 60s 拒绝访问」时先查是否有副本长时间未 reload（进程时钟、缓存实现被替换等）。

## 缓解与恢复

- **误停用**：`POST /admin/v1/tenants/{id}/resume`（需要 `tenant:write`），本进程立即生效，
  其它副本 ≤ 30s（TTL）收敛；恢复动作写 `tenant.resume` 审计。
- **未登记**：补建租户后 `activate`；若是一次性批量接入，走下面的「存量回填」。
- **已终止**：终态不可恢复（ADR-0006），需按新租户重新开通；数据处置见 ENT-15。
- **503（无法判定）**：按数据库故障处理，不要通过关闭守卫来「恢复业务」——
  那等于让已停用的租户重新可用。

### 存量回填（首次上线守卫的前置）

守卫开启后，未登记的 `merchant_id` 一律拒绝。存量部署必须先回填再开守卫：

```sql
-- 迁移 000010 已内置该逻辑，幂等；手工排查时可单独执行确认影响面
SELECT DISTINCT merchant_id FROM iam_role        WHERE merchant_id <> ''
UNION SELECT DISTINCT merchant_id FROM iam_role_binding  WHERE merchant_id <> ''
UNION SELECT DISTINCT merchant_id FROM identity_account   WHERE merchant_id <> '';
```

回填生成的租户 `name` 等于 `merchant_id`、`status_reason` 为 `backfill: 存量商户自动登记`，
需要运营随后补全名称；回填**不覆盖**已存在的租户记录。

## 升级

- 影响多个商户或无法判定原因 → 升级至平台值班（P1），并附：`merchant_id`、`/readyz` 结果、
  租户表查询结果、最近 10 条 `tenant.*` 审计；
- 疑似鉴权缺陷（状态为 `active` 仍 403）→ 升级至后端负责人，附请求 `trace_id` 与守卫日志；
- 需要临时放行某个已停用商户 → 走 `resume` 并留 `reason`，禁止改代码或关守卫。

## 相关链接

- [ADR-0006 租户即隔离边界](../adr/0006-tenant-as-isolation-boundary.md)
- [审计事件目录](../audit/EVENTS.md)
- [权限目录](../iam/PERMISSIONS.md)
- [生产就绪实施计划 §五](../production-readiness-implementation-plan.md)
