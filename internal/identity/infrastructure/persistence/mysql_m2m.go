package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// m2mClientSelectColumns 是客户端表的显式列清单（禁止 SELECT *，DATA-04）。
const m2mClientSelectColumns = `id, client_id, name, status, allowed_channels, allowed_scopes,
	token_version, owner, created_by, rotation_due_at, created_at, updated_at, deleted_at, version`

// m2mSecretSelectColumns 是密钥表的显式列清单。
const m2mSecretSelectColumns = `id, client_id, secret_hash, fingerprint, status, created_at, expires_at, revoked_at` //nolint:gosec // G101 误报：SQL 列清单，非凭据。

// SQLM2MClientRepository 是 MySQL 8.0 的 M2M 客户端仓储。
type SQLM2MClientRepository struct {
	sql *sql.DB
}

// SQLM2MSecretRepository 是 MySQL 8.0 的 M2M 密钥仓储。
//
// 与客户端仓储共享同一 *sql.DB 与事务上下文：轮换时「新增 secret + 旧 secret
// 转 retiring」由 app 层 UoW 保证原子性（§21.2.4）。
type SQLM2MSecretRepository struct {
	sql *sql.DB
}

// 编译期断言。
var (
	_ domain.M2MClientRepository = (*SQLM2MClientRepository)(nil)
	_ domain.M2MSecretRepository = (*SQLM2MSecretRepository)(nil)
)

// NewSQLM2MRepositories 创建客户端与密钥仓储（共享同一连接池）。
func NewSQLM2MRepositories(sqlDB *sql.DB) (*SQLM2MClientRepository, *SQLM2MSecretRepository) {
	return &SQLM2MClientRepository{sql: sqlDB}, &SQLM2MSecretRepository{sql: sqlDB}
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLM2MClientRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLM2MSecretRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByClientID 按对外标识加载客户端；软删客户端仍返回，用于阻止 client_id 复用。
func (r *SQLM2MClientRepository) FindByClientID(ctx context.Context, clientID string) (*domain.M2MClient, error) {
	target := strings.TrimSpace(clientID)
	if target == "" {
		return nil, apperr.BadRequest("client_id is required")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+m2mClientSelectColumns+` FROM identity_m2m_client WHERE client_id = ?`, target)
	client, err := scanM2MClient(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.NotFound("m2m client not found")
		}
		return nil, err
	}
	return client, nil
}

// FindByID 按主键加载客户端。
func (r *SQLM2MClientRepository) FindByID(ctx context.Context, id string) (*domain.M2MClient, error) {
	pk, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+m2mClientSelectColumns+` FROM identity_m2m_client WHERE id = ?`, pk)
	client, err := scanM2MClient(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.NotFound("m2m client not found")
		}
		return nil, err
	}
	return client, nil
}

// Save 创建（expectedVersion=0）或乐观锁更新客户端。
func (r *SQLM2MClientRepository) Save(ctx context.Context, client *domain.M2MClient, expectedVersion int64) error {
	if client == nil {
		return apperr.BadRequest("m2m client is required")
	}
	channels, err := encodeM2MChannels(client.AllowedChannels)
	if err != nil {
		return err
	}
	scopes, err := encodeM2MScopes(client.AllowedScopes)
	if err != nil {
		return err
	}
	pk, err := parseID(client.ID)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_m2m_client (
			id, client_id, name, status, allowed_channels, allowed_scopes,
			token_version, owner, created_by, rotation_due_at, created_at, updated_at, deleted_at, version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			pk, client.ClientID, client.Name, string(client.Status), channels, scopes,
			client.TokenVersion, client.Owner, client.CreatedBy, nullTime(client.RotationDueAt),
			client.CreatedAt.UTC(), client.UpdatedAt.UTC(), nullTime(client.DeletedAt),
		)
		return conflictOnDuplicate(err, "m2m client_id already exists")
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_m2m_client SET
			name = ?, status = ?, allowed_channels = ?, allowed_scopes = ?,
			token_version = ?, owner = ?, rotation_due_at = ?, updated_at = ?,
			deleted_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		client.Name, string(client.Status), channels, scopes,
		client.TokenVersion, client.Owner, nullTime(client.RotationDueAt), client.UpdatedAt.UTC(),
		nullTime(client.DeletedAt), pk, expectedVersion,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 版本不匹配或记录不存在：统一按冲突返回，避免暴露主键存在性。
		return apperr.Conflict("m2m client version conflict")
	}
	return nil
}

