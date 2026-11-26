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

// memoryM2MStore 是 M2M 客户端与密钥共享的内存索引。
//
// 客户端与密钥分别由两个仓储类型暴露，但共用同一份存储：
// 轮换时「新增 secret + 旧 secret 转 retiring」必须在同一事务语义下完成，
// 内存模式用同一把锁保证一致性（与 SQL 模式的事务上下文等价）。
type memoryM2MStore struct {
	mu      sync.RWMutex
	clients map[string]*domain.M2MClient // 主键 id → 客户端
	byCID   map[string]string            // client_id → 主键 id
	secrets map[string]*domain.M2MSecret // 主键 id → 密钥
	byHash  map[string]string            // secret_hash → 主键 id
}

// MemoryM2MClientRepository 是内存版客户端仓储；只用于本地 / 单测。
type MemoryM2MClientRepository struct {
	store *memoryM2MStore
}

// MemoryM2MSecretRepository 是内存版密钥仓储；与客户端仓储共享同一存储。
type MemoryM2MSecretRepository struct {
	store *memoryM2MStore
}

// 编译期断言：分别满足各自的领域端口。
var (
	_ domain.M2MClientRepository = (*MemoryM2MClientRepository)(nil)
	_ domain.M2MSecretRepository = (*MemoryM2MSecretRepository)(nil)
)

// NewMemoryM2MRepositories 创建共享同一存储的客户端与密钥仓储。
func NewMemoryM2MRepositories() (*MemoryM2MClientRepository, *MemoryM2MSecretRepository) {
	store := &memoryM2MStore{
		clients: make(map[string]*domain.M2MClient),
		byCID:   make(map[string]string),
		secrets: make(map[string]*domain.M2MSecret),
		byHash:  make(map[string]string),
	}
	return &MemoryM2MClientRepository{store: store}, &MemoryM2MSecretRepository{store: store}
}

// FindByClientID 按对外标识加载客户端；软删客户端仍返回。
func (r *MemoryM2MClientRepository) FindByClientID(_ context.Context, clientID string) (*domain.M2MClient, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.byCID[strings.TrimSpace(clientID)]
	if !ok {
		return nil, apperr.NotFound("m2m client not found")
	}
	return cloneM2MClient(store.clients[id]), nil
}

// FindByID 按主键加载客户端。
func (r *MemoryM2MClientRepository) FindByID(_ context.Context, id string) (*domain.M2MClient, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	client, ok := store.clients[strings.TrimSpace(id)]
	if !ok {
		return nil, apperr.NotFound("m2m client not found")
	}
	return cloneM2MClient(client), nil
}

// Save 创建或更新客户端；expectedVersion=0 表示创建。
func (r *MemoryM2MClientRepository) Save(_ context.Context, client *domain.M2MClient, expectedVersion int64) error {
	if client == nil {
		return apperr.BadRequest("m2m client is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, exists := store.clients[client.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("m2m client already exists")
		}
		if _, dup := store.byCID[client.ClientID]; dup {
			return apperr.Conflict("m2m client_id already exists")
		}
		copied := cloneM2MClient(client)
		if copied.Version == 0 {
			copied.Version = 1
		}
		store.clients[copied.ID] = copied
		store.byCID[copied.ClientID] = copied.ID
		return nil
	}
	if !exists {
		return apperr.NotFound("m2m client not found")
	}
	if stored.Version != expectedVersion {
		return apperr.Conflict("m2m client version conflict")
	}
	if stored.ClientID != client.ClientID {
		return apperr.Conflict("m2m client_id is immutable")
	}
	copied := cloneM2MClient(client)
	copied.Version = expectedVersion + 1
	store.clients[copied.ID] = copied
	return nil
}

