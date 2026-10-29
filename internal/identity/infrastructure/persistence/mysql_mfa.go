package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// mfaSelectColumns 是 MFA 绑定表的显式列清单。
const mfaSelectColumns = `account_id, secret_ciphertext, confirmed_at, last_used_step, version, recovery_code_hashes, created_at, updated_at`

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

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLMFAEnrollmentRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByAccount 返回账号绑定。
func (r *SQLMFAEnrollmentRepository) FindByAccount(ctx context.Context, accountID string) (*domain.MFAEnrollment, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+mfaSelectColumns+` FROM identity_mfa WHERE account_id = ?`, id)
	enrollment, err := scanMFAEnrollment(row)
	if err != nil {
		return nil, resolveNotFound(err, "mfa enrollment not found")
	}
	return enrollment, nil
}

// Save 创建或使用乐观锁更新账号绑定。
//
// expectedVersion=0 仅创建（已存在返回 40900）；>0 必须匹配当前版本，避免并发 Confirm / 解绑被覆盖。
func (r *SQLMFAEnrollmentRepository) Save(
	ctx context.Context,
	enrollment *domain.MFAEnrollment,
	expectedVersion int64,
) error {
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
	if expectedVersion == 0 {
		_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_mfa
			(account_id, secret_ciphertext, confirmed_at, last_used_step, version, recovery_code_hashes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, enrollment.SecretCiphertext, nullTime(enrollment.ConfirmedAt),
			enrollment.LastUsedStep, enrollment.Version, payload,
			enrollment.CreatedAt.UTC(), enrollment.UpdatedAt.UTC())
		return conflictOnDuplicate(err, "mfa enrollment already exists")
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_mfa
		SET secret_ciphertext = ?, confirmed_at = ?, last_used_step = ?, version = ?, recovery_code_hashes = ?, updated_at = ?
		WHERE account_id = ? AND version = ?`,
		enrollment.SecretCiphertext, nullTime(enrollment.ConfirmedAt), enrollment.LastUsedStep,
		enrollment.Version, payload, enrollment.UpdatedAt.UTC(), accountID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return apperr.Internal("check mfa update result failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("mfa enrollment version conflict")
	}
	return nil
}

// ConsumeRecoveryCode 使用 JSON_REMOVE 原子删除恢复码，避免读写回写导致复活。
func (r *SQLMFAEnrollmentRepository) ConsumeRecoveryCode(
	ctx context.Context,
	accountID, codeHash string,
	now time.Time,
) (int, bool, error) {
	id, err := parseID(accountID)
	if err != nil {
		return 0, false, err
	}
	if r.sql == nil {
		return 0, false, apperr.Unavailable("identity mfa repository is not configured")
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_mfa
		SET recovery_code_hashes = JSON_REMOVE(recovery_code_hashes,
				JSON_UNQUOTE(JSON_SEARCH(recovery_code_hashes, 'one', ?))),
			version = version + 1,
			updated_at = ?
		WHERE account_id = ?
		  AND JSON_SEARCH(recovery_code_hashes, 'one', ?) IS NOT NULL`,
		codeHash, now.UTC(), id, codeHash)
	if err != nil {
		return 0, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, apperr.Internal("check recovery code result failed", err)
	}
	remaining, countErr := r.recoveryCodeCount(ctx, id)
	if countErr != nil {
		return 0, false, countErr
	}
	return remaining, affected > 0, nil
}

// recoveryCodeCount 返回绑定剩余恢复码数量。
func (r *SQLMFAEnrollmentRepository) recoveryCodeCount(ctx context.Context, accountID int64) (int, error) {
	var remaining sql.NullInt64
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT JSON_LENGTH(recovery_code_hashes) FROM identity_mfa WHERE account_id = ?`, accountID)
	if err := row.Scan(&remaining); err != nil {
		return 0, resolveNotFound(err, "mfa enrollment not found")
	}
	if !remaining.Valid {
		return 0, nil
	}
	return int(remaining.Int64), nil
}

// Delete 删除绑定；不存在也返回成功。
func (r *SQLMFAEnrollmentRepository) Delete(ctx context.Context, accountID string) error {
	id, err := parseID(accountID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `DELETE FROM identity_mfa WHERE account_id = ?`, id)
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
		&enrollment.LastUsedStep, &enrollment.Version,
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
	if enrollment.Version == 0 {
		enrollment.Version = 1
	}
	enrollment.ConfirmedAt = nullableTime(confirmedAt)
	enrollment.CreatedAt = enrollment.CreatedAt.UTC()
	enrollment.UpdatedAt = enrollment.UpdatedAt.UTC()
	return &enrollment, nil
}
