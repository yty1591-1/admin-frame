// Package domain 定义租户域实体、状态机与仓储契约（ENT-09）。
//
// 本包不依赖仓储实现、HTTP 或 IAM：租户是隔离边界（ADR-0006），
// 状态流转规则必须能脱离基础设施单测，否则规则会散落在 handler 与 SQL 里。
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Status 是租户生命周期状态。
type Status string

// 租户状态取值；顺序即生命周期顺序，terminated 为终态。
const (
	// StatusPending 已创建、尚未开通：不接受业务流量。
	StatusPending Status = "pending"
	// StatusActive 正常服务。
	StatusActive Status = "active"
	// StatusSuspended 停用：拒绝访问，数据保留，可恢复。
	StatusSuspended Status = "suspended"
	// StatusTerminated 已终止：终态，不可恢复；数据按策略处置。
	StatusTerminated Status = "terminated"
)

// MaxNameLength 租户名称长度上限（与 tenant.name VARCHAR(128) 对齐）。
const MaxNameLength = 128

// MaxReasonLength 状态原因长度上限（与 tenant.status_reason VARCHAR(255) 对齐）。
const MaxReasonLength = 255

// Valid 判断状态是否为已定义取值。
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusActive, StatusSuspended, StatusTerminated:
		return true
	default:
		return false
	}
}

// Terminal 判断是否为终态：终态租户不允许任何后续迁移。
func (s Status) Terminal() bool {
	return s == StatusTerminated
}

// Serving 判断该状态是否允许业务访问；停用与待开通都不放行。
func (s Status) Serving() bool {
	return s == StatusActive
}

// transitions 是状态机的唯一真相：只列合法迁移，未列出的一律拒绝。
//
// 刻意不允许 suspended → pending（回退到未开通会让已开通的租户丢失开通事实），
// 也不允许 terminated → 任意状态（终止需走新租户，避免「复活」绕过合同与结算）。
var transitions = map[Status][]Status{
	StatusPending:   {StatusActive, StatusTerminated},
	StatusActive:    {StatusSuspended, StatusTerminated},
	StatusSuspended: {StatusActive, StatusTerminated},
}

// CanTransitionTo 判断从当前状态迁移到目标状态是否合法。
func (s Status) CanTransitionTo(target Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// Tenant 是租户实体。
type Tenant struct {
	// ID 租户标识，同时是 B 端各表 merchant_id 的取值（ADR-0006）。
	ID string
	// Name 租户名称。
	Name string
	// Status 当前状态。
	Status Status
	// StatusReason 最近一次状态变更原因（审计之外的运营说明）。
	StatusReason string
	// Version 乐观锁版本；每次成功迁移 +1。
	Version int64
	// SuspendedAt 最近一次停用时间；恢复后保留，供回溯。
	SuspendedAt *time.Time
	// TerminatedAt 终止时间；非空即终态。
	TerminatedAt *time.Time
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// NewTenant 构造待开通（pending）租户；名称去空白并校验长度。
func NewTenant(id, name string, now time.Time) (Tenant, error) {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return Tenant{}, fmt.Errorf("tenant: id is required")
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return Tenant{}, ErrNameRequired
	}
	if len([]rune(trimmedName)) > MaxNameLength {
		return Tenant{}, ErrNameTooLong
	}
	return Tenant{
		ID:        trimmedID,
		Name:      trimmedName,
		Status:    StatusPending,
		Version:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// Validate 校验仓储回读的实体自洽性。
//
// 用于在应用层防御「库里有脏数据」：状态枚举非法或终态缺终止时间时，
// 宁可报错也不要把不一致的状态当作正常数据继续流转。
func (t Tenant) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("tenant: id is required")
	}
	if strings.TrimSpace(t.Name) == "" {
		return ErrNameRequired
	}
	if !t.Status.Valid() {
		return fmt.Errorf("tenant: unsupported status %q", t.Status)
	}
	if t.Status == StatusTerminated && t.TerminatedAt == nil {
		return fmt.Errorf("tenant: terminated tenant %s has no terminated_at", t.ID)
	}
	if t.Version <= 0 {
		return fmt.Errorf("tenant: version must be positive, got %d", t.Version)
	}
	return nil
}

// Transition 迁移到目标状态并递增版本号。
//
// 停用与终止必须给出原因：这两个动作会影响客户可用性与合同，不能只有操作人没有理由。
// 返回的 Tenant 是迁移后的副本，调用方需把它交给仓储做 CAS 写入。
func (t Tenant) Transition(target Status, reason string, now time.Time) (Tenant, error) {
	if !target.Valid() {
		return t, fmt.Errorf("tenant: unsupported status %q", target)
	}
	if t.Status.Terminal() {
		return t, ErrTerminated
	}
	if !t.Status.CanTransitionTo(target) {
		return t, &InvalidTransitionError{From: t.Status, To: target}
	}
	trimmedReason := strings.TrimSpace(reason)
	if (target == StatusSuspended || target == StatusTerminated) && trimmedReason == "" {
		return t, ErrReasonRequired
	}
	if len([]rune(trimmedReason)) > MaxReasonLength {
		return t, ErrReasonTooLong
	}

	next := t
	next.Status = target
	next.StatusReason = trimmedReason
	next.Version = t.Version + 1
	next.UpdatedAt = now.UTC()
	switch target {
	case StatusSuspended:
		moment := now.UTC()
		next.SuspendedAt = &moment
	case StatusTerminated:
		moment := now.UTC()
		next.TerminatedAt = &moment
	}
	return next, nil
}
