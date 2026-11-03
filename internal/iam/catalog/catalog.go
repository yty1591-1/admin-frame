// Package catalog 维护 IAM 权限点目录的唯一真相，并提供目录与常量、种子迁移、
// 文档三处一致性的校验入口。
//
// 目录只描述「有哪些权限点」，不存放角色授权关系；授权关系在 iam_role_permission。
// 新增 / 废弃权限点必须同时修改本文件、internal/iam/perm.go、
// migrations/*_seed_*.up.sql（追加式种子，禁止改写已应用的种子文件）
// 与 docs/iam/PERMISSIONS.md；四者不一致时本包的单元测试失败。
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
)

// Channel 表示权限点可授予的端别。
type Channel string

// 端别取值。both 表示 A / B 两端均可授予，由授权时的端别校验决定实际范围。
const (
	// ChannelAdmin 仅 A 端（平台）可授予。
	ChannelAdmin Channel = "admin"
	// ChannelBiz 仅 B 端（商户）可授予。
	ChannelBiz Channel = "biz"
	// ChannelBoth A / B 两端均可授予。
	ChannelBoth Channel = "both"
)

// Status 表示权限点生命周期状态。
type Status string

// 权限点状态取值。
const (
	// StatusActive 可用，可继续授予。
	StatusActive Status = "active"
	// StatusDeprecated 已弃用，保留既有授权但禁止新授。
	StatusDeprecated Status = "deprecated"
)

