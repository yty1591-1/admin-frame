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
//
// 归档：archive* 三个 map 模拟归档表，仅用于本地 / 单测验证清理任务语义，
// 不参与任何在线鉴权查询。
type MemorySessionRepository struct {
	mu       sync.RWMutex
	byID     map[string]*domain.Session
	byHash   map[string]string
	families map[string]*domain.SessionFamily

	archivedSessions  map[string]*domain.Session
	archivedFamilies  map[string]*domain.SessionFamily
	archivedSessionAt map[string]time.Time
	archivedFamilyAt  map[string]time.Time
}

// NewMemorySessionRepository 创建空的会话仓储。
func NewMemorySessionRepository() *MemorySessionRepository {
	return &MemorySessionRepository{
		byID:              make(map[string]*domain.Session),
		byHash:            make(map[string]string),
		families:          make(map[string]*domain.SessionFamily),
		archivedSessions:  make(map[string]*domain.Session),
		archivedFamilies:  make(map[string]*domain.SessionFamily),
		archivedSessionAt: make(map[string]time.Time),
		archivedFamilyAt:  make(map[string]time.Time),
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
	r.revokeFamilyLocked(familyID, reason, now)
	return revoked, nil
}

// revokeFamilyLocked 撤销设备会话；调用方须持有写锁。
func (r *MemorySessionRepository) revokeFamilyLocked(familyID string, reason domain.RevokeReason, now time.Time) {
	family, ok := r.families[familyID]
	if !ok {
		return
	}
	updated := cloneFamily(family)
	if err := updated.Revoke(reason, now); err != nil {
		// 已过期设备不可再变为 revoked，保留历史事实。
		return
	}
	r.families[familyID] = updated
}

// RevokeByAccount 撤销账号下所有 active / rotated 会话，返回受影响条数。
func (r *MemorySessionRepository) RevokeByAccount(_ context.Context, accountID string, reason domain.RevokeReason, now time.Time) (int, error) {
	if accountID == "" {
		return 0, apperr.BadRequest("account id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	revoked := 0
	for id, session := range r.byID {
		if session.AccountID != accountID {
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
	for id, family := range r.families {
		if family.AccountID != accountID {
			continue
		}
		updated := cloneFamily(family)
		if err := updated.Revoke(reason, now); err != nil {
			continue
		}
		r.families[id] = updated
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

// FindFamily 按主键加载设备会话；不存在返回 NotFound。
func (r *MemorySessionRepository) FindFamily(_ context.Context, familyID string) (*domain.SessionFamily, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	family, ok := r.families[familyID]
	if !ok {
		return nil, apperr.NotFound("session family not found")
	}
	return cloneFamily(family), nil
}

// SaveFamily 创建设备会话或使用乐观锁更新。
func (r *MemorySessionRepository) SaveFamily(_ context.Context, family *domain.SessionFamily, expectedVersion int64) error {
	if family == nil {
		return apperr.BadRequest("session family is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.families[family.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("session family id already exists")
		}
		r.families[family.ID] = cloneFamily(family)
		return nil
	}
	if !exists {
		return apperr.NotFound("session family not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("session family version conflict")
	}
	if existing.AccountID != family.AccountID {
		return apperr.BadRequest("session family account is immutable")
	}
	r.families[family.ID] = cloneFamily(family)
	return nil
}

// ListFamiliesByAccount 按 last_used_at / id 倒序返回设备会话。
func (r *MemorySessionRepository) ListFamiliesByAccount(
	_ context.Context,
	accountID string,
	afterLastUsed time.Time,
	afterID string,
	limit int,
) ([]*domain.SessionFamily, error) {
	if limit <= 0 {
		limit = 20
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.SessionFamily, 0, limit)
	for _, family := range r.families {
		if family.AccountID != accountID || family.Status != domain.SessionStatusActive {
			continue
		}
		if !afterLastUsed.IsZero() {
			if family.LastUsedAt.After(afterLastUsed) {
				continue
			}
			if family.LastUsedAt.Equal(afterLastUsed) && (afterID == "" || family.ID >= afterID) {
				continue
			}
		}
		out = append(out, cloneFamily(family))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastUsedAt.Equal(out[j].LastUsedAt) {
			return out[i].LastUsedAt.After(out[j].LastUsedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListByFamily 返回 family 下全部会话，按创建时间倒序、ID 升序。
func (r *MemorySessionRepository) ListByFamily(_ context.Context, familyID string) ([]*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.Session, 0, 4)
	for _, session := range r.byID {
		if session.FamilyID != familyID {
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

// ExpireStale 把到期的会话与设备标记为 expired，单次最多 limit 条。
func (r *MemorySessionRepository) ExpireStale(_ context.Context, now time.Time, limit int) (domain.SessionExpireResult, error) {
	if limit <= 0 {
		limit = 500
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var result domain.SessionExpireResult
	for id, session := range r.byID {
		if result.Sessions >= limit {
			break
		}
		if session.Status != domain.SessionStatusActive && session.Status != domain.SessionStatusRotated {
			continue
		}
		updated := cloneSession(session)
		if err := updated.Expire(now); err != nil {
			continue
		}
		r.byID[id] = updated
		result.Sessions++
	}
	for id, family := range r.families {
		if result.Families >= limit {
			break
		}
		updated := cloneFamily(family)
		if err := updated.Expire(now); err != nil {
			continue
		}
		r.families[id] = updated
		result.Families++
	}
	return result, nil
}

// DeleteExpiredBefore 删除 before 之前的 rotated / revoked / expired 会话与设备。
func (r *MemorySessionRepository) DeleteExpiredBefore(_ context.Context, before time.Time, limit int) (domain.SessionDeleteResult, error) {
	if limit <= 0 {
		limit = 500
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var result domain.SessionDeleteResult
	for id, session := range r.byID {
		if result.Sessions >= limit {
			break
		}
		if session.Status == domain.SessionStatusActive {
			continue
		}
		if !session.UpdatedAt.Before(before) {
			continue
		}
		delete(r.byID, id)
		delete(r.byHash, session.RefreshHash)
		result.Sessions++
	}
	for id, family := range r.families {
		if result.Families >= limit {
			break
		}
		if family.Status == domain.SessionStatusActive {
			continue
		}
		if !family.UpdatedAt.Before(before) {
			continue
		}
		delete(r.families, id)
		result.Families++
	}
	return result, nil
}

// ArchiveExpiredBefore 在内存中模拟「归档 + 删除」的原子迁移，行为与 SQL 实现一致。
func (r *MemorySessionRepository) ArchiveExpiredBefore(
	_ context.Context,
	before time.Time,
	limit int,
	archivedAt time.Time,
) (domain.SessionDeleteResult, error) {
	if limit <= 0 {
		limit = 500
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var result domain.SessionDeleteResult
	for id, session := range r.byID {
		if result.Sessions >= limit {
			break
		}
		if session.Status == domain.SessionStatusActive || !session.UpdatedAt.Before(before) {
			continue
		}
		r.archivedSessions[id] = cloneSession(session)
		r.archivedSessionAt[id] = archivedAt
		delete(r.byID, id)
		delete(r.byHash, session.RefreshHash)
		result.Sessions++
	}
	for id, family := range r.families {
		if result.Families >= limit {
			break
		}
		if family.Status == domain.SessionStatusActive || !family.UpdatedAt.Before(before) {
			continue
		}
		r.archivedFamilies[id] = cloneFamily(family)
		r.archivedFamilyAt[id] = archivedAt
		delete(r.families, id)
		result.Families++
	}
	return result, nil
}

// DeleteArchivedBefore 删除归档时间早于 before 的历史记录，行为与 SQL 实现一致。
func (r *MemorySessionRepository) DeleteArchivedBefore(
	_ context.Context,
	before time.Time,
	limit int,
) (domain.SessionDeleteResult, error) {
	if limit <= 0 {
		limit = 500
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var result domain.SessionDeleteResult
	for id, archivedAt := range r.archivedSessionAt {
		if result.Sessions >= limit {
			break
		}
		if !archivedAt.Before(before) {
			continue
		}
		delete(r.archivedSessions, id)
		delete(r.archivedSessionAt, id)
		result.Sessions++
	}
	for id, archivedAt := range r.archivedFamilyAt {
		if result.Families >= limit {
			break
		}
		if !archivedAt.Before(before) {
			continue
		}
		delete(r.archivedFamilies, id)
		delete(r.archivedFamilyAt, id)
		result.Families++
	}
	return result, nil
}

// cloneFamily 返回设备会话深拷贝，隔离可空时间字段。
func cloneFamily(family *domain.SessionFamily) *domain.SessionFamily {
	if family == nil {
		return nil
	}
	cp := *family
	cp.RevokedAt = cloneTime(family.RevokedAt)
	return &cp
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
