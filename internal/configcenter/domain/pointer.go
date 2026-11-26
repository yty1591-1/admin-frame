package domain

import (
	"fmt"
	"time"
)

// Pointer 是 (env, namespace) 当前生效的发布指针。
//
// 发布与回滚只改本表：active_release_id 指向不可变 Release，
// version 为 CAS 版本，切换失败返回 ErrPointerConflict。
type Pointer struct {
	Env             string
	Namespace       string
	ActiveReleaseID int64
	Version         int64
	UpdatedAt       time.Time
}

// Validate 校验指针的不变量。
func (p Pointer) Validate() error {
	if !ValidEnv(p.Env) {
		return fmt.Errorf("configcenter: invalid env %q", p.Env)
	}
	if !ValidNamespace(p.Namespace) {
		return fmt.Errorf("configcenter: invalid namespace %q", p.Namespace)
	}
	if p.ActiveReleaseID <= 0 {
		return fmt.Errorf("configcenter: active_release_id is required")
	}
	if p.Version < 1 {
		return fmt.Errorf("configcenter: pointer version must be >= 1")
	}
	if p.UpdatedAt.IsZero() {
		return fmt.Errorf("configcenter: updated_at is required")
	}
	return nil
}
