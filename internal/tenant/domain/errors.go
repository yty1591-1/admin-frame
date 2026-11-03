package domain

import (
	"errors"
	"fmt"
)

// 领域错误；应用层据此翻译为 HTTP 语义，仓储实现也复用同一组哨兵。
var (
	// ErrNotFound 租户不存在。
	ErrNotFound = errors.New("tenant: not found")
	// ErrNameRequired 租户名称必填。
	ErrNameRequired = errors.New("tenant: name is required")
	// ErrNameTooLong 租户名称超长。
	ErrNameTooLong = fmt.Errorf("tenant: name must be at most %d characters", MaxNameLength)
	// ErrReasonRequired 停用 / 终止缺少原因。
	ErrReasonRequired = errors.New("tenant: reason is required for suspend and terminate")
	// ErrReasonTooLong 状态原因超长。
	ErrReasonTooLong = fmt.Errorf("tenant: reason must be at most %d characters", MaxReasonLength)
	// ErrTerminated 租户已终止，不可再迁移。
	ErrTerminated = errors.New("tenant: tenant is terminated")
	// ErrVersionConflict 并发迁移冲突（乐观锁失败）。
	ErrVersionConflict = errors.New("tenant: concurrent status change detected")
	// ErrAlreadyExists 租户 ID 已存在。
	ErrAlreadyExists = errors.New("tenant: tenant already exists")
	// ErrSubjectRequired 成员主体 ID 必填。
	ErrSubjectRequired = errors.New("tenant: member subject id is required")
	// ErrSubjectTooLong 成员主体 ID 超长。
	ErrSubjectTooLong = fmt.Errorf("tenant: member subject id must be at most %d characters", MaxSubjectIDLength)
	// ErrMemberNotFound 成员不存在。
	ErrMemberNotFound = errors.New("tenant: member not found")
	// ErrMemberAlreadyExists 成员已在册，重复添加视为冲突。
	ErrMemberAlreadyExists = errors.New("tenant: member already active in tenant")
	// ErrPlanNotFound 指定编码的套餐不存在。
	ErrPlanNotFound = errors.New("tenant: plan not found")
	// ErrPlanCodeRequired 套餐编码必填。
	ErrPlanCodeRequired = errors.New("tenant: plan code is required")
	// ErrPlanCodeTooLong 套餐编码超长。
	ErrPlanCodeTooLong = errors.New("tenant: plan code exceeds length limit")
	// ErrNoDefaultPlan 目录中缺少默认套餐（迁移 000012 保证存在；缺失属部署事故）。
	ErrNoDefaultPlan = errors.New("tenant: no default plan configured")
	// ErrNoOwner 开通前置：租户至少要有一名在册 owner 才能开通（T-12）。
	ErrNoOwner = errors.New("tenant: activation requires at least one active owner member")
	// ErrLastOwner 租户至少保留一名在册 owner。
	ErrLastOwner = errors.New("tenant: tenant must keep at least one active owner")
	// ErrResourceRequired 配额资源键必填。
	ErrResourceRequired = errors.New("tenant: quota resource is required")
	// ErrResourceTooLong 配额资源键超长。
	ErrResourceTooLong = fmt.Errorf("tenant: quota resource must be at most %d characters", MaxResourceLength)
	// ErrCodeRequired 租户业务编码必填（T-11）。
	ErrCodeRequired = errors.New("tenant: code is required")
	// ErrCodeTooLong 租户业务编码超长。
	ErrCodeTooLong = fmt.Errorf("tenant: code must be at most %d characters", MaxCodeLength)
	// ErrCodeExists 租户业务编码已被占用（uk_tenant_code）。
	ErrCodeExists = errors.New("tenant: code already exists")
	// ErrSeatLimitExceeded 席位已满（T-13）；统一 409，当前值与上限由错误详情携带。
	ErrSeatLimitExceeded = errors.New("tenant: member seat limit exceeded")
	// ErrInvitationNotFound 邀请不存在。
	ErrInvitationNotFound = errors.New("tenant: invitation not found")
	// ErrInvitationNotPending 邀请不在待处理状态，token 不可用。
	ErrInvitationNotPending = errors.New("tenant: invitation is not pending")
	// ErrInvitationExpired 邀请已过期，token 不可用。
	ErrInvitationExpired = errors.New("tenant: invitation is expired")
	// ErrInvitationEmailRequired 被邀请邮箱必填。
	ErrInvitationEmailRequired = errors.New("tenant: invitation email is required")
	// ErrInvitationEmailTooLong 被邀请邮箱超长。
	ErrInvitationEmailTooLong = fmt.Errorf("tenant: invitation email must be at most %d characters", MaxInvitationEmailLength)
	// ErrInvitationPendingExists 同租户同邮箱已存在待处理邀请（引导走 resend）。
	ErrInvitationPendingExists = errors.New("tenant: a pending invitation already exists for this email")
	// ErrInvitationAlreadyAccepted 邀请已被其他主体接受。
	ErrInvitationAlreadyAccepted = errors.New("tenant: invitation already accepted")
)

