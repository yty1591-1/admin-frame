# 租户开通运维手册（tenant-onboarding）

> 关联：ENT-09、ADR-0006、[tenant-suspend.md](./tenant-suspend.md)（反向操作：停用）、
> `docs/audit/EVENTS.md`（`tenant.create` / `tenant.activate`）

本手册覆盖新商户从「进系统」到「可服务」的两条路径，以及首部署时的存量回填。
所有命令都要求 A 端 `tenant:write` 权限。

## 症状

开通交付阶段的异常信号：

- 新商户用户登录返回 403 `tenant is not available`（租户未建、未回填或未 `activate`）；
- `activate` 返回 409（无在册 owner）或 404（`plan_code` 不存在）；
- 创建返回 409 `tenant code already exists`（编码全局唯一冲突）；
- 添加成员返回 409（席位超限，`user` 配额不足）。

## 影响

- 开通未完成期间，该商户全部 B 端请求被守卫拒绝（403）——不影响其它商户；
- 回填迁移 `000010` 幂等可重跑，重复执行不产生重复租户；
- 错误的 `plan_code` 在创建时即失败（404），不会产生「有租户没配额」的中间态。

## 排查

1. 租户是否存在 / 状态是否 `active`：`GET /admin/v1/tenants/{id}`；
2. 创建失败看响应业务码：40900 编码或席位冲突、40400 套餐编码、40000 字段校验；
3. 回填后仍 403：确认守卫已 reload（TTL ≤30s）且请求走的 `merchant_id` 与租户 id 一致；
4. 配额缺失：查创建时审计 `tenant.create` 的 `attrs.plan`，确认当时套餐目录已种子。

## 缓解与恢复

- 「有租户没 owner」→ `POST /tenants/{id}/members` 补 owner 后再 `activate`；
- 「配额物化错误」→ `PUT /tenants/{id}/quotas/{resource}` 手工修正（纠正后留审计）；
- 「编码建错」→ `code` 不可变：数据订正流程处理，或终止该租户按新租户重开；
- 「回填把名字建成了 merchant_id」→ 运营用 `PATCH /tenants/{id}` 补全名称与元数据。

## 路径一：新建租户（推荐）

一步创建（T-12 支持直落 owner，套餐配额在创建时物化，T-24）：

```http
POST /admin/v1/tenants
{
  "name": "示例科技",
  "code": "acme-cn",              // 可选；缺省用租户 ID 兜底（对账引用，创建后不可变）
  "plan_code": "standard",        // 可选；缺省 standard。配额在创建时按套餐物化，之后改套餐不影响本租户
  "owner_subject_id": "u-10001"   // 可选；推荐提供——开通前置要求至少一名在册 owner
}
```

随后开通：

```http
POST /admin/v1/tenants/{id}/activate
```

- `pending → active` 要求至少一名在册 owner，否则 409（先
  `POST /tenants/{id}/members` 补齐）；
- 开通即对外服务：B 端该 `merchant_id` 的请求在守卫缓存刷新后（≤30s）放行；
- 首名 owner 若经由创建参数直落，审计记 `tenant.member.add`（`seeded=true`）。

## 路径二：存量商户回填（首次上线守卫的前置）

守卫开启后，未登记的 `merchant_id` 一律拒绝（fail closed）。存量部署必须
**先回填再开守卫**，顺序不能反：

1. 执行迁移 `000010`（幂等，可重复跑）：把 `iam_role` / `iam_role_binding` /
   `identity_account` 中非空 `merchant_id` 登记为 `active` 租户；
2. 回填生成的租户 `name` = `merchant_id`、`status_reason` = `backfill: 存量商户自动登记`，
   **需要运营随后补全名称与元数据**（`PATCH /admin/v1/tenants/{id}`）；
3. 回填不覆盖已存在记录；排查影响面的手工 SQL 见 [tenant-suspend.md](./tenant-suspend.md)。

## 开通检查清单

开通前后逐项确认（全部满足才算完成交付）：

| # | 检查项 | 方式 | 失败处置 |
|---|---|---|---|
| 1 | 租户存在且 `status=active` | `GET /admin/v1/tenants/{id}` | 补 `activate` |
| 2 | 至少一名在册 owner | `GET /tenants/{id}/members?role=owner` | `POST members` 补 owner |
| 3 | 配额已物化（`user` 等资源有行） | `GET /tenants/{id}/quotas` | 检查创建时 `plan_code` 是否有效 |
| 4 | `code` 已按需订正（对账引用） | 详情响应 `code` 字段 | 数据订正流程（不可 PATCH） |
| 5 | 联系人 / 元数据补全 | 详情响应 | `PATCH /tenants/{id}` |
| 6 | B 端真实请求放行（≤30s） | 用该商户令牌调一个只读接口 | 查 [tenant-suspend.md](./tenant-suspend.md) |
| 7 | 审计可查 `tenant.create` / `tenant.activate` | 审计流水按 `resource_id` 查 | 查 Outbox 与 Relay（[async-dlq.md](./async-dlq.md)） |
| 8 | （可选）邀请首批成员 | `POST /tenants/{id}/invitations` | token 线下转交（邮件投递依赖 ENT-10） |

## 升级

- 批量开通（>20 租户）或回填影响面超预期 → 升级平台值班（P2），附影响面 SQL 结果；
- 套餐目录缺失（`DefaultPlan` 不可用）→ 升级后端负责人，检查迁移 `000012` 是否执行；
- 开通后 B 端持续 403 超过 60s → 按 [tenant-suspend.md](./tenant-suspend.md) 排查升级。

## 常见问题

- **创建后立即登录被 403**：租户还在 `pending`；守卫按状态拒绝，先 `activate`。
- **改套餐没生效**：套餐是开账模板（T-24），配额创建时已物化；调整存量租户配额
  用 `PUT /tenants/{id}/quotas/{resource}`，不要指望改套餐回填。
- **`code` 重复 409**：全局唯一约束（`uk_tenant_code`）；换编码或订正存量行。
- **成员席位不足 409**：`user` 配额限制；调高配额或清理停用成员（停用成员占席，复活也占席）。

## 相关链接

- [ADR-0006 租户即隔离边界](../adr/0006-tenant-as-isolation-boundary.md)
- [数据生命周期（终止后处置）](../tenant-data-lifecycle.md)
- [审计事件目录](../audit/EVENTS.md)
