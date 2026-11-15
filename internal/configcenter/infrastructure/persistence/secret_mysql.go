package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// SecretRepositories 汇总 MySQL 密钥仓储。
type SecretRepositories struct {
	Secrets domain.SecretRepository
	Grants  domain.SecretGrantRepository
	Access  domain.SecretAccessRepository
}

// NewSecrets 构造 MySQL 密钥仓储；access 使用进程内缓冲（审计主路径走 audit_event）。
func NewSecrets(database *sql.DB) (*SecretRepositories, error) {
	if database == nil {
		return nil, fmt.Errorf("configcenter secret mysql requires db")
	}
	access := &memoryAccessRepo{}
	return &SecretRepositories{
		Secrets: &secretMySQL{db: database},
		Grants:  &grantMySQL{db: database},
		Access:  access,
	}, nil
}

type secretMySQL struct{ db *sql.DB }

func (r *secretMySQL) conn(ctx context.Context) db.Querier {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// Create 写入密钥元数据与首个密文版本。
func (r *secretMySQL) Create(ctx context.Context, secret domain.Secret, version domain.SecretVersion) error {
	q := r.conn(ctx)
	_, err := q.ExecContext(ctx, `
INSERT INTO config_secret (
  id, env, namespace, name, description, owner, status, active_version, version,
  rotate_interval_days, dual_version, submitted_by, pending_action, pending_payload,
  expires_at, created_by, updated_by, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		secret.ID, secret.Env, secret.Namespace, secret.Name, secret.Description, secret.Owner,
		string(secret.Status), secret.ActiveVersion, secret.Version, secret.RotateIntervalDays,
		boolToTiny(secret.DualVersion), secret.SubmittedBy, secret.PendingAction, secret.PendingPayload,
		nullTime(secret.ExpiresAt),
		secret.CreatedBy, secret.UpdatedBy, secret.CreatedAt, secret.UpdatedAt)
	if err != nil {
		if isDuplicate(err) {
			return domain.ErrDuplicateKey
		}
		return err
	}
	_, err = q.ExecContext(ctx, `
INSERT INTO config_secret_version (
  id, secret_id, version, ciphertext, nonce, wrapped_dek, kek_key_id, algorithm,
  value_hash, status, created_by, created_at, revoked_at, destroyed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.SecretID, version.Version, version.Ciphertext, version.Nonce,
		version.WrappedDEK, version.KEKKeyID, version.Algorithm, version.ValueHash,
		string(version.Status), version.CreatedBy, version.CreatedAt,
		nullTime(version.RevokedAt), nullTime(version.DestroyedAt))
	return err
}

// GetByID 按主键读取密钥元数据。
func (r *secretMySQL) GetByID(ctx context.Context, id int64) (domain.Secret, error) {
	return r.scanOne(ctx, `SELECT `+secretSelectCols+` FROM config_secret WHERE id = ?`, id)
}

// GetByName 按环境 / namespace / 名称读取密钥元数据。
func (r *secretMySQL) GetByName(ctx context.Context, env, namespace, name string) (domain.Secret, error) {
	return r.scanOne(ctx, `SELECT `+secretSelectCols+` FROM config_secret WHERE env=? AND namespace=? AND name=?`,
		env, namespace, name)
}

const secretSelectCols = `id, env, namespace, name, description, owner, status, active_version, version,
 rotate_interval_days, dual_version, submitted_by, pending_action, pending_payload,
 expires_at, created_by, updated_by, created_at, updated_at`

func (r *secretMySQL) scanOne(ctx context.Context, query string, args ...any) (domain.Secret, error) {
	row := r.conn(ctx).QueryRowContext(ctx, query, args...)
	secret, err := scanSecret(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Secret{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Secret{}, err
	}
	if secret.ActiveVersion != nil {
		_ = r.conn(ctx).QueryRowContext(ctx,
			`SELECT value_hash FROM config_secret_version WHERE secret_id=? AND version=?`,
			secret.ID, *secret.ActiveVersion).Scan(&secret.Fingerprint)
	}
	return secret, nil
}

// List 分页列出密钥元数据。
func (r *secretMySQL) List(ctx context.Context, env, namespace string, status domain.SecretStatus, limit, offset int) ([]domain.Secret, int, error) {
	q := r.conn(ctx)
	where := `WHERE env=? AND namespace=?`
	args := []any{env, namespace}
	if status != "" {
		where += ` AND status=?`
		args = append(args, string(status))
	}
	var total int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(1) FROM config_secret `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT `+secretSelectCols+` FROM config_secret `+where+
		` ORDER BY name ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []domain.Secret
	for rows.Next() {
		secret, err := scanSecret(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, secret)
	}
	return out, total, rows.Err()
}

// UpdateCAS 按期望版本号条件更新密钥元数据。
func (r *secretMySQL) UpdateCAS(ctx context.Context, secret domain.Secret, expectedVersion int64) error {
	res, err := r.conn(ctx).ExecContext(ctx, `
UPDATE config_secret SET status=?, active_version=?, version=?, rotate_interval_days=?, dual_version=?,
  submitted_by=?, pending_action=?, pending_payload=?, expires_at=?, updated_by=?, updated_at=?,
  description=?, owner=?
 WHERE id=? AND version=?`,
		string(secret.Status), secret.ActiveVersion, secret.Version, secret.RotateIntervalDays,
		boolToTiny(secret.DualVersion), secret.SubmittedBy, secret.PendingAction, secret.PendingPayload,
		nullTime(secret.ExpiresAt),
		secret.UpdatedBy, secret.UpdatedAt, secret.Description, secret.Owner,
		secret.ID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrVersionConflict
	}
	return nil
}

// AppendVersion 追加一条密钥密文版本。
func (r *secretMySQL) AppendVersion(ctx context.Context, version domain.SecretVersion) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO config_secret_version (
  id, secret_id, version, ciphertext, nonce, wrapped_dek, kek_key_id, algorithm,
  value_hash, status, created_by, created_at, revoked_at, destroyed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.SecretID, version.Version, version.Ciphertext, version.Nonce,
		version.WrappedDEK, version.KEKKeyID, version.Algorithm, version.ValueHash,
		string(version.Status), version.CreatedBy, version.CreatedAt,
		nullTime(version.RevokedAt), nullTime(version.DestroyedAt))
	if isDuplicate(err) {
		return domain.ErrDuplicateKey
	}
	return err
}

// GetVersion 读取指定密文版本（含密文字段）。
func (r *secretMySQL) GetVersion(ctx context.Context, secretID, version int64) (domain.SecretVersion, error) {
	row := r.conn(ctx).QueryRowContext(ctx, `
SELECT id, secret_id, version, ciphertext, nonce, wrapped_dek, kek_key_id, algorithm,
       value_hash, status, created_by, created_at, revoked_at, destroyed_at
  FROM config_secret_version WHERE secret_id=? AND version=?`, secretID, version)
	var v domain.SecretVersion
	var status string
	var revoked, destroyed sql.NullTime
	err := row.Scan(&v.ID, &v.SecretID, &v.Version, &v.Ciphertext, &v.Nonce, &v.WrappedDEK,
		&v.KEKKeyID, &v.Algorithm, &v.ValueHash, &status, &v.CreatedBy, &v.CreatedAt, &revoked, &destroyed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SecretVersion{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SecretVersion{}, err
	}
	v.Status = domain.SecretVersionStatus(status)
	v.RevokedAt = timePtr(revoked)
	v.DestroyedAt = timePtr(destroyed)
	return v, nil
}

// ListVersions 分页列出密文版本元数据（不含密文）。
func (r *secretMySQL) ListVersions(ctx context.Context, secretID int64, limit, offset int) ([]domain.SecretVersion, int, error) {
	q := r.conn(ctx)
	var total int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(1) FROM config_secret_version WHERE secret_id=?`, secretID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, `
SELECT id, secret_id, version, kek_key_id, algorithm, value_hash, status, created_by, created_at, revoked_at, destroyed_at
  FROM config_secret_version WHERE secret_id=? ORDER BY version DESC LIMIT ? OFFSET ?`, secretID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []domain.SecretVersion
	for rows.Next() {
		var v domain.SecretVersion
		var status string
		var revoked, destroyed sql.NullTime
		if err := rows.Scan(&v.ID, &v.SecretID, &v.Version, &v.KEKKeyID, &v.Algorithm, &v.ValueHash,
			&status, &v.CreatedBy, &v.CreatedAt, &revoked, &destroyed); err != nil {
			return nil, 0, err
		}
		v.Status = domain.SecretVersionStatus(status)
		v.RevokedAt = timePtr(revoked)
		v.DestroyedAt = timePtr(destroyed)
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// UpdateVersionStatus 条件迁移密文版本状态，销毁时清空密文。
func (r *secretMySQL) UpdateVersionStatus(ctx context.Context, secretID, version int64, from, to domain.SecretVersionStatus, at time.Time) error {
	res, err := r.conn(ctx).ExecContext(ctx, `
UPDATE config_secret_version SET status=?, revoked_at=CASE WHEN ?='revoked' THEN ? ELSE revoked_at END,
  destroyed_at=CASE WHEN ?='destroyed' THEN ? ELSE destroyed_at END,
  ciphertext=CASE WHEN ?='destroyed' THEN NULL ELSE ciphertext END,
  wrapped_dek=CASE WHEN ?='destroyed' THEN NULL ELSE wrapped_dek END,
  nonce=CASE WHEN ?='destroyed' THEN NULL ELSE nonce END
 WHERE secret_id=? AND version=? AND status=?`,
		string(to), string(to), at, string(to), at, string(to), string(to), string(to),
		secretID, version, string(from))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrStateConflict
	}
	return nil
}

// AppendReview 追加一条密钥审批记录。
func (r *secretMySQL) AppendReview(ctx context.Context, review domain.SecretReview) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO config_secret_review (id, secret_id, action, reviewer, decision, comment, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.SecretID, string(review.Action), review.Reviewer,
		string(review.Decision), review.Comment, review.CreatedAt)
	return err
}

// ListReviews 列出密钥审批记录。
func (r *secretMySQL) ListReviews(ctx context.Context, secretID int64) ([]domain.SecretReview, error) {
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT id, secret_id, action, reviewer, decision, comment, created_at
  FROM config_secret_review WHERE secret_id=? ORDER BY created_at ASC`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SecretReview
	for rows.Next() {
		var rev domain.SecretReview
		var action, decision string
		if err := rows.Scan(&rev.ID, &rev.SecretID, &action, &rev.Reviewer, &decision, &rev.Comment, &rev.CreatedAt); err != nil {
			return nil, err
		}
		rev.Action = domain.SecretReviewAction(action)
		rev.Decision = domain.ReviewDecision(decision)
		out = append(out, rev)
	}
	return out, rows.Err()
}

type grantMySQL struct{ db *sql.DB }

func (r *grantMySQL) conn(ctx context.Context) db.Querier {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// Create 写入密钥授权记录。
func (r *grantMySQL) Create(ctx context.Context, grant domain.SecretGrant) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO config_secret_grant (
  id, env, namespace, service_identity, secret_id, permission, expires_at, created_by, created_at, revoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		grant.ID, grant.Env, grant.Namespace, grant.ServiceIdentity, grant.SecretID,
		grant.Permission, nullTime(grant.ExpiresAt), grant.CreatedBy, grant.CreatedAt, nullTime(grant.RevokedAt))
	if isDuplicate(err) {
		return domain.ErrDuplicateKey
	}
	return err
}

// GetByID 按主键读取授权。
func (r *grantMySQL) GetByID(ctx context.Context, id int64) (domain.SecretGrant, error) {
	row := r.conn(ctx).QueryRowContext(ctx, `
SELECT id, env, namespace, service_identity, secret_id, permission, expires_at, created_by, created_at, revoked_at
  FROM config_secret_grant WHERE id=?`, id)
	g, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SecretGrant{}, domain.ErrNotFound
	}
	return g, err
}

// ListBySecret 列出密钥的全部授权。
func (r *grantMySQL) ListBySecret(ctx context.Context, secretID int64) ([]domain.SecretGrant, error) {
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT id, env, namespace, service_identity, secret_id, permission, expires_at, created_by, created_at, revoked_at
  FROM config_secret_grant WHERE secret_id=? ORDER BY id ASC`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SecretGrant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FindActive 查找服务主体对密钥的有效 resolve 授权。
func (r *grantMySQL) FindActive(ctx context.Context, env, namespace, serviceIdentity string, secretID int64, now time.Time) (domain.SecretGrant, error) {
	row := r.conn(ctx).QueryRowContext(ctx, `
SELECT id, env, namespace, service_identity, secret_id, permission, expires_at, created_by, created_at, revoked_at
  FROM config_secret_grant
 WHERE env=? AND namespace=? AND service_identity=? AND secret_id=? AND permission='resolve'
   AND revoked_at IS NULL
   AND (expires_at IS NULL OR expires_at > ?)
 LIMIT 1`, env, namespace, serviceIdentity, secretID, now)
	g, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SecretGrant{}, domain.ErrNotFound
	}
	return g, err
}

// Activate 将待审授权转为可 resolve。
func (r *grantMySQL) Activate(ctx context.Context, id int64) error {
	res, err := r.conn(ctx).ExecContext(ctx, `
UPDATE config_secret_grant SET permission='resolve'
 WHERE id=? AND permission='pending' AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrStateConflict
	}
	return nil
}

// Revoke 吊销授权。
func (r *grantMySQL) Revoke(ctx context.Context, id int64, at time.Time) error {
	res, err := r.conn(ctx).ExecContext(ctx, `UPDATE config_secret_grant SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, at, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type memoryAccessRepo struct {
	items []domain.SecretAccess
}

// Append 追加密钥访问审计到进程内缓冲。
func (r *memoryAccessRepo) Append(_ context.Context, access domain.SecretAccess) error {
	r.items = append(r.items, access)
	if len(r.items) > 1000 {
		r.items = r.items[len(r.items)-1000:]
	}
	return nil
}

// ListBySecretName 按密钥名分页列出访问审计。
func (r *memoryAccessRepo) ListBySecretName(_ context.Context, _, _, name string, limit, offset int) ([]domain.SecretAccess, int, error) {
	var matched []domain.SecretAccess
	for i := len(r.items) - 1; i >= 0; i-- {
		if r.items[i].SecretName == name {
			matched = append(matched, r.items[i])
		}
	}
	total := len(matched)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

type secretScanner interface {
	Scan(dest ...any) error
}

func scanSecret(row secretScanner) (domain.Secret, error) {
	var s domain.Secret
	var status string
	var active sql.NullInt64
	var dual int
	var expires sql.NullTime
	err := row.Scan(&s.ID, &s.Env, &s.Namespace, &s.Name, &s.Description, &s.Owner, &status,
		&active, &s.Version, &s.RotateIntervalDays, &dual, &s.SubmittedBy,
		&s.PendingAction, &s.PendingPayload, &expires,
		&s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return domain.Secret{}, err
	}
	s.Status = domain.SecretStatus(status)
	s.DualVersion = dual != 0
	if active.Valid {
		v := active.Int64
		s.ActiveVersion = &v
	}
	s.ExpiresAt = timePtr(expires)
	return s, nil
}

func scanGrant(row secretScanner) (domain.SecretGrant, error) {
	var g domain.SecretGrant
	var expires, revoked sql.NullTime
	err := row.Scan(&g.ID, &g.Env, &g.Namespace, &g.ServiceIdentity, &g.SecretID, &g.Permission,
		&expires, &g.CreatedBy, &g.CreatedAt, &revoked)
	if err != nil {
		return domain.SecretGrant{}, err
	}
	g.ExpiresAt = timePtr(expires)
	g.RevokedAt = timePtr(revoked)
	return g, nil
}

func boolToTiny(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique")
}
