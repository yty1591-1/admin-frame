package persistence

import (
	"context"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryLoginAttemptRepository 是基于切片的登录尝试仓储，只追加、不更新。
type MemoryLoginAttemptRepository struct {
	mu       sync.RWMutex
	attempts []domain.LoginAttempt
}

// NewMemoryLoginAttemptRepository 创建空的登录尝试仓储。
func NewMemoryLoginAttemptRepository() *MemoryLoginAttemptRepository {
	return &MemoryLoginAttemptRepository{}
}

// Append 追加一条登录尝试。
func (r *MemoryLoginAttemptRepository) Append(_ context.Context, attempt *domain.LoginAttempt) error {
	if attempt == nil {
		return apperr.BadRequest("login attempt is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, *attempt)
	return nil
}

// CountFailuresSince 统计 since（含）之后指定账号摘要的失败次数。
func (r *MemoryLoginAttemptRepository) CountFailuresSince(_ context.Context, channel domain.Channel, accountRef string, since time.Time) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, attempt := range r.attempts {
		if attempt.Channel != channel || attempt.AccountRef != accountRef {
			continue
		}
		if attempt.Outcome != domain.LoginOutcomeFailure {
			continue
		}
		if attempt.OccurredAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

// LastSuccessAt 返回最近一次成功登录时间；没有记录时 found 为 false。
func (r *MemoryLoginAttemptRepository) LastSuccessAt(_ context.Context, channel domain.Channel, accountRef string) (time.Time, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest time.Time
	found := false
	for _, attempt := range r.attempts {
		if attempt.Channel != channel || attempt.AccountRef != accountRef {
			continue
		}
		if attempt.Outcome != domain.LoginOutcomeSuccess {
			continue
		}
		if !found || attempt.OccurredAt.After(latest) {
			latest = attempt.OccurredAt
			found = true
		}
	}
	return latest, found, nil
}
