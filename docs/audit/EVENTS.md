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
| `auth.tenant_switch` | B | 切换当前生效租户（T-22，`POST /biz/v1/auth/tenant-switch`）；`attrs.previous_tenant` 记录切换前生效租户，`attrs.session_id` 为同会话重签凭证；仅成员租户可切换，全部失败关闭（成员关系 / 租户状态不可判定即拒绝） | 认证 | 180d | active |
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
| `auth.saml.authorize` | C | SAML 授权发起（AuthnRequest / state 已签发）；只记 provider，不含主体 | 认证 | 180d | active |
| `auth.saml.callback` | C | SAML ACS 断言校验结果；只记 provider 与粗粒度原因，不含断言原文 | 认证 | 180d | active |
| `auth.m2m.token.issued` | A/B/C | M2M 令牌签发成功；记 `client_id`、端别与 scope，不含令牌原文 | 认证 | 180d | active |
| `auth.m2m.token.denied` | A/B/C | M2M 签发失败；`reason=invalid_client\|invalid_scope\|disabled\|rate_limited` | 认证 | 180d | active |
| `auth.m2m.client.created` | A | 创建机器客户端；记操作人与 scope 数量，不含 secret | 认证 | 180d | active |
| `auth.m2m.client.updated` | A | 更新机器客户端（名称 / 归属 / scope / 端别） | 认证 | 180d | active |
| `auth.m2m.client.disabled` | A | 停用机器客户端并查封全部密钥（`token_version++`） | 认证 | 180d | active |
| `auth.m2m.client.enabled` | A | 启用机器客户端；不复活旧令牌与旧密钥 | 认证 | 180d | active |
| `auth.m2m.client.deleted` | A | 软删机器客户端；`client_id` 不可复用 | 认证 | 180d | active |
| `auth.m2m.secret.rotated` | A | 轮换客户端密钥；只记指纹与宽限到期时间，不含明文 | 认证 | 180d | active |
| `auth.scim.user.created` | A | SCIM 建号；`actor_type=system`，记 `provider`、`external_id_hash`、`active`，不含 userName / 邮箱 | 认证 | 180d | active |
| `auth.scim.user.updated` | A | SCIM 更新 User（userName / externalId / displayName / active）；记 `version_check=if-match\|none` | 认证 | 180d | active |
| `auth.scim.user.deactivated` | A | SCIM `active=false`；撤销全部会话并递增 `token_version` | 认证 | 180d | active |
| `auth.scim.user.deleted` | A | SCIM 软删 User；清理组成员与角色投影，不含成员原文 | 认证 | 180d | active |
| `auth.scim.group.updated` | A | 组或成员变更；记 `provider`、`group_id`、`member_delta`（数量），不含成员列表 | 认证 | 180d | active |
| `auth.scim.token.created` | A | 签发 SCIM 令牌；只记 `provider` 与 `fingerprint`，不含令牌原文 | 认证 | 180d | active |
| `auth.scim.token.revoked` | A | 吊销 SCIM 令牌；只记 `provider` 与 `fingerprint` | 认证 | 180d | active |
| `auth.rate_limited` | A/B/C | 登录 / 短信触发限流；`resource_id` 为账号或手机号摘要 | 认证 | 180d | active |
| `auth.session.evicted` | A/B/C | 超出单账号设备上限，最久未使用的设备被淘汰（同时撤销该设备） | 认证 | 180d | active |
| `rbac.role_change` | A/B | 角色/权限点变更 | 权限 | 180d | active |
| `rbac.binding_change` | A/B | 主体角色绑定 / 解绑 | 权限 | 180d | active |
| `rbac.breakglass` | A | 破玻璃申请 / 审批 / 生效 / 撤销 | 权限 | 180d | active |
| `rbac.menu_change` | A | 菜单 / 路由资源创建、更新、停用 | 权限 | 180d | active |
| `rbac.sod_change` | A/B | 互斥角色组（RBAC2）创建、更新、启停、删除 | 权限 | 180d | active |
| `rbac.certification` | A | 访问复核活动创建 / 激活 / 关闭、任务裁决与证据导出 | 权限 | 180d | active |
| `config.change` | A | 平台配置变更 | 配置 | 180d | planned |
| `config.bootstrap.completed` | A | 首次启动初始化完成：基线 Release、生成密钥数量、缺失项；不记录配置值 | 配置 | 180d | planned |
| `config.release.publish` | A | 配置 Release 发布（含灰度范围、release_id、变更键数量） | 配置 | 180d | planned |
| `config.release.rollback` | A | 配置 Release 回滚（含源 / 目标 release_id、操作者、原因） | 配置 | 180d | planned |
| `config.release.approve` | A | 配置发布审批；记录提交人与审批人，二者必须不同 | 配置 | 180d | planned |
| `config.secret.create` | A | 创建业务密钥；只记录 key、版本、指纹，不记录明文 / 密文 | 配置 | 180d | active |
| `config.secret.rotate` | A | 轮换业务密钥；记录新旧版本与 grace，不记录密钥材料 | 配置 | 180d | active |
| `config.secret.revoke` | A | 吊销业务密钥；记录原因与影响服务，不记录密钥材料 | 配置 | 180d | active |
| `config.secret.grant` | A | 变更密钥解析授权（env / namespace / service_identity / secret） | 配置 | 180d | active |
| `config.secret.resolve` | A | 服务身份解析密钥；记录服务身份、key、版本、结果与 trace_id，不记录明文 | 配置 | 180d | active |
| `data.export` | A/B | 数据导出（含 PII 须记范围摘要）；同步 `GET /admin/v1/audit/events/export`、异步任务完成与下载（`attrs.mode=async` / `async-download`）均记录导出人、时间窗、条数、过滤条件名称与 `export_id` | 导出 | 365d | active |
| `audit.query` | A | 审计事件查询；`GET /admin/v1/audit/events` 每次查询记录操作者、时间窗、命中条数与过滤条件名称 | 权限 | 180d | active |
| `audit.retention_purge` | worker | 审计到期清理（元审计：记录 cutoff / 到期数 / 归档数 / 删除数 / 批次数；配置 `archive_prefix` 时先落归档对象再删除） | 权限 | 180d | active |
| `data.query_scope` | A | 跨商户/大范围查询明细 | 导出 | 365d | planned |
| `tenant.create` | A | 创建租户（`POST /admin/v1/tenants`）；记录名称、编码（`attrs.code`）与初始状态 `pending` | 租户 | 365d | active |
| `tenant.update` | A | 变更租户资料（T-11，`PATCH /admin/v1/tenants/{id}`：改名 + 元数据）；`attrs.fields` 记录变更字段列表（逗号分隔）；编码不可变，不在本动作范围内 | 租户 | 365d | active |
| `tenant.approval.submit` | A | 提交 / 重新提交入驻申请（`POST /admin/v1/tenants/{id}/approval`）；`reason` 记录申请说明；驳回后重提开启新一轮，历史事件保留（append-only，当前态由最后一条事件派生） | 租户 | 365d | active |
| `tenant.approval.approve` | A | 入驻审核通过（`POST /admin/v1/tenants/{id}/approval/approve`）；**提交人不得审批自己的申请**，违反时以 `outcome=failure` 留痕。通过只是开通前置之一，不改变租户状态 | 租户 | 365d | active |
| `tenant.approval.reject` | A | 入驻审核驳回（`POST /admin/v1/tenants/{id}/approval/reject`）；`reason` 必填并原样入审计；驳回后可修改资料重新提交 | 租户 | 365d | active |
| `tenant.activate` | A | 开通租户（`pending → active`）；要求**已通过入驻审核**（否则 40900，不写成功事件）且至少一名在册 owner。租户自此进入服务期 | 租户 | 365d | active |
| `tenant.suspend` | A | 停用租户（`active → suspended`）；`reason` 必填并原样入审计，失败尝试同样记录（`outcome=failure`）。停用前先撤销该租户全部会话，`attrs.revoked_sessions` 记录撤销数（D-02=①） | 租户 | 365d | active |
| `tenant.resume` | A | 恢复租户（`suspended → active`）；与首次开通分开记录，便于还原停用时长 | 租户 | 365d | active |
| `tenant.terminate` | A | 终止租户（→ `terminated` 终态）；`reason` 必填，终止后不可恢复 | 租户 | 365d | active |
| `tenant.member.add` | A | 添加租户成员（`POST /admin/v1/tenants/{id}/members`）；`attrs.subject_id` / `attrs.role`，已停用成员再次添加时 `attrs.reactivated=true`；`attrs.active_members` / `attrs.seat_limit` 记录席位上下文（T-13），席位超限被拒时以 `outcome=failure` 留痕 | 租户 | 365d | active |
| `tenant.invitation.create` | A | 创建成员邀请（T-14，`POST /admin/v1/tenants/{id}/invitations`）；`attrs.email` / `attrs.role` / `attrs.expires_at`；明文 token 只存摘要；ENT-10 就绪后邮件经通知中心异步投递（事件与邀请同事务，async 未启用时保持线下转交语义） | 租户 | 365d | active |
| `tenant.invitation.accept` | A | 接受邀请并建立成员关系；`attrs.subject_id` 记录实际接受主体（与 email 解耦）；同一主体重复接受幂等（不产生重复事件），换人抢受被拒不落事件（防枚举） | 租户 | 365d | active |
| `tenant.invitation.revoke` | A | 撤销待处理邀请，token 立即失效；`reason` 写入审计 | 租户 | 365d | active |
| `tenant.invitation.resend` | A | 重发邀请：旧 token 作废（摘要覆盖）、有效期顺延；泄露场景下即为轮换手段 | 租户 | 365d | active |
| `tenant.data_purge` | system | 租户硬删除完成（ENT-15 D-05 第 3 段）；`attrs.tables`（边界清单）/ `attrs.rows_deleted` / `attrs.export_manifest_sha256` / `attrs.terminated_at`；仅对「导出证据已校验」的租户执行，删除后写入且永久可查（审计是唯一不删的数据） | 租户 | 365d | active |
| `tenant.member.role_change` | A | 变更成员角色；`attrs.from` / `attrs.to` / `attrs.subject_id`，降级最后一名 owner 会被拒绝（`outcome=failure` 同样留痕） | 租户 | 365d | active |
| `tenant.member.disable` | A | 停用成员（保留成员关系，不物理删除）；`reason` 可选，建议填写离职或调岗原因 | 租户 | 365d | active |
| `tenant.member.enable` | A | 重新启用成员 | 租户 | 365d | active |
| `tenant.quota_change` | A | 修改租户配额（`PUT /admin/v1/tenants/{id}/quotas/{resource}`）；`attrs.resource` / `attrs.limit`（不限量记为 `unlimited`），执行与拒绝由 ENT-14 负责 | 租户 | 365d | active |
| `notification.template.create` | A | 注册通知模板（ENT-10）；`attrs.template_key` / `attrs.channel` / `attrs.locale` / `attrs.version` | 通知 | 365d | active |
| `notification.template.update` | A | 更新 / 停用 / 启用模板（同键落新版本或切换 `enabled`）；`attrs.template_key` / `attrs.channel` / `attrs.version` / `attrs.enabled` | 通知 | 365d | active |
| `notification.preference.update` | A/B | 变更渠道偏好（含退订）；`attrs.subject_type` / `attrs.subject_id` / `attrs.scene_class` / `attrs.channel` / `attrs.enabled`；事务类场景退订被拒不落事件 | 通知 | 365d | active |
| `notification.blacklist.add` | A | 加入渠道黑名单（投诉 / 退信 / 手工）；`attrs.channel` / `attrs.source` / `attrs.target_hmac`（仅摘要前 8 位，明文地址不入审计） | 通知 | 365d | active |
| `notification.blacklist.remove` | A | 移除黑名单条目；`attrs.channel` / `attrs.target_hmac` | 通知 | 365d | active |
| `notification.request.rejected` | system | 通知被拒发（黑名单 / 全渠道退订 / 过期 / 无可达地址）；`attrs.scene` / `attrs.semantic`，reason 记录逐渠道跳过原因 | 通知 | 365d | active |
| `notification.dlq.replay` | A | 重放死信通知（补偿动作，写操作）；`attrs.scene` / `attrs.replay_request`，重放生成新事件与新通知单、新幂等键防重复，原请求保留为审计痕迹 | 通知 | 365d | active |
| `notification.send` | A | 运营代发通知（`POST /admin/v1/notifications`，ENT-10 NT-06）；`attrs.scene` / `attrs.channels` / `attrs.template_key` / `attrs.recipient_ref`（摘要）；收件人明文仅入事件载荷 | 通知 | 365d | active |
| `featureflag.create` | A | 创建特性开关（ENT-11）；`attrs.permanent` / `attrs.default` / `attrs.rules`；防御性默认拒绝 default_enabled=true 的裸全量创建 | 治理 | 365d | active |
| `featureflag.update` | A | 变更开关描述 / 规则 / 默认值 / 到期时间；`attrs.fields`（变更字段列表）与 `attrs.version` | 治理 | 365d | active |
| `featureflag.enable` | A | 启用开关（kill switch 解除）；`POST .../status`，乐观锁防并发覆盖 | 治理 | 365d | active |
| `featureflag.disable` | A | 停用开关（kill switch，秒级生效 ≤5s）；`POST .../status` | 治理 | 365d | active |
| `featureflag.delete` | A | 物理删除开关；仅允许对非 enabled 状态执行（在用 kill switch 必须先停用） | 治理 | 365d | active |
| `featureflag.expire` | system | 到期自动禁用（worker 治理任务）；`attrs.expires_at`；「无长期遗留 flag」判据的执行留痕 | 治理 | 365d | active |
| `payment.capture` | B/C | 支付成功确认 | 资金 | 365d | planned |
| `payment.refund` | B/C | 退款 | 资金 | 365d | planned |

脚手架：`internal/shared/audit` 已提供 Emitter 与上述 `action` 常量；业务路径接线时把对应行 `status` 改为 `active`。