// codePattern 约束权限码：全小写，2～4 段，仅字母、数字、下划线。
//
// 不允许通配符 "*"：超级权限只能由 breakglass 角色在审批有效期内动态展开，
// 不能以目录中的一条通配记录表达（否则一次误授权即成为永久超管）。
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*(?::[a-z][a-z0-9_]*){0,2}$`)

// Entry 是权限点目录中的一条记录。
//
// 字段与 iam_permission 表列一一对应；Resource 由 Code 派生且库内为生成列，
// 不单独写入，避免目录内部自相矛盾。
type Entry struct {
	// Code 权限点稳定标识，对应 iam_permission.code。
	Code iam.PermissionCode
	// Name 显示名，对应 iam_permission.name。
	Name string
	// Channel 可授予端别，对应 iam_permission.channel。
	Channel Channel
	// DataBearing 是否涉及数据范围，对应 iam_permission.data_bearing。
	DataBearing bool
	// Status 生命周期状态，对应 iam_permission.status。
	Status Status
	// Sort 排序值，对应 iam_permission.sort。
	Sort int
	// Description 设计说明，仅用于文档与 Review，不落库。
	Description string
}

// Resource 返回权限码首段，用于数据范围匹配。
func (e Entry) Resource() string {
	return strings.SplitN(string(e.Code), ":", 2)[0]
}

// Validate 校验单条目录记录的自洽性。
//
// 只做与其它条目无关的检查；唯一性与排序由 Validate 负责。
func (e Entry) Validate() error {
	code := string(e.Code)
	if code == "" {
		return errors.New("权限码不能为空")
	}
	if len(code) > 128 {
		return fmt.Errorf("权限码 %q 超过 128 字节（iam_permission.code 列宽）", code)
	}
	if !codePattern.MatchString(code) {
		return fmt.Errorf("权限码 %q 不合法：须全小写、2～4 段、仅 [a-z0-9_]，且禁止通配符", code)
	}
	if e.Name == "" {
		return fmt.Errorf("权限码 %q 缺少显示名（iam_permission.name NOT NULL）", code)
	}
	if len(e.Name) > 128 {
		return fmt.Errorf("权限码 %q 的显示名超过 128 字节", code)
	}
	switch e.Channel {
	case ChannelAdmin, ChannelBiz, ChannelBoth:
	default:
		return fmt.Errorf("权限码 %q 的 channel=%q 非法：仅允许 admin / biz / both", code, e.Channel)
	}
	switch e.Status {
	case StatusActive, StatusDeprecated:
	default:
		return fmt.Errorf("权限码 %q 的 status=%q 非法：仅允许 active / deprecated", code, e.Status)
	}
	if e.Sort < 0 {
		return fmt.Errorf("权限码 %q 的 sort=%d 不得为负", code, e.Sort)
	}
	return nil
}

// entries 是权限点目录的唯一真相，顺序即授权页排序（Sort 必须严格递增）。
//
// 变更要求：新增条目只能追加，Sort 取当前最大值 + 10；不得调整既有条目的
// Code / Channel / DataBearing，否则视为破坏性变更，须走 ADR 与数据迁移。
var entries = []Entry{
	{
		Code:        iam.PermPermissionRead,
		Name:        "查看权限目录",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        10,
		Description: "读取权限点目录，供菜单与授权页展示；不含角色授权关系。",
	},
	{
		Code:        iam.PermRoleRead,
		Name:        "查看角色",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        20,
		Description: "查看角色定义与已授予权限；B 端限本商户。",
	},
	{
		Code:        iam.PermRoleWrite,
		Name:        "管理角色",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        30,
		Description: "创建、更新、停用、软删角色；不包含授权。",
	},
	{
		Code:        iam.PermRoleGrant,
		Name:        "授予角色权限",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        40,
		Description: "替换角色权限点集合；强制 Idempotency-Key 并写审计。",
	},
	{
		Code:        iam.PermBindingRead,
		Name:        "查看绑定",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        50,
		Description: "查看主体与角色的绑定关系；B 端限本商户。",
	},
	{
		Code:        iam.PermBindingWrite,
		Name:        "管理绑定",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        60,
		Description: "绑定或解绑主体角色；重复绑定幂等，解绑幂等。",
	},
	{
		Code:        iam.PermCheck,
		Name:        "模拟鉴权",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        70,
		Description: "以指定主体与资源模拟鉴权结果，用于排障与灰度比对；不改变授权。",
	},
	{
		Code:        iam.PermChangeRead,
		Name:        "查看审批单",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        80,
		Description: "查看高风险变更审批单与审批记录。",
	},
	{
		Code:        iam.PermChangeApprove,
		Name:        "审批变更",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        90,
		Description: "审批角色 / 绑定变更；申请人不得自审。",
	},
	{
		Code:        iam.PermBreakglassRequest,
		Name:        "申请破玻璃",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        100,
		Description: "申请限时超级权限；必须填写原因与有效期，默认上限 2 小时。",
	},
	{
		Code:        iam.PermBreakglassApprove,
		Name:        "审批破玻璃",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        110,
		Description: "审批破玻璃申请；与申请人强制分离并触发高危告警。",
	},
	{
		Code:        iam.PermOrderRead,
		Name:        "查询订单",
		Channel:     ChannelBoth,
		DataBearing: true,
		Status:      StatusDeprecated,
		Sort:        120,
		Description: "演示域已移除；保留既有授权，禁止新授。",
	},
	{
		Code:        iam.PermOrderWrite,
		Name:        "变更订单",
		Channel:     ChannelBoth,
		DataBearing: true,
		Status:      StatusDeprecated,
		Sort:        130,
		Description: "演示域已移除；保留既有授权，禁止新授。",
	},
	{
		Code:        iam.PermFileRead,
		Name:        "查看文件",
		Channel:     ChannelBoth,
		DataBearing: true,
		Status:      StatusDeprecated,
		Sort:        140,
		Description: "演示域已移除；保留既有授权，禁止新授。",
	},
	{
		Code:        iam.PermFileWrite,
		Name:        "管理文件",
		Channel:     ChannelBoth,
		DataBearing: true,
		Status:      StatusDeprecated,
		Sort:        150,
		Description: "演示域已移除；保留既有授权，禁止新授。",
	},
	{
		Code:        iam.PermAuditRead,
		Name:        "查询审计",
		Channel:     ChannelAdmin,
		DataBearing: true,
		Status:      StatusActive,
		Sort:        160,
		Description: "查询审计事件；敏感字段脱敏，批量导出需额外 data:export。",
	},
	{
		Code:        iam.PermDataExport,
		Name:        "导出数据",
		Channel:     ChannelBoth,
		DataBearing: true,
		Status:      StatusActive,
		Sort:        170,
		Description: "批量导出业务数据；必须限流并记录导出审计。",
	},
	{
		Code:        iam.PermConfigRead,
		Name:        "查看配置",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        180,
		Description: "查看运行时配置；敏感项脱敏。",
	},
	{
		Code:        iam.PermConfigWrite,
		Name:        "修改配置",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        190,
		Description: "修改运行时配置；须审计并支持回滚。",
	},
	{
		Code:        iam.PermChangeRequest,
		Name:        "发起变更申请",
		Channel:     ChannelBiz,
		Status:      StatusActive,
		Sort:        200,
		Description: "以本商户身份发起高风险变更申请，并查看本商户审批单（仅 B 端）。",
	},
	{
		Code:        iam.PermChangeReview,
		Name:        "审批本商户变更",
		Channel:     ChannelBiz,
		Status:      StatusActive,
		Sort:        210,
		Description: "审批本商户变更申请；申请人不得自审（职责分离）。",
	},
	{
		Code:        iam.PermMenuRead,
		Name:        "查看菜单资源",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        220,
		Description: "查看菜单 / 路由资源管理列表；不改变前端可见性。",
	},
	{
		Code:        iam.PermMenuWrite,
		Name:        "管理菜单资源",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        230,
		Description: "创建、更新、停用菜单资源；菜单仅影响入口展示，不构成接口鉴权边界。",
	},
	{
		Code:        iam.PermSodRead,
		Name:        "查看互斥组",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        240,
		Description: "查看 RBAC2 互斥角色组及其存量违规；B 端限本商户。",
	},
	{
		Code:        iam.PermSodWrite,
		Name:        "管理互斥组",
		Channel:     ChannelBoth,
		Status:      StatusActive,
		Sort:        250,
		Description: "创建、更新、停用互斥角色组；变更前强制存量违规预检。",
	},
	{
		Code:        iam.PermIdentityAccountRead,
		Name:        "查看身份账号",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        255,
		Description: "查询账号列表 / 详情与目标账号会话；不包含任何处置动作。",
	},
	{
		Code:        iam.PermIdentityAccountWrite,
		Name:        "管理身份账号",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        260,
		Description: "停用、启用、锁定、解锁与恢复身份账号；不包含凭据重置、删除与会话撤销。",
	},
	{
		Code:        iam.PermIdentityAccountCredential,
		Name:        "重置身份凭据",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        265,
		Description: "重置目标账号密码或 MFA；属于账号接管高风险动作，必须与生命周期权限分离。",
	},
	{
		Code:        iam.PermIdentityAccountDelete,
		Name:        "删除身份账号",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        270,
		Description: "软删目标账号；不可逆操作入口，不包含恢复与会话撤销。",
	},
	{
		Code:        iam.PermIdentityAccountSession,
		Name:        "撤销身份会话",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        275,
		Description: "撤销目标账号指定会话或全部会话；不包含凭据与生命周期变更。",
	},
	{
		Code:        iam.PermTenantRead,
		Name:        "查看租户",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        285,
		Description: "查询租户列表与详情（含状态与状态原因）；不包含任何状态变更。",
	},
	{
		Code:        iam.PermTenantWrite,
		Name:        "管理租户",
		Channel:     ChannelAdmin,
		Status:      StatusActive,
		Sort:        295,
		Description: "创建租户与迁移租户状态（启用 / 停用 / 恢复 / 终止）；每次迁移写审计。",
	},
}

// All 返回目录副本，调用方可自由排序 / 过滤而不影响包内状态。
func All() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// Codes 返回全部权限码，顺序与 All 一致。
func Codes() []iam.PermissionCode {
	out := make([]iam.PermissionCode, len(entries))
	for i, e := range entries {
		out[i] = e.Code
	}
	return out
}

// ByCode 按权限码查找目录条目。
func ByCode(code iam.PermissionCode) (Entry, bool) {
	for _, e := range entries {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}

// Validate 校验整个目录：逐条自洽、权限码唯一、排序严格递增、父节点存在。
//
// 该函数同时被启动期校验与单元测试调用；错误信息必须直接可行动，
// 指出具体权限码与修复方向。
func Validate() error {
	if len(entries) == 0 {
		return errors.New("权限目录为空，至少需要一条基线权限点")
	}
	seen := make(map[iam.PermissionCode]int, len(entries))
	prevSort := -1
	for i, e := range entries {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("目录第 %d 条非法: %w", i+1, err)
		}
		if first, ok := seen[e.Code]; ok {
			return fmt.Errorf("权限码 %q 重复（第 %d 条与第 %d 条）", e.Code, first+1, i+1)
		}
		seen[e.Code] = i
		if e.Sort <= prevSort {
			return fmt.Errorf("权限码 %q 的 sort=%d 未严格递增（前一条为 %d），目录顺序必须稳定", e.Code, e.Sort, prevSort)
		}
		prevSort = e.Sort
	}
	return nil
}
