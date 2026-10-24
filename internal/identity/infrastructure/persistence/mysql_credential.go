package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// credentialSelectColumns 是凭据表的显式列清单。
const credentialSelectColumns = `id, account_id, credential_type, credential_hash, algorithm, created_at, updated_at` //nolint:gosec // G101: SQL 列名，不是凭证

// SQLCredentialRepository 是 MySQL 8.0 的凭据仓储实现。
//
// 唯一键为 (account_id, credential_type)：Save 先按业务键定位，
// 存在则更新原行（保留主键与创建时间），不存在才插入，避免唯一键冲突误改其他账号。
type SQLCredentialRepository struct {
	sql *sql.DB
}

// 编译期断言。
var _ domain.CredentialRepository = (*SQLCredentialRepository)(nil)

// NewSQLCredentialRepository 基于已打开的 *sql.DB 创建凭据仓储。
func NewSQLCredentialRepository(sqlDB *sql.DB) *SQLCredentialRepository {
	return &SQLCredentialRepository{sql: sqlDB}
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLCredentialRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByAccount 返回账号指定类型的凭据。
func (r *SQLCredentialRepository) FindByAccount(ctx context.Context, accountID string, typ domain.CredentialType) (*domain.Credential, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+credentialSelectColumns+` FROM identity_credential WHERE account_id = ? AND credential_type = ?`,
		id, string(typ))
	credential, err := scanCredential(row)
	if err != nil {
		return nil, resolveNotFound(err, "credential not found")
	}
	return credential, nil
}

// ListByAccount 返回账号全部凭据，按类型排序。
func (r *SQLCredentialRepository) ListByAccount(ctx context.Context, accountID string) ([]*domain.Credential, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+credentialSelectColumns+` FROM identity_credential WHERE account_id = ? ORDER BY credential_type`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Credential, 0, 2)
	for rows.Next() {
		credential, scanErr := scanCredential(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Save 创建或替换凭据。
func (r *SQLCredentialRepository) Save(ctx context.Context, credential *domain.Credential) error {
	if credential == nil {
		return apperr.BadRequest("credential is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity credential repository is not configured")
	}
	accountID, err := parseID(credential.AccountID)
	if err != nil {
		return err
	}
	var existingID int64
	err = r.exec(ctx).QueryRowContext(ctx,
		`SELECT id FROM identity_credential WHERE account_id = ? AND credential_type = ?`,
		accountID, string(credential.Type)).Scan(&existingID)
	switch {
	case err == nil:
		_, err = r.exec(ctx).ExecContext(ctx,
			`UPDATE identity_credential SET credential_hash = ?, algorithm = ?, updated_at = ? WHERE id = ?`,
			credential.Hash, credential.Algorithm, credential.UpdatedAt.UTC(), existingID)
		return err
	case errors.Is(err, sql.ErrNoRows):
		newID, parseErr := parseID(credential.ID)
		if parseErr != nil {
			return parseErr
		}
		_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_credential
			(id, account_id, credential_type, credential_hash, algorithm, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newID, accountID, string(credential.Type), credential.Hash, credential.Algorithm,
			credential.CreatedAt.UTC(), credential.UpdatedAt.UTC())
		return conflictOnDuplicate(err, "credential already exists")
	default:
		return err
	}
}

// Delete 删除凭据；不存在也返回成功。
func (r *SQLCredentialRepository) Delete(ctx context.Context, accountID string, typ domain.CredentialType) error {
	id, err := parseID(accountID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx,
		`DELETE FROM identity_credential WHERE account_id = ? AND credential_type = ?`, id, string(typ))
	return err
}

// scanCredential 扫描一行凭据。
func scanCredential(row scanRow) (*domain.Credential, error) {
	var credential domain.Credential
	err := row.Scan(
		&credential.ID, &credential.AccountID, &credential.Type,
		&credential.Hash, &credential.Algorithm, &credential.CreatedAt, &credential.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	credential.CreatedAt = credential.CreatedAt.UTC()
	credential.UpdatedAt = credential.UpdatedAt.UTC()
	return &credential, nil
}
