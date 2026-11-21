package domain

import (
	"context"
	"fmt"
	"time"
)

// InstallStatus 是首次安装状态机（ADR-0017）。
type InstallStatus string

const (
	InstallPending    InstallStatus = "pending"
	InstallInstalling InstallStatus = "installing"
	InstallReady      InstallStatus = "ready"
	InstallFailed     InstallStatus = "failed"
)

// ValidInstallStatus 报告 status 是否为已知枚举。
func ValidInstallStatus(s InstallStatus) bool {
	switch s {
	case InstallPending, InstallInstalling, InstallReady, InstallFailed:
		return true
	default:
		return false
	}
}

// InstallState 是某环境下的安装态行。
type InstallState struct {
	Env         string
	Status      InstallStatus
	Owner       string
	LeaseUntil  *time.Time
	Version     int64
	LastError   string
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// Validate 校验安装态不变量。
func (s InstallState) Validate() error {
	if !ValidEnv(s.Env) {
		return fmt.Errorf("configcenter: invalid install env %q", s.Env)
	}
	if !ValidInstallStatus(s.Status) {
		return fmt.Errorf("configcenter: invalid install status %q", s.Status)
	}
	if s.Version < 1 {
		return fmt.Errorf("configcenter: install version must be >= 1")
	}
	if s.UpdatedAt.IsZero() {
		return fmt.Errorf("configcenter: install updated_at is required")
	}
	return nil
}

// LeaseActive 报告租约在 now 时是否仍有效。
func (s InstallState) LeaseActive(now time.Time) bool {
	if s.Status != InstallInstalling {
		return false
	}
	if s.Owner == "" || s.LeaseUntil == nil {
		return false
	}
	return s.LeaseUntil.After(now)
}

// InstallRepository 是安装态持久化端口。
type InstallRepository interface {
	Get(ctx context.Context, env string) (InstallState, error)
	// Ensure 在 env 无行时插入初始状态；已存在则返回当前行（幂等）。
	Ensure(ctx context.Context, state InstallState) (InstallState, error)
	// CompareAndSwap 以 expectedVersion 做 CAS；成功返回新行，版本冲突返回 ErrVersionConflict。
	CompareAndSwap(ctx context.Context, next InstallState, expectedVersion int64) (InstallState, error)
}