// List 分页列出客户端（含软删），按创建时间倒序。
func (r *MemoryM2MClientRepository) List(_ context.Context, filter domain.M2MClientListFilter) ([]*domain.M2MClient, int, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	matched := make([]*domain.M2MClient, 0, len(store.clients))
	for _, client := range store.clients {
		if filter.Status != "" && client.Status != filter.Status {
			continue
		}
		if filter.Channel != "" && !client.AllowsChannel(filter.Channel) {
			continue
		}
		matched = append(matched, cloneM2MClient(client))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
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

// ListByClient 返回客户端全部密钥，按创建时间倒序。
func (r *MemoryM2MSecretRepository) ListByClient(_ context.Context, clientID string) ([]*domain.M2MSecret, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	target := strings.TrimSpace(clientID)
	out := make([]*domain.M2MSecret, 0, domain.MaxM2MSecretsPerClient)
	for _, secret := range store.secrets {
		if secret.ClientID == target {
			out = append(out, cloneM2MSecret(secret))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// FindByHash 按摘要加载密钥。
func (r *MemoryM2MSecretRepository) FindByHash(_ context.Context, secretHash string) (*domain.M2MSecret, error) {
	store := r.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.byHash[strings.TrimSpace(secretHash)]
	if !ok {
		return nil, apperr.NotFound("m2m secret not found")
	}
	return cloneM2MSecret(store.secrets[id]), nil
}

// Save 创建密钥；摘要重复返回冲突。
func (r *MemoryM2MSecretRepository) Save(_ context.Context, secret *domain.M2MSecret) error {
	if secret == nil {
		return apperr.BadRequest("m2m secret is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.secrets[secret.ID]; exists {
		return apperr.Conflict("m2m secret already exists")
	}
	if _, dup := store.byHash[secret.SecretHash]; dup {
		return apperr.Conflict("m2m secret hash already exists")
	}
	copied := cloneM2MSecret(secret)
	store.secrets[copied.ID] = copied
	store.byHash[copied.SecretHash] = copied.ID
	return nil
}

// UpdateStatus 更新密钥状态与到期 / 吊销时间。
func (r *MemoryM2MSecretRepository) UpdateStatus(_ context.Context, secret *domain.M2MSecret) error {
	if secret == nil {
		return apperr.BadRequest("m2m secret is required")
	}
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.secrets[secret.ID]; !ok {
		return apperr.NotFound("m2m secret not found")
	}
	store.secrets[secret.ID] = cloneM2MSecret(secret)
	return nil
}

// RevokeAll 吊销客户端全部未吊销密钥。
func (r *MemoryM2MSecretRepository) RevokeAll(_ context.Context, clientID string, now time.Time) (int, error) {
	store := r.store
	store.mu.Lock()
	defer store.mu.Unlock()
	target := strings.TrimSpace(clientID)
	affected := 0
	for id, secret := range store.secrets {
		if secret.ClientID != target || secret.Status == domain.M2MSecretRevoked {
			continue
		}
		copied := cloneM2MSecret(secret)
		copied.Status = domain.M2MSecretRevoked
		revokedAt := now.UTC()
		copied.RevokedAt = &revokedAt
		store.secrets[id] = copied
		affected++
	}
	return affected, nil
}

// cloneM2MClient 深拷贝客户端；切片必须复制，避免调用方修改内部状态。
func cloneM2MClient(client *domain.M2MClient) *domain.M2MClient {
	if client == nil {
		return nil
	}
	copied := *client
	copied.AllowedChannels = append([]domain.Channel(nil), client.AllowedChannels...)
	copied.AllowedScopes = append([]string(nil), client.AllowedScopes...)
	if client.DeletedAt != nil {
		deletedAt := *client.DeletedAt
		copied.DeletedAt = &deletedAt
	}
	if client.RotationDueAt != nil {
		rotation := *client.RotationDueAt
		copied.RotationDueAt = &rotation
	}
	return &copied
}

// cloneM2MSecret 深拷贝密钥。
func cloneM2MSecret(secret *domain.M2MSecret) *domain.M2MSecret {
	if secret == nil {
		return nil
	}
	copied := *secret
	if secret.ExpiresAt != nil {
		expires := *secret.ExpiresAt
		copied.ExpiresAt = &expires
	}
	if secret.RevokedAt != nil {
		revoked := *secret.RevokedAt
		copied.RevokedAt = &revoked
	}
	return &copied
}
