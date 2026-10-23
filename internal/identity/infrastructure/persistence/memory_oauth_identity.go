package persistence

import (
	"context"
	"sync"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// oauthIdentityKey 是 (provider, provider_subject) 唯一索引的内存表示。
type oauthIdentityKey struct {
	provider string
	subject  string
}

// MemoryOAuthIdentityRepository 是基于内存 map 的外部身份映射仓储。
type MemoryOAuthIdentityRepository struct {
	mu        sync.RWMutex
	byID      map[string]*domain.OAuthIdentity
	bySubject map[oauthIdentityKey]string
}

// NewMemoryOAuthIdentityRepository 创建空的映射仓储。
func NewMemoryOAuthIdentityRepository() *MemoryOAuthIdentityRepository {
	return &MemoryOAuthIdentityRepository{
		byID:      make(map[string]*domain.OAuthIdentity),
		bySubject: make(map[oauthIdentityKey]string),
	}
}

// FindByProviderSubject 按提供方 + 主体标识加载映射；不存在返回 NotFound。
func (r *MemoryOAuthIdentityRepository) FindByProviderSubject(_ context.Context, provider, subject string) (*domain.OAuthIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.bySubject[oauthIdentityKey{provider: provider, subject: subject}]
	if !ok {
		return nil, apperr.NotFound("oauth identity not found")
	}
	cp := *r.byID[id]
	return &cp, nil
}

// Save 创建映射；主键或唯一键冲突返回 Conflict。
func (r *MemoryOAuthIdentityRepository) Save(_ context.Context, identity *domain.OAuthIdentity) error {
	if identity == nil {
		return apperr.BadRequest("oauth identity is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[identity.ID]; exists {
		return apperr.Conflict("oauth identity id already exists")
	}
	key := oauthIdentityKey{provider: identity.Provider, subject: identity.ProviderSubject}
	if _, dup := r.bySubject[key]; dup {
		return apperr.Conflict("oauth identity already exists for this provider subject")
	}
	cp := *identity
	r.byID[identity.ID] = &cp
	r.bySubject[key] = identity.ID
	return nil
}
