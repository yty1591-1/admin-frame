package domain

import (
	"errors"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 领域哨兵错误。调用方可用 errors.Is 精确判断失败原因，
// 同时 apperr 仍提供稳定的 HTTP 业务码映射。
var (
	// ErrInvalidChannel 端别不是 admin / biz。
	ErrInvalidChannel = errors.New("invalid channel")
	// ErrInvalidRoleScope 角色作用域与端别 / 商户不匹配。
	ErrInvalidRoleScope = errors.New("invalid role scope")
	// ErrInvalidRoleCode 角色编码格式非法。
	ErrInvalidRoleCode = errors.New("invalid role code")
	// ErrInvalidRoleParent 父角色非法：自引用、成环、跨端 / 跨商户、超深或破玻璃参与继承。
	ErrInvalidRoleParent = errors.New("invalid role parent")
	// ErrInvalidSodGroup 互斥角色组字段非法：租户组合、编码、成员数或并发阈值不合法。
	ErrInvalidSodGroup = errors.New("invalid sod group")
	// ErrInvalidRoleConstraint 角色约束非法：前置角色自引用 / 重复 / 超限，或基数上限非法。
	ErrInvalidRoleConstraint = errors.New("invalid role constraint")
	// ErrInvalidPermissionCode 权限码格式非法。
	ErrInvalidPermissionCode = errors.New("invalid permission code")
	// ErrWildcardNotAllowed 出现通配符权限码；仅 breakglass 可在运行时展开 *。
	ErrWildcardNotAllowed = errors.New("wildcard permission not allowed")
	// ErrInvalidDataScope 数据范围自身非法（类型、取值、端别限制）。
	ErrInvalidDataScope = errors.New("invalid data scope")
	// ErrMerchantSetTooLarge merchant_set 超过上限。
	ErrMerchantSetTooLarge = errors.New("merchant set too large")
	// ErrVersionConflict 乐观锁版本不匹配。
	ErrVersionConflict = errors.New("version conflict")
	// ErrRoleNotActive 角色已停用或已删除，不能继续用于授权 / 绑定。
	ErrRoleNotActive = errors.New("role not active")
	// ErrRoleDeleted 角色已软删。
	ErrRoleDeleted = errors.New("role deleted")
	// ErrBindingChannelMismatch 绑定与角色的端别 / 商户不一致（跨端越权）。
	ErrBindingChannelMismatch = errors.New("binding channel mismatch")
	// ErrInvalidBinding 绑定自身字段非法。
	ErrInvalidBinding = errors.New("invalid binding")
	// ErrBreakglassRequiresExpiry 破玻璃绑定缺少有效期或超出上限。
	ErrBreakglassRequiresExpiry = errors.New("breakglass requires bounded expiry")
	// ErrInvalidSubjectType 主体类型与端别不匹配。
	ErrInvalidSubjectType = errors.New("invalid subject type")
	// ErrBreakglassRequiresApproval 破玻璃角色 / 绑定必须走审批，禁止直接创建。
	ErrBreakglassRequiresApproval = errors.New("breakglass requires approval")
	// ErrMissingActor 写操作缺少操作人，无法满足审计要求。
	ErrMissingActor = errors.New("actor is required")
	// ErrEmptyInput 必填字段为空。
	ErrEmptyInput = errors.New("required field is empty")
	// ErrInvalidChangeStatus 审批单状态非法。
	ErrInvalidChangeStatus = errors.New("invalid change status")
	// ErrInvalidChangeAction 审批单动作不受支持。
	ErrInvalidChangeAction = errors.New("invalid change action")
	// ErrInvalidChangeTarget 审批单目标类型 / ID 与动作不匹配。
	ErrInvalidChangeTarget = errors.New("invalid change target")
	// ErrChangePayloadInvalid 审批载荷不是 JSON 对象、超限或含敏感字段。
	ErrChangePayloadInvalid = errors.New("invalid change payload")
	// ErrChangeNotPending 审批单已被处理，不能重复决定。
	ErrChangeNotPending = errors.New("change request is not pending")
	// ErrChangeNotApproved 审批单未通过，不能应用。
	ErrChangeNotApproved = errors.New("change request is not approved")
	// ErrSelfApproval 申请人自审，违反职责分离（SoD）。
	ErrSelfApproval = errors.New("requester must not approve own change")
	// ErrNotRequester 非申请人尝试撤销审批单。
	ErrNotRequester = errors.New("actor is not the requester")
	// ErrChangeTenantOverride B 端载荷试图覆盖 merchant_id（SEC-14）。
	ErrChangeTenantOverride = errors.New("change payload must not override tenant")
	// ErrInvalidChangeTenant 审批单端别与商户组合非法：A 端不得带商户，B 端必须带商户。
	ErrInvalidChangeTenant = errors.New("invalid change tenant")
	// ErrChangeExpired 审批时效已过，不能再通过。
	ErrChangeExpired = errors.New("change request expired")
	// ErrInvalidDecisionReason 审批结论说明非法；拒绝时必填。
	ErrInvalidDecisionReason = errors.New("invalid decision reason")
	// ErrInvalidRoleTemplate 角色模板字段非法。
	ErrInvalidRoleTemplate = errors.New("invalid role template")
	// ErrTemplateNotActive 角色模板已停用或已删除，不能用于实例化。
	ErrTemplateNotActive = errors.New("role template not active")
	// ErrInvalidMenu 菜单 / 路由资源字段非法。
	ErrInvalidMenu = errors.New("invalid menu")
)

// errInvalid 构造参数错误（HTTP 400 / 业务码 40000）。
//
// 使用 errors.Join 同时挂接领域哨兵与 apperr 语义哨兵，
// 使 errors.Is(err, ErrWildcardNotAllowed) 与 errors.Is(err, apperr.ErrBadRequest) 都成立。
func errInvalid(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeBadRequest, msg, errors.Join(sentinel, apperr.ErrBadRequest))
}

// errConflict 构造状态冲突错误（HTTP 409 / 业务码 40900）。
func errConflict(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeConflict, msg, errors.Join(sentinel, apperr.ErrConflict))
}

// errForbidden 构造无权限错误（HTTP 403 / 业务码 40300）。
func errForbidden(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeForbidden, msg, errors.Join(sentinel, apperr.ErrForbidden))
}
