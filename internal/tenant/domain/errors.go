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
)

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
