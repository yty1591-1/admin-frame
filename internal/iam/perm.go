package iam

// PermissionCode 是权限点的稳定标识，形如 resource:action 或 domain:resource:action。
//
// 权限码是跨版本契约：发布后不得改名、不得复用；废弃通过 iam_permission.status=deprecated
// 表达，而不是删除常量，否则历史审计与旧客户端无法解释。新增权限码必须同步
// docs/iam/PERMISSIONS.md、本文件与 migrations/000003_seed_iam_baseline.up.sql，
// 由 internal/iam/catalog 的单元测试做门禁。
type PermissionCode string

// String 返回权限码字面量，便于日志、错误信息与测试断言。
func (c PermissionCode) String() string { return string(c) }

// 权限点目录基线。命名规则：
//   - 全小写，段间以 ":" 分隔，2～4 段；
//   - 禁止通配符 "*"（超级权限只由 breakglass 角色在审批有效期内动态展开）；
//   - IAM 自身管理权限统一以 "iam:" 开头，便于按资源聚合。
const (
	// PermPermissionRead 查看权限点目录。
	PermPermissionRead PermissionCode = "iam:permission:read"
	// PermRoleRead 查看角色定义与已授予权限。
	PermRoleRead PermissionCode = "iam:role:read"
	// PermRoleWrite 创建、更新、停用、软删角色（不含授权）。
	PermRoleWrite PermissionCode = "iam:role:write"
	// PermRoleGrant 替换角色权限点集合。
	PermRoleGrant PermissionCode = "iam:role:grant"
	// PermBindingRead 查看主体与角色的绑定关系。
	PermBindingRead PermissionCode = "iam:binding:read"
	// PermBindingWrite 绑定或解绑主体角色。
	PermBindingWrite PermissionCode = "iam:binding:write"
	// PermCheck 模拟鉴权，用于排障与灰度比对。
	PermCheck PermissionCode = "iam:check"
	// PermMenuRead 查看菜单 / 路由资源管理列表（A 端专属）。
	PermMenuRead PermissionCode = "iam:menu:read"
	// PermMenuWrite 创建、更新、停用菜单资源（A 端专属，不含授权关系）。
	PermMenuWrite PermissionCode = "iam:menu:write"
	// PermSodRead 查看互斥角色组（RBAC2）。
	PermSodRead PermissionCode = "iam:sod:read"
	// PermSodWrite 管理互斥角色组（RBAC2），含成员与阈值变更。
	PermSodWrite PermissionCode = "iam:sod:write"
	// PermIdentityAccountRead 查看身份账号列表 / 详情与目标账号会话（A 端专属）。
	PermIdentityAccountRead PermissionCode = "identity:account:read"
	// PermIdentityAccountWrite 管理身份账号生命周期（停用 / 启用 / 锁定 / 解锁 / 软删恢复）。
	PermIdentityAccountWrite PermissionCode = "identity:account:write"
	// PermIdentityAccountCredential 重置目标账号密码 / MFA（凭据处置，A 端专属）。
	//
	// 与生命周期写、删除、会话撤销分离，落实职责分离（SEC-02 / P1-14）。
	PermIdentityAccountCredential PermissionCode = "identity:account:credential"
	// PermIdentityAccountDelete 软删目标账号（不可逆操作的入口）。
	PermIdentityAccountDelete PermissionCode = "identity:account:delete"
	// PermIdentityAccountSession 撤销目标账号的会话（单设备或全部）。
	PermIdentityAccountSession PermissionCode = "identity:account:session"
	// PermIdentityM2MRead 查看 M2M 客户端列表与状态（A 端专属，§21.2.4）。
	PermIdentityM2MRead PermissionCode = "identity:m2m:read"
	// PermIdentityM2MWrite 创建 / 停用 / 启用 / 软删 M2M 客户端（A 端专属）。
	PermIdentityM2MWrite PermissionCode = "identity:m2m:write"
	// PermIdentityM2MCredential 轮换 M2M 客户端密钥（返回一次性明文）。
	//
	// 与 write 分离：能建客户端不等于能取走密钥，落实职责分离（SEC-02）。
	PermIdentityM2MCredential PermissionCode = "identity:m2m:credential" //nolint:gosec // G101 误报：权限码，非凭据。
	// PermIdentitySCIMRead 查看 SCIM 令牌指纹与组映射状态（A 端专属，§21.1.6）。
	PermIdentitySCIMRead PermissionCode = "identity:scim:read"
	// PermIdentitySCIMWrite 签发与吊销 SCIM 令牌（A 端专属）。
	PermIdentitySCIMWrite PermissionCode = "identity:scim:write"
	// PermChangeRead 查看高风险变更审批单。
	PermChangeRead PermissionCode = "iam:change:read"
	// PermChangeApprove 审批角色 / 绑定等高风险变更。
	PermChangeApprove PermissionCode = "iam:change:approve"
	// PermChangeRequest 以本商户身份发起审批单，并查看本商户待办列表（B 端专属）。
	//
	// 与 A 端的 iam:change:read 刻意分离：即使 B 端商户过滤逻辑出错，
	// 持有本权限点的主体也无法读取平台级审批单（纵深防御，SEC-14）。
	PermChangeRequest PermissionCode = "iam:change:request"
	// PermChangeReview 审批本商户变更（B 端专属），申请人不得自审（SoD）。
	PermChangeReview PermissionCode = "iam:change:review"
	// PermBreakglassRequest 申请限时超级权限。
	PermBreakglassRequest PermissionCode = "iam:breakglass:request"
	// PermBreakglassApprove 审批破玻璃申请。
	PermBreakglassApprove PermissionCode = "iam:breakglass:approve"
	// PermCertificationRead 查看访问复核活动、任务与证据导出（A 端专属）。
	PermCertificationRead PermissionCode = "iam:certification:read"
	// PermCertificationWrite 创建 / 激活 / 关闭访问复核活动并生成任务清单（A 端专属）。
	PermCertificationWrite PermissionCode = "iam:certification:write"
	// PermCertificationDecide 裁决复核任务（确认保留 / 驳回回收）；与 write 分离以落实职责分离。
	PermCertificationDecide PermissionCode = "iam:certification:decide"

	// PermOrderRead 查询订单。
	//
	// Deprecated：订单演示域已移除；保留权限码与既有授权以解释历史审计，禁止新授。
	PermOrderRead PermissionCode = "order:read"
	// PermOrderWrite 创建、更新、取消订单。
	//
	// Deprecated：订单演示域已移除；保留权限码与既有授权以解释历史审计，禁止新授。
	PermOrderWrite PermissionCode = "order:write"
	// PermFileRead 下载或预览文件。
	//
	// Deprecated：文件演示域已移除；保留权限码与既有授权以解释历史审计，禁止新授。
	PermFileRead PermissionCode = "file:read"
	// PermFileWrite 上传、替换、删除文件。
	//
	// Deprecated：文件演示域已移除；保留权限码与既有授权以解释历史审计，禁止新授。
	PermFileWrite PermissionCode = "file:write"
	// PermAuditRead 查询审计事件。
	PermAuditRead PermissionCode = "audit:read"
	// PermDataExport 批量导出业务数据。
	PermDataExport PermissionCode = "data:export"
	// PermConfigRead 查看运行时配置。
	PermConfigRead PermissionCode = "config:read"
	// PermConfigWrite 修改运行时配置。
	PermConfigWrite PermissionCode = "config:write"
	// PermConfigPublish 发布配置 Release。
	PermConfigPublish PermissionCode = "config:publish"
	// PermConfigApprove 审批配置发布。
	PermConfigApprove PermissionCode = "config:approve"
	// PermConfigSecretRead 查看业务密钥元数据（不含明文）。
	PermConfigSecretRead PermissionCode = "config:secret:read" //nolint:gosec // G101: 权限码常量，非凭据。
	// PermConfigSecretWrite 创建、轮换、吊销业务密钥（明文一次性输入）。
	PermConfigSecretWrite PermissionCode = "config:secret:write" //nolint:gosec // G101: 权限码常量，非凭据。
	// PermConfigSecretApprove 审批业务密钥变更。
	PermConfigSecretApprove PermissionCode = "config:secret:approve" //nolint:gosec // G101: 权限码常量，非凭据。
	// PermTenantRead 查看租户（ENT-09）。
	PermTenantRead PermissionCode = "tenant:read"
	// PermTenantWrite 创建租户与变更租户状态（ENT-09）。
	PermTenantWrite PermissionCode = "tenant:write"
	// PermTenantApprove 入驻审核裁决（ENT-09 审核流）。
	//
	// 与 tenant:write 分开：提交申请用 tenant:write，通过 / 驳回用本权限，
	// 便于把「能建租户」与「能批准自己建的租户」拆给不同角色（职责分离）。
	PermTenantApprove PermissionCode = "tenant:approve"
	// PermNotificationRead 查询通知与投递状态（ENT-10）。
	PermNotificationRead PermissionCode = "notification:read"
	// PermNotificationWrite 代发通知、DLQ 补偿重放与模板管理（ENT-10）。
	PermNotificationWrite PermissionCode = "notification:write"
	// PermFeatureFlagRead 查询特性开关与灰度规则（ENT-11）。
	PermFeatureFlagRead PermissionCode = "featureflag:read"
	// PermFeatureFlagWrite 管理特性开关（启停 / 规则 / 到期治理）（ENT-11）。
	PermFeatureFlagWrite PermissionCode = "featureflag:write"
)
