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
)
