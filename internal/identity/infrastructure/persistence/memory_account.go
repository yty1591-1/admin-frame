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

// accountKey 是 (channel, subject) 唯一索引的内存表示。
type accountKey struct {
	channel domain.Channel
	subject string
}

// MemoryAccountRepository 是基于内存 map 的账号仓储。
//
// 并发安全：内部使用 RWMutex；所有返回值均为深拷贝。
// channel / subject / merchant_id 创建后不可变，Save 会拒绝变更，模拟唯一索引约束。
type MemoryAccountRepository struct {
	mu        sync.RWMutex
	accounts  map[string]*domain.Account
	bySubject map[accountKey]string
}

// NewMemoryAccountRepository 创建空的账号仓储。
func NewMemoryAccountRepository() *MemoryAccountRepository {
	return &MemoryAccountRepository{
		accounts:  make(map[string]*domain.Account),
		bySubject: make(map[accountKey]string),
	}
}

// FindByID 按主键加载账号；不存在返回 NotFound。
func (r *MemoryAccountRepository) FindByID(_ context.Context, id string) (*domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	account, ok := r.accounts[id]
	if !ok {
		return nil, apperr.NotFound("account not found")
	}
	return cloneAccount(account), nil
}

// FindBySubject 按端别 + 规范化主体加载账号；软删账号仍可查到，用于阻止 subject 复用。
func (r *MemoryAccountRepository) FindBySubject(_ context.Context, channel domain.Channel, subject string) (*domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.bySubject[accountKey{channel: channel, subject: domain.NormalizeSubject(subject)}]
	if !ok {
		return nil, apperr.NotFound("account not found")
	}
	return cloneAccount(r.accounts[id]), nil
}

// Save 创建或更新账号；expectedVersion 为 0 表示创建。
func (r *MemoryAccountRepository) Save(_ context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.accounts[account.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("account id already exists")
		}
		key := accountKey{channel: account.Channel, subject: account.Subject}
		if _, dup := r.bySubject[key]; dup {
			return apperr.Conflict("account subject already exists in this channel")
		}
		r.accounts[account.ID] = cloneAccount(account)
		r.bySubject[key] = account.ID
		return nil
	}
	if !exists {
		return apperr.NotFound("account not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("account version conflict")
	}
	if existing.Channel != account.Channel || existing.Subject != account.Subject || existing.MerchantID != account.MerchantID {
		return apperr.BadRequest("account channel, subject and merchant are immutable")
	}
	r.accounts[account.ID] = cloneAccount(account)
	return nil
}

// List 按端别 / 状态 / 关键词分页列出账号（含软删），按创建时间倒序。
func (r *MemoryAccountRepository) List(_ context.Context, filter domain.AccountListFilter) ([]*domain.Account, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keyword := domain.NormalizeSubject(filter.Keyword)
	matched := make([]*domain.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if filter.Channel != "" && account.Channel != filter.Channel {
			continue
		}
		if filter.Status != "" && account.Status != filter.Status {
			continue
		}
		if keyword != "" && !strings.Contains(account.Subject, keyword) {
			continue
		}
		matched = append(matched, cloneAccount(account))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	offset := filter.Offset
	if offset > total {
		offset = total
	}
	end := offset + filter.Limit
	if filter.Limit <= 0 || end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

// RebindSubject 更新登录主体；channel / merchant_id 仍不可变。
func (r *MemoryAccountRepository) RebindSubject(_ context.Context, account *domain.Account, expectedVersion int64) error {
	if account == nil {
		return apperr.BadRequest("account is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.accounts[account.ID]
	if !exists {
		return apperr.NotFound("account not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("account version conflict")
	}
	if existing.Channel != account.Channel || existing.MerchantID != account.MerchantID {
		return apperr.BadRequest("account channel and merchant are immutable")
	}
	newKey := accountKey{channel: account.Channel, subject: account.Subject}
	if id, dup := r.bySubject[newKey]; dup && id != account.ID {
		return apperr.Conflict("account subject already exists in this channel")
	}
	delete(r.bySubject, accountKey{channel: existing.Channel, subject: existing.Subject})
	r.bySubject[newKey] = account.ID
	r.accounts[account.ID] = cloneAccount(account)
	return nil
}

// cloneAccount 返回账号深拷贝，隔离可空时间字段。
func cloneAccount(account *domain.Account) *domain.Account {
	if account == nil {
		return nil
	}
	cp := *account
	cp.PasswordUpdatedAt = cloneTime(account.PasswordUpdatedAt)
	cp.LockedUntil = cloneTime(account.LockedUntil)
	cp.DeletedAt = cloneTime(account.DeletedAt)
	return &cp
}

// cloneTime 复制可空时间，避免调用方与仓储共享同一指针。
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cp := value.UTC()
	return &cp
}
