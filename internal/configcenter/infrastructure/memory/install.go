package memory

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// InstallRepository 是 domain.InstallRepository 的进程内实现。
type InstallRepository struct {
	store *store
}

var _ domain.InstallRepository = (*InstallRepository)(nil)

// Get 返回安装态；不存在返回 ErrNotFound。
func (r *InstallRepository) Get(_ context.Context, env string) (domain.InstallState, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	state, ok := r.store.installs[env]
	if !ok {
		return domain.InstallState{}, domain.ErrNotFound
	}
	return cloneInstall(state), nil
}

// Ensure 幂等插入初始行。
func (r *InstallRepository) Ensure(_ context.Context, state domain.InstallState) (domain.InstallState, error) {
	if err := state.Validate(); err != nil {
		return domain.InstallState{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if existing, ok := r.store.installs[state.Env]; ok {
		return cloneInstall(existing), nil
	}
	r.store.installs[state.Env] = cloneInstall(state)
	return cloneInstall(state), nil
}

// CompareAndSwap 以 expectedVersion 做 CAS。
func (r *InstallRepository) CompareAndSwap(_ context.Context, next domain.InstallState, expectedVersion int64) (domain.InstallState, error) {
	if err := next.Validate(); err != nil {
		return domain.InstallState{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	cur, ok := r.store.installs[next.Env]
	if !ok {
		return domain.InstallState{}, domain.ErrNotFound
	}
	if cur.Version != expectedVersion {
		return domain.InstallState{}, domain.ErrVersionConflict
	}
	next.Version = expectedVersion + 1
	r.store.installs[next.Env] = cloneInstall(next)
	return cloneInstall(next), nil
}

func cloneInstall(s domain.InstallState) domain.InstallState {
	s.LeaseUntil = cloneTime(s.LeaseUntil)
	s.CompletedAt = cloneTime(s.CompletedAt)
	return s
}
