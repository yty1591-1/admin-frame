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
