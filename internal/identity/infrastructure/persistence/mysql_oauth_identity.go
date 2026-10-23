package persistence

import (
	"context"
	"database/sql"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// oauthIdentitySelectColumns 是外部身份映射表的显式列清单。
const oauthIdentitySelectColumns = `id, account_id, provider, provider_subject, created_at, updated_at`

// SQLOAuthIdentityRepository 是 MySQL 8.0 的外部身份映射仓储实现。
type SQLOAuthIdentityRepository struct {
	sql *sql.DB
}

// 编译期断言。
var _ domain.OAuthIdentityRepository = (*SQLOAuthIdentityRepository)(nil)

// NewSQLOAuthIdentityRepository 基于已打开的 *sql.DB 创建映射仓储。
func NewSQLOAuthIdentityRepository(sqlDB *sql.DB) *SQLOAuthIdentityRepository {
	return &SQLOAuthIdentityRepository{sql: sqlDB}
}

// FindByProviderSubject 按提供方 + 主体标识加载映射。
func (r *SQLOAuthIdentityRepository) FindByProviderSubject(ctx context.Context, provider, subject string) (*domain.OAuthIdentity, error) {
	row := r.sql.QueryRowContext(ctx,
		`SELECT `+oauthIdentitySelectColumns+` FROM identity_oauth_identity WHERE provider = ? AND provider_subject = ?`,
		provider, subject)
	identity, err := scanOAuthIdentity(row)
	if err != nil {
		return nil, resolveNotFound(err, "oauth identity not found")
	}
	return identity, nil
}

// Save 创建映射；唯一键冲突返回 Conflict。
func (r *SQLOAuthIdentityRepository) Save(ctx context.Context, identity *domain.OAuthIdentity) error {
	if identity == nil {
		return apperr.BadRequest("oauth identity is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity oauth repository is not configured")
	}
	id, err := parseID(identity.ID)
	if err != nil {
		return err
	}
	accountID, err := parseID(identity.AccountID)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO identity_oauth_identity
		(id, account_id, provider, provider_subject, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, accountID, identity.Provider, identity.ProviderSubject,
		identity.CreatedAt.UTC(), identity.UpdatedAt.UTC())
	return conflictOnDuplicate(err, "oauth identity already exists for this provider subject")
}

// scanOAuthIdentity 扫描一行映射。
func scanOAuthIdentity(row scanRow) (*domain.OAuthIdentity, error) {
	var identity domain.OAuthIdentity
	err := row.Scan(
		&identity.ID, &identity.AccountID, &identity.Provider,
		&identity.ProviderSubject, &identity.CreatedAt, &identity.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	identity.CreatedAt = identity.CreatedAt.UTC()
	identity.UpdatedAt = identity.UpdatedAt.UTC()
	return &identity, nil
}
