package persistence

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mfaSelectColumns 是 MFA 绑定表的显式列清单。
const mfaSelectColumns = `account_id, secret_ciphertext, confirmed_at, recovery_code_hashes, created_at, updated_at`

// SQLMFAEnrollmentRepository 是 MySQL 8.0 的 TOTP 绑定仓储实现，主键为账号 ID。
type SQLMFAEnrollmentRepository struct {
	sql *sql.DB
}

// 编译期断言。
var _ domain.MFAEnrollmentRepository = (*SQLMFAEnrollmentRepository)(nil)

// NewSQLMFAEnrollmentRepository 基于已打开的 *sql.DB 创建 MFA 绑定仓储。
func NewSQLMFAEnrollmentRepository(sqlDB *sql.DB) *SQLMFAEnrollmentRepository {
	return &SQLMFAEnrollmentRepository{sql: sqlDB}
}

// FindByAccount 返回账号绑定。
func (r *SQLMFAEnrollmentRepository) FindByAccount(ctx context.Context, accountID string) (*domain.MFAEnrollment, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	row := r.sql.QueryRowContext(ctx,
		`SELECT `+mfaSelectColumns+` FROM identity_mfa WHERE account_id = ?`, id)
	enrollment, err := scanMFAEnrollment(row)
	if err != nil {
		return nil, resolveNotFound(err, "mfa enrollment not found")
	}
	return enrollment, nil
}

// Save 创建或覆盖账号绑定。
func (r *SQLMFAEnrollmentRepository) Save(ctx context.Context, enrollment *domain.MFAEnrollment) error {
	if enrollment == nil {
		return apperr.BadRequest("mfa enrollment is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity mfa repository is not configured")
	}
	accountID, err := parseID(enrollment.AccountID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(enrollment.RecoveryCodeHashes)
	if err != nil {
		return apperr.Internal("encode recovery code hashes failed", err)
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO identity_mfa
		(account_id, secret_ciphertext, confirmed_at, recovery_code_hashes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		AS new ON DUPLICATE KEY UPDATE
			secret_ciphertext = new.secret_ciphertext,
			confirmed_at = new.confirmed_at,
			recovery_code_hashes = new.recovery_code_hashes,
			updated_at = new.updated_at`,
		accountID, enrollment.SecretCiphertext, nullTime(enrollment.ConfirmedAt),
		payload, enrollment.CreatedAt.UTC(), enrollment.UpdatedAt.UTC())
	return err
}

// Delete 删除绑定；不存在也返回成功。
func (r *SQLMFAEnrollmentRepository) Delete(ctx context.Context, accountID string) error {
	id, err := parseID(accountID)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `DELETE FROM identity_mfa WHERE account_id = ?`, id)
	return err
}

// scanMFAEnrollment 扫描一行绑定；恢复码 JSON 必须可解析。
func scanMFAEnrollment(row scanRow) (*domain.MFAEnrollment, error) {
	var (
		enrollment  domain.MFAEnrollment
		confirmedAt sql.NullTime
		hashes      []byte
	)
	err := row.Scan(
		&enrollment.AccountID, &enrollment.SecretCiphertext, &confirmedAt,
		&hashes, &enrollment.CreatedAt, &enrollment.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(hashes, &enrollment.RecoveryCodeHashes); err != nil {
		return nil, apperr.Internal("decode recovery code hashes failed", err)
	}
	if enrollment.RecoveryCodeHashes == nil {
		enrollment.RecoveryCodeHashes = []string{}
	}
	enrollment.ConfirmedAt = nullableTime(confirmedAt)
	enrollment.CreatedAt = enrollment.CreatedAt.UTC()
	enrollment.UpdatedAt = enrollment.UpdatedAt.UTC()
	return &enrollment, nil
}
