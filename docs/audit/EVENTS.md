# 审计事件目录（AUD-01）

> 稳定 `action` 只追加、不复用语义。实现状态：`active` = 代码须可发出；`planned` = 路径未合入前允许仅目录占位。

| action | 端 | 说明 | 保留类别 | 最短保留 | status |
|--------|----|------|----------|----------|--------|
| `auth.login` | A/B/C | 登录成功 | 认证 | 180d | active |
| `auth.login_failed` | A/B/C | 登录失败（含防爆破关联）；MFA 校验遇到 locked / disabled 同样记此事件 | 认证 | 180d | active |
| `auth.account.created` | A/B/C | A 端 `admin-api` 启动引导创建系统默认平台管理员（`actor_type=system`，`service=admin-api`）/ C 端短信自助开户 / OAuth 自动建号（`actor_type=system`，仅记录 provider） | 认证 | 180d | active |
| `auth.account.locked` | A/B/C | 登录失败达阈值触发账号锁定 | 认证 | 180d | active |
| `auth.account.disabled` | A | 管理员停用账号（同时撤销全部会话） | 认证 | 180d | active |
| `auth.account.enabled` | A | 管理员恢复停用账号 | 认证 | 180d | active |
| `auth.account.unlocked` | A | 管理员手动解锁账号 | 认证 | 180d | active |
| `auth.account.deleted` | A/C | 软删账号（管理员注销 / 用户自助注销） | 认证 | 180d | active |
| `auth.account.restored` | A | 管理员恢复软删账号 | 认证 | 180d | active |
| `auth.password.changed` | A | 用户自助改密 / 强制改密完成（递增 token_version 并撤销全部会话） | 认证 | 180d | active |
| `auth.password.reset` | A | 管理员重置密码（生成一次性临时口令并置强制改密标记） | 认证 | 180d | active |
| `auth.profile.updated` | A/B | 用户自助修改资料（仅 `display_name`；只记字段名，不记昵称原文） | 认证 | 180d | active |
| `auth.phone.rebound` | C | C 端手机号换绑（撤销全部会话） | 认证 | 180d | active |
| `auth.logout` | A/B/C | 登出当前会话（幂等） | 认证 | 180d | active |
| `auth.token.refresh` | A/B/C | 刷新令牌成功轮换 | 认证 | 180d | active |
| `auth.token.reused` | A/B/C | 刷新令牌重放，触发整链撤销 | 认证 | 180d | active |
| `auth.token.revoked` | A/B | 单会话 / 全端撤销 | 认证 | 180d | active |
| `auth.mfa.bound` | A | MFA 绑定完成 | 认证 | 180d | active |
| `auth.mfa.failed` | A | MFA 动态码 / 恢复码校验失败 | 认证 | 180d | active |
| `auth.mfa.disabled` | A | MFA 解绑（同时撤销全部会话） | 认证 | 180d | active |
| `auth.mfa.reset` | A | 管理员重置 MFA 绑定（用户丢失 TOTP 与恢复码的兜底） | 认证 | 180d | active |
| `auth.mfa.setup` | A | MFA 绑定发起（已下发新密钥但尚未完成首次动态码校验） | 认证 | 180d | active |
| `auth.mfa.recovery_used` | A | 使用一次性恢复码完成 MFA（含剩余数量） | 认证 | 180d | active |
| `auth.mfa.ticket_replay` | A | 强制绑定票据重放被拒绝（不计入失败次数） | 认证 | 180d | active |
| `auth.sms.send` | C | 短信验证码发送成功 / 失败；`resource_id` 为手机号摘要，不含明文 | 认证 | 180d | active |
| `auth.oauth.authorize` | C | OAuth 授权发起（state 已签发）；只记 provider，不含外部主体 | 认证 | 180d | active |
| `auth.oauth.callback` | C | OAuth 回调结果；只记 provider 与粗粒度原因，不含授权码 / 令牌 | 认证 | 180d | active |
| `auth.rate_limited` | A/B/C | 登录 / 短信触发限流；`resource_id` 为账号或手机号摘要 | 认证 | 180d | active |
| `auth.session.evicted` | A/B/C | 超出单账号设备上限，最久未使用的设备被淘汰（同时撤销该设备） | 认证 | 180d | active |
| `rbac.role_change` | A/B | 角色/权限点变更 | 权限 | 180d | active |
| `rbac.binding_change` | A/B | 主体角色绑定 / 解绑 | 权限 | 180d | active |
| `rbac.breakglass` | A | 破玻璃申请 / 审批 / 生效 / 撤销 | 权限 | 180d | active |
| `rbac.menu_change` | A | 菜单 / 路由资源创建、更新、停用 | 权限 | 180d | active |
| `rbac.sod_change` | A/B | 互斥角色组（RBAC2）创建、更新、启停、删除 | 权限 | 180d | active |
| `config.change` | A | 平台配置变更 | 配置 | 180d | planned |
| `data.export` | A/B | 数据导出（含 PII 须记范围摘要）；同步 `GET /admin/v1/audit/events/export`、异步任务完成与下载（`attrs.mode=async` / `async-download`）均记录导出人、时间窗、条数、过滤条件名称与 `export_id` | 导出 | 365d | active |
| `audit.query` | A | 审计事件查询；`GET /admin/v1/audit/events` 每次查询记录操作者、时间窗、命中条数与过滤条件名称 | 权限 | 180d | active |
| `audit.retention_purge` | worker | 审计到期清理（元审计：记录 cutoff / 到期数 / 归档数 / 删除数 / 批次数；配置 `archive_prefix` 时先落归档对象再删除） | 权限 | 180d | active |
| `data.query_scope` | A | 跨商户/大范围查询明细 | 导出 | 365d | planned |
| `tenant.create` | A | 创建租户（`POST /admin/v1/tenants`）；记录名称与初始状态 `pending` | 租户 | 365d | active |
| `tenant.activate` | A | 开通租户（`pending → active`）；租户自此进入服务期 | 租户 | 365d | active |
| `tenant.suspend` | A | 停用租户（`active → suspended`）；`reason` 必填并原样入审计，失败尝试同样记录（`outcome=failure`） | 租户 | 365d | active |
| `tenant.resume` | A | 恢复租户（`suspended → active`）；与首次开通分开记录，便于还原停用时长 | 租户 | 365d | active |
| `tenant.terminate` | A | 终止租户（→ `terminated` 终态）；`reason` 必填，终止后不可恢复 | 租户 | 365d | active |
| `payment.capture` | B/C | 支付成功确认 | 资金 | 365d | planned |
| `payment.refund` | B/C | 退款 | 资金 | 365d | planned |

脚手架：`internal/shared/audit` 已提供 Emitter 与上述 `action` 常量；业务路径接线时把对应行 `status` 改为 `active`。
