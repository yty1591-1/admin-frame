package persistence

import (
	"context"
	"sort"
	"sync"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// credentialKey 是 (account_id, credential_type) 唯一索引的内存表示。
type credentialKey struct {
	accountID string
	typ       domain.CredentialType
}

// MemoryCredentialRepository 是基于内存 map 的凭据仓储。
type MemoryCredentialRepository struct {
	mu          sync.RWMutex
	credentials map[credentialKey]*domain.Credential
}

// NewMemoryCredentialRepository 创建空的凭据仓储。
func NewMemoryCredentialRepository() *MemoryCredentialRepository {
	return &MemoryCredentialRepository{credentials: make(map[credentialKey]*domain.Credential)}
}

// FindByAccount 返回账号指定类型的凭据；不存在返回 NotFound。
func (r *MemoryCredentialRepository) FindByAccount(_ context.Context, accountID string, typ domain.CredentialType) (*domain.Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	credential, ok := r.credentials[credentialKey{accountID: accountID, typ: typ}]
	if !ok {
		return nil, apperr.NotFound("credential not found")
	}
	return cloneCredential(credential), nil
}

// ListByAccount 返回账号全部凭据，按类型排序。
func (r *MemoryCredentialRepository) ListByAccount(_ context.Context, accountID string) ([]*domain.Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.Credential, 0, 2)
	for key, credential := range r.credentials {
		if key.accountID != accountID {
			continue
		}
		out = append(out, cloneCredential(credential))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, nil
}

// Save 创建或替换凭据：同账号同类型只保留一条，替换时保留原主键与创建时间。
func (r *MemoryCredentialRepository) Save(_ context.Context, credential *domain.Credential) error {
	if credential == nil {
		return apperr.BadRequest("credential is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := credentialKey{accountID: credential.AccountID, typ: credential.Type}
	cp := *credential
	cp.PasswordHistory = append([]domain.PasswordHistoryEntry(nil), credential.PasswordHistory...)
	if existing, ok := r.credentials[key]; ok {
		cp.ID = existing.ID
		cp.CreatedAt = existing.CreatedAt
	} else if existingByID := r.findByIDLocked(credential.ID); existingByID != nil {
		return apperr.Conflict("credential id already exists")
	}
	r.credentials[key] = &cp
	return nil
}

// Delete 删除凭据；不存在也返回成功。
func (r *MemoryCredentialRepository) Delete(_ context.Context, accountID string, typ domain.CredentialType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.credentials, credentialKey{accountID: accountID, typ: typ})
	return nil
}

// findByIDLocked 检查主键是否已被其他凭据占用；调用方必须持有写锁。
func (r *MemoryCredentialRepository) findByIDLocked(id string) *domain.Credential {
	for _, credential := range r.credentials {
		if credential.ID == id {
			return credential
		}
	}
	return nil
}

// cloneCredential 深拷贝可变的密码历史切片，避免调用方追加修改仓储内部状态。
func cloneCredential(credential *domain.Credential) *domain.Credential {
	cp := *credential
	cp.PasswordHistory = append([]domain.PasswordHistoryEntry(nil), credential.PasswordHistory...)
	return &cp
}
