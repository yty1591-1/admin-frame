package persistence

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// memoryScimStore 是 SCIM 令牌、组与成员关系共享的内存索引。
//
// 三者放在同一存储里，是为了让「组成员变更 + 角色投影」在内存模式下
// 也能落在同一把锁的临界区内，语义与 SQL 事务保持一致。
type memoryScimStore struct {
	mu sync.RWMutex

	tokens     map[string]*domain.ScimToken // 主键 id → 令牌
	tokenHash  map[string]string            // token_hash → 主键 id
	groups     map[string]*domain.ScimGroup // 主键 id → 组
	groupExt   map[string]string            // provider + "\x00" + external_id → 主键 id
	groupName  map[string]string            // provider + "\x00" + display_name → 主键 id
	members    map[string]map[string]time.Time
	memberOfID map[string]map[string]struct{} // account_id → group_id 集合
}

// MemoryScimTokenRepository 是内存版 SCIM 令牌仓储；只用于本地 / 单测。
type MemoryScimTokenRepository struct{ store *memoryScimStore }

// MemoryScimGroupRepository 是内存版 SCIM 组仓储；与令牌仓储共享同一存储。
type MemoryScimGroupRepository struct{ store *memoryScimStore }

// 编译期断言。
var (
	_ domain.ScimTokenRepository = (*MemoryScimTokenRepository)(nil)
	_ domain.ScimGroupRepository = (*MemoryScimGroupRepository)(nil)
)

// NewMemoryScimRepositories 创建共享同一存储的令牌与组仓储。
func NewMemoryScimRepositories() (*MemoryScimTokenRepository, *MemoryScimGroupRepository) {
	store := &memoryScimStore{
		tokens:     make(map[string]*domain.ScimToken),
		tokenHash:  make(map[string]string),
		groups:     make(map[string]*domain.ScimGroup),
		groupExt:   make(map[string]string),
		groupName:  make(map[string]string),
		members:    make(map[string]map[string]time.Time),
		memberOfID: make(map[string]map[string]struct{}),
	}
	return &MemoryScimTokenRepository{store: store}, &MemoryScimGroupRepository{store: store}
}