// List 分页列出客户端（含软删），按创建时间倒序。
func (r *SQLM2MClientRepository) List(ctx context.Context, filter domain.M2MClientListFilter) ([]*domain.M2MClient, int, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(filter.Status))
	}
	if filter.Channel != "" {
		// JSON_CONTAINS 命中 allowed_channels 中的端别字符串。
		where = append(where, "JSON_CONTAINS(allowed_channels, JSON_QUOTE(?))")
		args = append(args, string(filter.Channel))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_m2m_client`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+m2mClientSelectColumns+` FROM identity_m2m_client`+clause+
			` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	clients := make([]*domain.M2MClient, 0, limit)
	for rows.Next() {
		client, scanErr := scanM2MClient(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return clients, total, nil
}

// ListByClient 返回客户端全部密钥，按创建时间倒序。
func (r *SQLM2MSecretRepository) ListByClient(ctx context.Context, clientID string) ([]*domain.M2MSecret, error) {
	target := strings.TrimSpace(clientID)
	if target == "" {
		return nil, apperr.BadRequest("client_id is required")
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+m2mSecretSelectColumns+` FROM identity_m2m_secret WHERE client_id = ? ORDER BY created_at DESC, id DESC`,
		target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	secrets := make([]*domain.M2MSecret, 0, domain.MaxM2MSecretsPerClient)
	for rows.Next() {
		secret, scanErr := scanM2MSecret(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return secrets, nil
}

// FindByHash 按摘要加载密钥；用于签发端点的凭据校验。
func (r *SQLM2MSecretRepository) FindByHash(ctx context.Context, secretHash string) (*domain.M2MSecret, error) {
	target := strings.TrimSpace(secretHash)
	if target == "" {
		return nil, apperr.BadRequest("secret hash is required")
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+m2mSecretSelectColumns+` FROM identity_m2m_secret WHERE secret_hash = ?`, target)
	secret, err := scanM2MSecret(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.NotFound("m2m secret not found")
		}
		return nil, err
	}
	return secret, nil
}

// Save 创建密钥。
func (r *SQLM2MSecretRepository) Save(ctx context.Context, secret *domain.M2MSecret) error {
	if secret == nil {
		return apperr.BadRequest("m2m secret is required")
	}
	pk, err := parseID(secret.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_m2m_secret (
			id, client_id, secret_hash, fingerprint, status, created_at, expires_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		pk, secret.ClientID, secret.SecretHash, secret.Fingerprint, string(secret.Status),
		secret.CreatedAt.UTC(), nullTime(secret.ExpiresAt), nullTime(secret.RevokedAt),
	)
	return conflictOnDuplicate(err, "m2m secret already exists")
}

// UpdateStatus 更新密钥状态、宽限到期与吊销时间。
func (r *SQLM2MSecretRepository) UpdateStatus(ctx context.Context, secret *domain.M2MSecret) error {
	if secret == nil {
		return apperr.BadRequest("m2m secret is required")
	}
	pk, err := parseID(secret.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE identity_m2m_secret SET status = ?, expires_at = ?, revoked_at = ? WHERE id = ?`,
		string(secret.Status), nullTime(secret.ExpiresAt), nullTime(secret.RevokedAt), pk)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.NotFound("m2m secret not found")
	}
	return nil
}

// RevokeAll 吊销客户端全部未吊销密钥。
func (r *SQLM2MSecretRepository) RevokeAll(ctx context.Context, clientID string, now time.Time) (int, error) {
	target := strings.TrimSpace(clientID)
	if target == "" {
		return 0, apperr.BadRequest("client_id is required")
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE identity_m2m_secret SET status = ?, revoked_at = ? WHERE client_id = ? AND status <> ?`,
		string(domain.M2MSecretRevoked), now.UTC(), target, string(domain.M2MSecretRevoked))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// scanM2MClient 扫描客户端行并解码 JSON 列。
func scanM2MClient(row scanRow) (*domain.M2MClient, error) {
	var (
		client    domain.M2MClient
		status    string
		channels  []byte
		scopes    []byte
		rotation  sql.NullTime
		deletedAt sql.NullTime
	)
	if err := row.Scan(&client.ID, &client.ClientID, &client.Name, &status, &channels, &scopes,
		&client.TokenVersion, &client.Owner, &client.CreatedBy, &rotation, &client.CreatedAt, &client.UpdatedAt, &deletedAt,
		&client.Version); err != nil {
		return nil, err
	}
	client.Status = domain.M2MClientStatus(status)
	if err := decodeM2MChannels(channels, &client.AllowedChannels); err != nil {
		return nil, err
	}
	if err := decodeM2MScopes(scopes, &client.AllowedScopes); err != nil {
		return nil, err
	}
	client.RotationDueAt = nullableTime(rotation)
	client.CreatedAt = client.CreatedAt.UTC()
	client.UpdatedAt = client.UpdatedAt.UTC()
	client.DeletedAt = nullableTime(deletedAt)
	return &client, nil
}

// scanM2MSecret 扫描密钥行。
func scanM2MSecret(row scanRow) (*domain.M2MSecret, error) {
	var (
		secret    domain.M2MSecret
		status    string
		expiresAt sql.NullTime
		revokedAt sql.NullTime
	)
	if err := row.Scan(&secret.ID, &secret.ClientID, &secret.SecretHash, &secret.Fingerprint, &status,
		&secret.CreatedAt, &expiresAt, &revokedAt); err != nil {
		return nil, err
	}
	secret.Status = domain.M2MSecretStatus(status)
	secret.CreatedAt = secret.CreatedAt.UTC()
	secret.ExpiresAt = nullableTime(expiresAt)
	secret.RevokedAt = nullableTime(revokedAt)
	return &secret, nil
}

// encodeM2MChannels 把端别列表编码为 JSON 数组。
func encodeM2MChannels(channels []domain.Channel) ([]byte, error) {
	values := make([]string, 0, len(channels))
	for _, item := range channels {
		values = append(values, string(item))
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return nil, apperr.Internal("encode m2m allowed_channels failed", err)
	}
	return payload, nil
}

// encodeM2MScopes 把权限码列表编码为 JSON 数组。
func encodeM2MScopes(scopes []string) ([]byte, error) {
	payload, err := json.Marshal(scopes)
	if err != nil {
		return nil, apperr.Internal("encode m2m allowed_scopes failed", err)
	}
	return payload, nil
}

// decodeM2MChannels 解码 allowed_channels。
func decodeM2MChannels(raw []byte, out *[]domain.Channel) error {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return apperr.Internal("decode m2m allowed_channels failed", err)
	}
	channels := make([]domain.Channel, 0, len(values))
	for _, item := range values {
		channels = append(channels, domain.Channel(item))
	}
	*out = channels
	return nil
}

// decodeM2MScopes 解码 allowed_scopes。
func decodeM2MScopes(raw []byte, out *[]string) error {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return apperr.Internal("decode m2m allowed_scopes failed", err)
	}
	if values == nil {
		values = []string{}
	}
	*out = values
	return nil
}
