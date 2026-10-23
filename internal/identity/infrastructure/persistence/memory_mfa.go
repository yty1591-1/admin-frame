package persistence

import (
	"context"
	"sync"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryMFAEnrollmentRepository 是基于内存 map 的 TOTP 绑定仓储，主键为账号 ID。
type MemoryMFAEnrollmentRepository struct {
	mu          sync.RWMutex
	enrollments map[string]*domain.MFAEnrollment
}

// NewMemoryMFAEnrollmentRepository 创建空的 MFA 绑定仓储。
func NewMemoryMFAEnrollmentRepository() *MemoryMFAEnrollmentRepository {
	return &MemoryMFAEnrollmentRepository{enrollments: make(map[string]*domain.MFAEnrollment)}
}

// FindByAccount 返回账号绑定；不存在返回 NotFound。
func (r *MemoryMFAEnrollmentRepository) FindByAccount(_ context.Context, accountID string) (*domain.MFAEnrollment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	enrollment, ok := r.enrollments[accountID]
	if !ok {
		return nil, apperr.NotFound("mfa enrollment not found")
	}
	return cloneMFAEnrollment(enrollment), nil
}

// Save 创建或覆盖账号绑定。
func (r *MemoryMFAEnrollmentRepository) Save(_ context.Context, enrollment *domain.MFAEnrollment) error {
	if enrollment == nil {
		return apperr.BadRequest("mfa enrollment is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enrollments[enrollment.AccountID] = cloneMFAEnrollment(enrollment)
	return nil
}

// Delete 删除绑定；不存在也返回成功。
func (r *MemoryMFAEnrollmentRepository) Delete(_ context.Context, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.enrollments, accountID)
	return nil
}

// cloneMFAEnrollment 深拷贝密钥密文、恢复码与确认时间。
func cloneMFAEnrollment(enrollment *domain.MFAEnrollment) *domain.MFAEnrollment {
	if enrollment == nil {
		return nil
	}
	cp := *enrollment
	cp.SecretCiphertext = append([]byte(nil), enrollment.SecretCiphertext...)
	cp.RecoveryCodeHashes = append([]string(nil), enrollment.RecoveryCodeHashes...)
	if cp.RecoveryCodeHashes == nil {
		cp.RecoveryCodeHashes = []string{}
	}
	cp.ConfirmedAt = cloneTime(enrollment.ConfirmedAt)
	return &cp
}