// InvalidRoleError 表示未定义的成员角色。
type InvalidRoleError struct {
	// Role 非法角色取值。
	Role string
}

// Error 实现 error。
func (e *InvalidRoleError) Error() string {
	return fmt.Sprintf("tenant: unsupported member role %q", e.Role)
}

// InvalidResourceError 表示非法的配额资源键。
type InvalidResourceError struct {
	// Resource 非法资源键。
	Resource string
}

// Error 实现 error。
func (e *InvalidResourceError) Error() string {
	return fmt.Sprintf("tenant: invalid quota resource %q (allowed: a-z 0-9 . _ -)", e.Resource)
}

// InvalidStatusError 表示未定义的成员状态。
//
// 与 InvalidRoleError 分开建模：调用方（HTTP 层）需要把它翻译成 400，
// 而仓储回读到的脏数据仍应是 500。因此只有入参校验路径返回本错误，
// Member.Validate（脏数据防御）保持裸错误。
type InvalidStatusError struct {
	// Status 非法状态取值。
	Status string
}

// Error 实现 error。
func (e *InvalidStatusError) Error() string {
	return fmt.Sprintf("tenant: unsupported member status %q", e.Status)
}

// InvalidTransitionError 表示状态机不允许的迁移。
type InvalidTransitionError struct {
	// From 当前状态。
	From Status
	// To 目标状态。
	To Status
}

// Error 实现 error；错误信息同时给出两个状态，便于直接定位误操作。
func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("tenant: cannot transition from %s to %s", e.From, e.To)
}

// InvalidCodeError 表示非法的租户业务编码（T-11）。
type InvalidCodeError struct {
	// Code 非法编码取值。
	Code string
}

// Error 实现 error。
func (e *InvalidCodeError) Error() string {
	return fmt.Sprintf("tenant: invalid code %q (allowed: a-z 0-9 . _ -)", e.Code)
}

// FieldTooLongError 表示元数据字段超长（T-11）。
type FieldTooLongError struct {
	// Field 字段名（与 API 字段一致，便于前端定位）。
	Field string
	// Max 长度上限。
	Max int
}

// Error 实现 error。
func (e *FieldTooLongError) Error() string {
	return fmt.Sprintf("tenant: %s must be at most %d characters", e.Field, e.Max)
}

// InvalidEmailError 表示联系邮箱格式非法（T-11）。
type InvalidEmailError struct {
	// Email 非法邮箱取值。
	Email string
}

// Error 实现 error。
func (e *InvalidEmailError) Error() string {
	return fmt.Sprintf("tenant: invalid contact email %q", e.Email)
}

// InvalidFieldError 表示字段取值包含非法字符（T-11，时区 / 语言标签）。
type InvalidFieldError struct {
	// Field 字段名。
	Field string
	// Value 非法取值。
	Value string
}

// Error 实现 error。
func (e *InvalidFieldError) Error() string {
	return fmt.Sprintf("tenant: invalid %s %q", e.Field, e.Value)
}

// SeatLimitError 表示席位已满（T-13）：409 响应需附当前值与上限，
// 让运营能直接判断是「清停用成员」还是「提配额」。
type SeatLimitError struct {
	// Active 当前在册成员数。
	Active int
	// Limit 席位上限（tenant_quota 的 user 资源）。
	Limit int64
}

// Error 实现 error。
func (e *SeatLimitError) Error() string {
	return fmt.Sprintf("%s: active %d, limit %d", ErrSeatLimitExceeded, e.Active, e.Limit)
}

// Unwrap 挂接哨兵，调用方可 errors.Is(err, domain.ErrSeatLimitExceeded)。
func (e *SeatLimitError) Unwrap() error {
	return ErrSeatLimitExceeded
}
