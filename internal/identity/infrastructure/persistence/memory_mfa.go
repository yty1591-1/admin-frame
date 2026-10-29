package persistence

import (
	"context"
	"sync"
	"time"

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

// Save 创建或使用乐观锁更新账号绑定；版本冲突返回 40900。
func (r *MemoryMFAEnrollmentRepository) Save(
	_ context.Context,
	enrollment *domain.MFAEnrollment,
	expectedVersion int64,
) error {
	if enrollment == nil {
		return apperr.BadRequest("mfa enrollment is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.enrollments[enrollment.AccountID]
	switch {
	case expectedVersion == 0:
		if ok {
			return apperr.Conflict("mfa enrollment already exists")
		}
	case !ok || existing.Version != expectedVersion:
		return apperr.Conflict("mfa enrollment version conflict")
	}
	r.enrollments[enrollment.AccountID] = cloneMFAEnrollment(enrollment)
	return nil
}

// ConsumeRecoveryCode 原子消费一条恢复码；重复消费返回 false。
func (r *MemoryMFAEnrollmentRepository) ConsumeRecoveryCode(
	_ context.Context,
	accountID, codeHash string,
	now time.Time,
) (remaining int, consumed bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.enrollments[accountID]
	if !ok {
		return 0, false, apperr.NotFound("mfa enrollment not found")
	}
	clone := cloneMFAEnrollment(existing)
	if !clone.UseRecoveryCode(codeHash, now) {
		return len(clone.RecoveryCodeHashes), false, nil
	}
	r.enrollments[accountID] = clone
	return len(clone.RecoveryCodeHashes), true, nil
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
