# 审计事件目录（AUD-01）

> 稳定 `action` 只追加、不复用语义。实现状态：`active` = 代码须可发出；`planned` = 路径未合入前允许仅目录占位。

| action | 端 | 说明 | 保留类别 | 最短保留 | status |
|--------|----|------|----------|----------|--------|
| `auth.login` | A/B/C | 登录成功 | 认证 | 180d | active |
| `auth.login_failed` | A/B/C | 登录失败（含防爆破关联） | 认证 | 180d | active |
| `auth.account.created` | A/B/C | 运维 seed CLI 开通账号（含首个管理员）/ C 端短信自助开户 | 认证 | 180d | active |
| `auth.account.locked` | A/B/C | 登录失败达阈值触发账号锁定 | 认证 | 180d | active |
| `auth.account.disabled` | A | 管理员停用账号（同时撤销全部会话） | 认证 | 180d | active |
| `auth.account.enabled` | A | 管理员恢复停用账号 | 认证 | 180d | active |
| `auth.account.unlocked` | A | 管理员手动解锁账号 | 认证 | 180d | active |
| `auth.account.deleted` | A/C | 软删账号（管理员注销 / 用户自助注销） | 认证 | 180d | active |
| `auth.account.restored` | A | 管理员恢复软删账号 | 认证 | 180d | active |
| `auth.password.changed` | A | 用户自助改密 / 强制改密完成（递增 token_version 并撤销全部会话） | 认证 | 180d | active |
| `auth.password.reset` | A | 管理员重置密码（生成一次性临时口令并置强制改密标记） | 认证 | 180d | active |
| `auth.phone.rebound` | C | C 端手机号换绑（撤销全部会话） | 认证 | 180d | active |
| `auth.logout` | A/B/C | 登出当前会话（幂等） | 认证 | 180d | active |
| `auth.token.refresh` | A/B/C | 刷新令牌成功轮换 | 认证 | 180d | active |
| `auth.token.reused` | A/B/C | 刷新令牌重放，触发整链撤销 | 认证 | 180d | active |
| `auth.token.revoked` | A/B | 单会话 / 全端撤销 | 认证 | 180d | active |
| `auth.mfa.bound` | A | MFA 绑定完成 | 认证 | 180d | active |
| `auth.mfa.failed` | A | MFA 动态码 / 恢复码校验失败 | 认证 | 180d | active |
| `auth.mfa.disabled` | A | MFA 解绑（同时撤销全部会话） | 认证 | 180d | active |
| `auth.mfa.reset` | A | 管理员重置 MFA 绑定（用户丢失 TOTP 与恢复码的兜底） | 认证 | 180d | active |
| `rbac.role_change` | A/B | 角色/权限点变更 | 权限 | 180d | active |
| `rbac.binding_change` | A/B | 主体角色绑定 / 解绑 | 权限 | 180d | active |
| `rbac.breakglass` | A | 破玻璃申请 / 审批 / 生效 / 撤销 | 权限 | 180d | active |
| `rbac.menu_change` | A | 菜单 / 路由资源创建、更新、停用 | 权限 | 180d | active |
| `rbac.sod_change` | A/B | 互斥角色组（RBAC2）创建、更新、启停、删除 | 权限 | 180d | active |
| `config.change` | A | 平台配置变更 | 配置 | 180d | planned |
| `data.export` | A/B | 数据导出（含 PII 须记范围摘要） | 导出 | 365d | planned |
| `data.query_scope` | A | 跨商户/大范围查询明细 | 导出 | 365d | planned |
| `payment.capture` | B/C | 支付成功确认 | 资金 | 365d | planned |
| `payment.refund` | B/C | 退款 | 资金 | 365d | planned |

脚手架：`internal/shared/audit` 已提供 Emitter 与上述 `action` 常量；业务路径接线时把对应行 `status` 改为 `active`。
