package persistence

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemorySessionRepository 是基于内存 map 的会话仓储。
//
// 并发安全：内部使用 RWMutex；refresh_token_hash 全局唯一，
// account_id / family_id / refresh_token_hash 创建后不可变。
type MemorySessionRepository struct {
	mu     sync.RWMutex
	byID   map[string]*domain.Session
	byHash map[string]string
}

// NewMemorySessionRepository 创建空的会话仓储。
func NewMemorySessionRepository() *MemorySessionRepository {
	return &MemorySessionRepository{
		byID:   make(map[string]*domain.Session),
		byHash: make(map[string]string),
	}
}

// FindByID 按主键加载会话；不存在返回 NotFound。
func (r *MemorySessionRepository) FindByID(_ context.Context, id string) (*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.byID[id]
	if !ok {
		return nil, apperr.NotFound("session not found")
	}
	return cloneSession(session), nil
}

// FindByRefreshHash 按刷新令牌摘要加载会话；不存在返回 NotFound。
func (r *MemorySessionRepository) FindByRefreshHash(_ context.Context, hash string) (*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byHash[hash]
	if !ok {
		return nil, apperr.NotFound("session not found")
	}
	return cloneSession(r.byID[id]), nil
}

// Save 创建或更新会话；expectedVersion 为 0 表示创建。
func (r *MemorySessionRepository) Save(_ context.Context, session *domain.Session, expectedVersion int64) error {
	if session == nil {
		return apperr.BadRequest("session is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.byID[session.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("session id already exists")
		}
		if _, dup := r.byHash[session.RefreshHash]; dup {
			return apperr.Conflict("refresh token hash already exists")
		}
		r.byID[session.ID] = cloneSession(session)
		r.byHash[session.RefreshHash] = session.ID
		return nil
	}
	if !exists {
		return apperr.NotFound("session not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("session version conflict")
	}
	if existing.AccountID != session.AccountID || existing.FamilyID != session.FamilyID ||
		existing.RefreshHash != session.RefreshHash {
		return apperr.BadRequest("session account, family and refresh hash are immutable")
	}
	r.byID[session.ID] = cloneSession(session)
	return nil
}

// RevokeFamily 撤销一个家庭下所有 active / rotated 会话，返回受影响条数。
func (r *MemorySessionRepository) RevokeFamily(_ context.Context, familyID string, reason domain.RevokeReason, now time.Time) (int, error) {
	if familyID == "" {
		return 0, apperr.BadRequest("family id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	revoked := 0
	for id, session := range r.byID {
		if session.FamilyID != familyID {
			continue
		}
		if session.Status != domain.SessionStatusActive && session.Status != domain.SessionStatusRotated {
			continue
		}
		updated := cloneSession(session)
		if err := updated.Revoke(reason, now); err != nil {
			return revoked, err
		}
		r.byID[id] = updated
		revoked++
	}
	return revoked, nil
}

// ListByAccount 返回账号全部会话，按创建时间倒序、ID 升序保证输出稳定。
func (r *MemorySessionRepository) ListByAccount(_ context.Context, accountID string) ([]*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.Session, 0, 4)
	for _, session := range r.byID {
		if session.AccountID != accountID {
			continue
		}
		out = append(out, cloneSession(session))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// cloneSession 返回会话深拷贝，隔离可空时间字段。
func cloneSession(session *domain.Session) *domain.Session {
	if session == nil {
		return nil
	}
	cp := *session
	cp.RevokedAt = cloneTime(session.RevokedAt)
	return &cp
}