// Create 写入新令牌；摘要重复返回 Conflict。
func (r *MemoryScimTokenRepository) Create(_ context.Context, token *domain.ScimToken) error {
	if token == nil {
		return apperr.BadRequest("scim token is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, dup := store.tokenHash[token.TokenHash]; dup {
		return apperr.Conflict("scim token already exists")
	}
	if _, dup := store.tokens[token.ID]; dup {
		return apperr.Conflict("scim token id already exists")
	}
	store.tokens[token.ID] = cloneScimToken(token)
	store.tokenHash[token.TokenHash] = token.ID
	return nil
}

// UpdateStatus 更新令牌状态、宽限到期与吊销时间。
func (r *MemoryScimTokenRepository) UpdateStatus(_ context.Context, token *domain.ScimToken) error {
	if token == nil {
		return apperr.BadRequest("scim token is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.tokens[token.ID]; !ok {
		return apperr.NotFound("scim token not found")
	}
	store.tokens[token.ID] = cloneScimToken(token)
	return nil
}

// FindByHash 按 provider + 摘要加载令牌。
func (r *MemoryScimTokenRepository) FindByHash(_ context.Context, provider, tokenHash string) (*domain.ScimToken, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.tokenHash[strings.TrimSpace(tokenHash)]
	if !ok {
		return nil, apperr.NotFound("scim token not found")
	}
	token := store.tokens[id]
	if token.Provider != strings.TrimSpace(provider) {
		return nil, apperr.NotFound("scim token not found")
	}
	return cloneScimToken(token), nil
}

// FindByID 按主键加载令牌。
func (r *MemoryScimTokenRepository) FindByID(_ context.Context, id string) (*domain.ScimToken, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	token, ok := store.tokens[strings.TrimSpace(id)]
	if !ok {
		return nil, apperr.NotFound("scim token not found")
	}
	return cloneScimToken(token), nil
}

// ListByProvider 返回 provider 下全部令牌，按创建时间倒序。
func (r *MemoryScimTokenRepository) ListByProvider(_ context.Context, provider string) ([]*domain.ScimToken, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]*domain.ScimToken, 0, 4)
	for _, token := range store.tokens {
		if token.Provider != strings.TrimSpace(provider) {
			continue
		}
		out = append(out, cloneScimToken(token))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// FindByID 按主键加载组；软删组仍返回。
func (r *MemoryScimGroupRepository) FindByID(_ context.Context, id string) (*domain.ScimGroup, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	group, ok := store.groups[strings.TrimSpace(id)]
	if !ok {
		return nil, apperr.NotFound("scim group not found")
	}
	return cloneScimGroup(group), nil
}

// FindByExternalID 按 provider + 外部 ID 加载组。
func (r *MemoryScimGroupRepository) FindByExternalID(_ context.Context, provider, externalID string) (*domain.ScimGroup, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.groupExt[scimGroupKey(provider, externalID)]
	if !ok {
		return nil, apperr.NotFound("scim group not found")
	}
	return cloneScimGroup(store.groups[id]), nil
}

// FindByDisplayName 按 provider + 展示名加载组。
func (r *MemoryScimGroupRepository) FindByDisplayName(_ context.Context, provider, displayName string) (*domain.ScimGroup, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.groupName[scimGroupKey(provider, displayName)]
	if !ok {
		return nil, apperr.NotFound("scim group not found")
	}
	return cloneScimGroup(store.groups[id]), nil
}

// Create 创建组；唯一键冲突返回 Conflict。
func (r *MemoryScimGroupRepository) Create(_ context.Context, group *domain.ScimGroup) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, dup := store.groups[group.ID]; dup {
		return apperr.Conflict("scim group id already exists")
	}
	extKey := scimGroupKey(group.Provider, group.ExternalID)
	if _, dup := store.groupExt[extKey]; dup {
		return apperr.Conflict("scim group externalId already exists")
	}
	nameKey := scimGroupKey(group.Provider, group.DisplayName)
	if _, dup := store.groupName[nameKey]; dup {
		return apperr.Conflict("scim group displayName already exists")
	}
	store.groups[group.ID] = cloneScimGroup(group)
	store.groupExt[extKey] = group.ID
	store.groupName[nameKey] = group.ID
	return nil
}

// Update 更新组并使用乐观锁。
func (r *MemoryScimGroupRepository) Update(_ context.Context, group *domain.ScimGroup, expectedVersion int64) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	existing, ok := store.groups[group.ID]
	if !ok {
		return apperr.NotFound("scim group not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("scim group version conflict")
	}
	// 唯一键变更：先释放旧键，再占用新键；占用失败时回滚旧键，避免索引与主记录不一致。
	oldExt := scimGroupKey(existing.Provider, existing.ExternalID)
	newExt := scimGroupKey(group.Provider, group.ExternalID)
	oldName := scimGroupKey(existing.Provider, existing.DisplayName)
	newName := scimGroupKey(group.Provider, group.DisplayName)
	if other, dup := store.groupExt[newExt]; dup && other != group.ID {
		return apperr.Conflict("scim group externalId already exists")
	}
	if other, dup := store.groupName[newName]; dup && other != group.ID {
		return apperr.Conflict("scim group displayName already exists")
	}
	delete(store.groupExt, oldExt)
	delete(store.groupName, oldName)
	store.groupExt[newExt] = group.ID
	store.groupName[newName] = group.ID
	store.groups[group.ID] = cloneScimGroup(group)
	return nil
}

// SoftDelete 软删组并使用乐观锁。
func (r *MemoryScimGroupRepository) SoftDelete(_ context.Context, group *domain.ScimGroup, expectedVersion int64) error {
	if group == nil {
		return apperr.BadRequest("scim group is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	existing, ok := store.groups[group.ID]
	if !ok {
		return apperr.NotFound("scim group not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("scim group version conflict")
	}
	store.groups[group.ID] = cloneScimGroup(group)
	return nil
}

// List 分页列出未删除组。
func (r *MemoryScimGroupRepository) List(_ context.Context, filter domain.ScimGroupFilter) ([]*domain.ScimGroup, int, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	matched := make([]*domain.ScimGroup, 0, 16)
	for _, group := range store.groups {
		if group.Provider != filter.Provider || group.IsDeleted() {
			continue
		}
		if filter.DisplayName != "" && group.DisplayName != filter.DisplayName {
			continue
		}
		if filter.ExternalID != "" && group.ExternalID != filter.ExternalID {
			continue
		}
		matched = append(matched, cloneScimGroup(group))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
	total := len(matched)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := total
	if filter.Limit > 0 && start+filter.Limit < end {
		end = start + filter.Limit
	}
	return matched[start:end], total, nil
}

// ListByAccount 返回账号所属的未删除组。
func (r *MemoryScimGroupRepository) ListByAccount(_ context.Context, accountID string) ([]*domain.ScimGroup, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]*domain.ScimGroup, 0, 4)
	for groupID := range store.memberOfID[strings.TrimSpace(accountID)] {
		group, ok := store.groups[groupID]
		if !ok || group.IsDeleted() {
			continue
		}
		out = append(out, cloneScimGroup(group))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListMembers 返回组成员，按账号 ID 升序。
func (r *MemoryScimGroupRepository) ListMembers(_ context.Context, groupID string) ([]*domain.ScimGroupMember, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	members := store.members[strings.TrimSpace(groupID)]
	out := make([]*domain.ScimGroupMember, 0, len(members))
	for accountID, createdAt := range members {
		out = append(out, &domain.ScimGroupMember{
			GroupID: strings.TrimSpace(groupID), AccountID: accountID, CreatedAt: createdAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, nil
}

// AddMembers 批量添加成员；已存在的成员幂等跳过。
func (r *MemoryScimGroupRepository) AddMembers(_ context.Context, members []*domain.ScimGroupMember) error {
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, member := range members {
		if member == nil {
			continue
		}
		if _, ok := store.groups[member.GroupID]; !ok {
			return apperr.NotFound("scim group not found")
		}
		if store.members[member.GroupID] == nil {
			store.members[member.GroupID] = make(map[string]time.Time, 4)
		}
		store.members[member.GroupID][member.AccountID] = member.CreatedAt
		if store.memberOfID[member.AccountID] == nil {
			store.memberOfID[member.AccountID] = make(map[string]struct{}, 2)
		}
		store.memberOfID[member.AccountID][member.GroupID] = struct{}{}
	}
	return nil
}

// RemoveMembers 批量移除成员；不存在的成员幂等跳过。
func (r *MemoryScimGroupRepository) RemoveMembers(_ context.Context, groupID string, accountIDs []string) error {
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, accountID := range accountIDs {
		delete(store.members[groupID], accountID)
		delete(store.memberOfID[accountID], groupID)
	}
	return nil
}

// RemoveAllMembers 清空组成员。
func (r *MemoryScimGroupRepository) RemoveAllMembers(_ context.Context, groupID string) error {
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	for accountID := range store.members[groupID] {
		delete(store.memberOfID[accountID], groupID)
	}
	delete(store.members, groupID)
	return nil
}

// CountMembers 返回组成员数量。
func (r *MemoryScimGroupRepository) CountMembers(_ context.Context, groupIDs []string) (map[string]int, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make(map[string]int, len(groupIDs))
	for _, groupID := range groupIDs {
		out[groupID] = len(store.members[groupID])
	}
	return out, nil
}

// scimGroupKey 组合 provider 与业务唯一键。
func scimGroupKey(provider, value string) string {
	return strings.TrimSpace(provider) + "\x00" + strings.TrimSpace(value)
}

// cloneScimToken 返回令牌副本，避免调用方修改仓储内部状态。
func cloneScimToken(token *domain.ScimToken) *domain.ScimToken {
	if token == nil {
		return nil
	}
	cloned := *token
	if token.ExpiresAt != nil {
		expiry := *token.ExpiresAt
		cloned.ExpiresAt = &expiry
	}
	if token.RevokedAt != nil {
		revoked := *token.RevokedAt
		cloned.RevokedAt = &revoked
	}
	return &cloned
}

// cloneScimGroup 返回组副本。
func cloneScimGroup(group *domain.ScimGroup) *domain.ScimGroup {
	if group == nil {
		return nil
	}
	cloned := *group
	if group.DeletedAt != nil {
		deleted := *group.DeletedAt
		cloned.DeletedAt = &deleted
	}
	return &cloned
}
