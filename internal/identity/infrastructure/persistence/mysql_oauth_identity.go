package persistence

import (
	"context"
	"database/sql"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// oauthIdentitySelectColumns 是外部身份映射表的显式列清单。
//
// S5 起不再存在明文 provider_subject 列；主体由密文解密回填。
const oauthIdentitySelectColumns = `id, account_id, provider, provider_subject_index, provider_subject_ciphertext, created_at, updated_at`

// OAuthIdentityRepositoryOption 配置 SQL 外部身份仓储的可选能力。
type OAuthIdentityRepositoryOption func(*SQLOAuthIdentityRepository)

// WithOAuthSubjectProtector 注入外部身份主体保护器（S5 起为必需能力）。
//
// 未注入时读写路径失败关闭，避免退回明文存储。
func WithOAuthSubjectProtector(protector SubjectProtector) OAuthIdentityRepositoryOption {
	return func(r *SQLOAuthIdentityRepository) {
		if protector != nil {
			r.protector = protector
		}
	}
}

// SQLOAuthIdentityRepository 是 MySQL 8.0 的外部身份映射仓储实现。
type SQLOAuthIdentityRepository struct {
	sql       *sql.DB
	protector SubjectProtector
}

// 编译期断言。
var _ domain.OAuthIdentityRepository = (*SQLOAuthIdentityRepository)(nil)

// NewSQLOAuthIdentityRepository 基于已打开的 *sql.DB 创建映射仓储。
func NewSQLOAuthIdentityRepository(sqlDB *sql.DB, opts ...OAuthIdentityRepositoryOption) *SQLOAuthIdentityRepository {
	repository := &SQLOAuthIdentityRepository{sql: sqlDB}
	for _, opt := range opts {
		if opt != nil {
			opt(repository)
		}
	}
	return repository
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLOAuthIdentityRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByProviderSubject 按提供方 + 确定性主体索引加载映射。
//
// S5：只按 provider_subject_index 查找，不再回退明文；未配置保护器时失败关闭。
func (r *SQLOAuthIdentityRepository) FindByProviderSubject(ctx context.Context, provider, subject string) (*domain.OAuthIdentity, error) {
	if r.protector == nil {
		return nil, apperr.Unavailable("oauth subject protector is not configured")
	}
	index := r.protector.ProviderIndex(provider, subject)
	if index == "" {
		return nil, apperr.Internal("oauth provider subject index is empty", nil)
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+oauthIdentitySelectColumns+` FROM identity_oauth_identity
		 WHERE provider = ? AND provider_subject_index = ?`,
		provider, index)
	identity, err := scanOAuthIdentity(row)
	if err != nil {
		return nil, resolveNotFound(err, "oauth identity not found")
	}
	if err := r.decodeProviderSubject(identity); err != nil {
		return nil, err
	}
	return identity, nil
}

// Save 创建映射；唯一键冲突返回 Conflict。
//
// S5：只写 provider_subject_index 与 provider_subject_ciphertext。
func (r *SQLOAuthIdentityRepository) Save(ctx context.Context, identity *domain.OAuthIdentity) error {
	if identity == nil {
		return apperr.BadRequest("oauth identity is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity oauth repository is not configured")
	}
	index, ciphertext, err := r.protectProviderSubject(identity)
	if err != nil {
		return err
	}
	id, err := parseID(identity.ID)
	if err != nil {
		return err
	}
	accountID, err := parseID(identity.AccountID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_oauth_identity
		(id, account_id, provider, provider_subject_index, provider_subject_ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, accountID, identity.Provider, index, ciphertext,
		identity.CreatedAt.UTC(), identity.UpdatedAt.UTC())
	return conflictOnDuplicate(err, "oauth identity already exists for this provider subject")
}

// ListByAccount 返回账号已绑定的外部身份，按 provider 升序（BIZ-02）。
func (r *SQLOAuthIdentityRepository) ListByAccount(ctx context.Context, accountID string) ([]*domain.OAuthIdentity, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+oauthIdentitySelectColumns+` FROM identity_oauth_identity
		 WHERE account_id = ? ORDER BY provider, id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.OAuthIdentity, 0, 4)
	for rows.Next() {
		identity, scanErr := scanOAuthIdentity(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if decodeErr := r.decodeProviderSubject(identity); decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete 解绑账号的指定 provider；不存在返回 NotFound（BIZ-02）。
func (r *SQLOAuthIdentityRepository) Delete(ctx context.Context, accountID, provider string) error {
	id, err := parseID(accountID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`DELETE FROM identity_oauth_identity WHERE account_id = ? AND provider = ?`, id, provider)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.NotFound("oauth identity not found")
	}
	return nil
}

// protectProviderSubject 计算外部身份主体索引与密文；未配置保护器时失败关闭。
func (r *SQLOAuthIdentityRepository) protectProviderSubject(identity *domain.OAuthIdentity) (providerIndex string, providerCiphertext []byte, err error) {
	if r.protector == nil {
		return "", nil, apperr.Unavailable("oauth subject protector is not configured")
	}
	index := r.protector.ProviderIndex(identity.Provider, identity.ProviderSubject)
	ciphertext, err := r.protector.EncryptProviderSubject(identity.ID, identity.ProviderSubject)
	if err != nil {
		return "", nil, err
	}
	return index, ciphertext, nil
}

// decodeProviderSubject 用密文回填明文主体；S5 后密文必须存在。
func (r *SQLOAuthIdentityRepository) decodeProviderSubject(identity *domain.OAuthIdentity) error {
	if r.protector == nil {
		return apperr.Unavailable("oauth subject protector is not configured")
	}
	if len(identity.ProviderSubjectCiphertext) == 0 {
		return apperr.Internal("oauth provider subject ciphertext is missing", nil)
	}
	plaintext, err := r.protector.DecryptProviderSubject(identity.ID, identity.ProviderSubjectCiphertext)
	if err != nil {
		return err
	}
	identity.ProviderSubject = plaintext
	return nil
}

// scanOAuthIdentity 扫描一行映射。
func scanOAuthIdentity(row scanRow) (*domain.OAuthIdentity, error) {
	var (
		identity      domain.OAuthIdentity
		subjectIndex  sql.NullString
		subjectCipher []byte
	)
	err := row.Scan(
		&identity.ID, &identity.AccountID, &identity.Provider,
		&subjectIndex, &subjectCipher, &identity.CreatedAt, &identity.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	identity.ProviderSubjectIndex = subjectIndex.String
	identity.ProviderSubjectCiphertext = subjectCipher
	identity.CreatedAt = identity.CreatedAt.UTC()
	identity.UpdatedAt = identity.UpdatedAt.UTC()
	return &identity, nil
}
