package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// sessionSelectColumns 是会话表的显式列清单。
const sessionSelectColumns = `id, account_id, family_id, refresh_token_hash, status, token_version, version,
	expires_at, idle_expires_at, last_used_at, revoked_at, revoked_reason, created_at, updated_at`

// SQLSessionRepository 是 MySQL 8.0 的会话仓储实现。
//
// 刷新并发：Save 使用 version 乐观锁，只有一个请求能把 active 改为 rotated；
// 其余请求受影响行数为 0，由调用方按重放处理。
type SQLSessionRepository struct {
	sql *sql.DB
}

// 编译期断言。
var _ domain.SessionRepository = (*SQLSessionRepository)(nil)

// NewSQLSessionRepository 基于已打开的 *sql.DB 创建会话仓储。
func NewSQLSessionRepository(sqlDB *sql.DB) *SQLSessionRepository {
	return &SQLSessionRepository{sql: sqlDB}
}

// FindByID 按主键加载会话。
func (r *SQLSessionRepository) FindByID(ctx context.Context, id string) (*domain.Session, error) {
	sessionID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.sql.QueryRowContext(ctx,
		`SELECT `+sessionSelectColumns+` FROM identity_session WHERE id = ?`, sessionID)
	session, err := scanSession(row)
	if err != nil {
		return nil, resolveNotFound(err, "session not found")
	}
	return session, nil
}

// FindByRefreshHash 按刷新令牌摘要加载会话。
func (r *SQLSessionRepository) FindByRefreshHash(ctx context.Context, hash string) (*domain.Session, error) {
	row := r.sql.QueryRowContext(ctx,
		`SELECT `+sessionSelectColumns+` FROM identity_session WHERE refresh_token_hash = ?`, hash)
	session, err := scanSession(row)
	if err != nil {
		return nil, resolveNotFound(err, "session not found")
	}
	return session, nil
}

// Save 创建（expectedVersion=0）或更新会话。
func (r *SQLSessionRepository) Save(ctx context.Context, session *domain.Session, expectedVersion int64) error {
	if session == nil {
		return apperr.BadRequest("session is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity session repository is not configured")
	}
	if expectedVersion == 0 {
		return r.insert(ctx, session)
	}
	return r.update(ctx, session, expectedVersion)
}

// insert 写入新会话；刷新哈希冲突映射为 40900。
func (r *SQLSessionRepository) insert(ctx context.Context, session *domain.Session) error {
	sessionID, err := parseID(session.ID)
	if err != nil {
		return err
	}
	accountID, err := parseID(session.AccountID)
	if err != nil {
		return err
	}
	familyID, err := parseID(session.FamilyID)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO identity_session
		(id, account_id, family_id, refresh_token_hash, status, token_version, version,
		 expires_at, idle_expires_at, last_used_at, revoked_at, revoked_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, accountID, familyID, session.RefreshHash, string(session.Status),
		session.TokenVersion, session.Version, session.ExpiresAt.UTC(),
		nullTimeValue(session.IdleExpiresAt), session.LastUsedAt.UTC(),
		nullTime(session.RevokedAt), string(session.RevokedReason),
		session.CreatedAt.UTC(), session.UpdatedAt.UTC())
	return conflictOnDuplicate(err, "refresh token hash already exists")
}

// update 以乐观锁更新会话状态。
func (r *SQLSessionRepository) update(ctx context.Context, session *domain.Session, expectedVersion int64) error {
	sessionID, err := parseID(session.ID)
	if err != nil {
		return err
	}
	result, err := r.sql.ExecContext(ctx, `UPDATE identity_session
		SET status = ?, version = ?, last_used_at = ?, revoked_at = ?, revoked_reason = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		string(session.Status), session.Version, session.LastUsedAt.UTC(),
		nullTime(session.RevokedAt), string(session.RevokedReason),
		session.UpdatedAt.UTC(), sessionID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var current int64
	err = r.sql.QueryRowContext(ctx, `SELECT version FROM identity_session WHERE id = ?`, sessionID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("session not found")
	}
	if err != nil {
		return err
	}
	return apperr.Conflict("session version conflict")
}

// RevokeFamily 撤销一个家庭下所有 active / rotated 会话，返回受影响条数。
func (r *SQLSessionRepository) RevokeFamily(ctx context.Context, familyID string, reason domain.RevokeReason, now time.Time) (int, error) {
	if err := reason.Validate(); err != nil {
		return 0, err
	}
	family, err := parseID(familyID)
	if err != nil {
		return 0, err
	}
	result, err := r.sql.ExecContext(ctx, `UPDATE identity_session
		SET status = 'revoked', revoked_at = ?, revoked_reason = ?, updated_at = ?, version = version + 1
		WHERE family_id = ? AND status IN ('active', 'rotated')`,
		now.UTC(), string(reason), now.UTC(), family)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// ListByAccount 返回账号全部会话，按创建时间倒序、ID 升序。
func (r *SQLSessionRepository) ListByAccount(ctx context.Context, accountID string) ([]*domain.Session, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx,
		`SELECT `+sessionSelectColumns+` FROM identity_session WHERE account_id = ?
		 ORDER BY created_at DESC, id ASC`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.Session, 0, 4)
	for rows.Next() {
		session, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanSession 扫描一行会话；可空列允许 NULL。
func scanSession(row scanRow) (*domain.Session, error) {
	var (
		session       domain.Session
		idleExpiresAt sql.NullTime
		revokedAt     sql.NullTime
	)
	err := row.Scan(
		&session.ID, &session.AccountID, &session.FamilyID, &session.RefreshHash,
		&session.Status, &session.TokenVersion, &session.Version,
		&session.ExpiresAt, &idleExpiresAt, &session.LastUsedAt, &revokedAt,
		&session.RevokedReason, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	session.ExpiresAt = session.ExpiresAt.UTC()
	session.LastUsedAt = session.LastUsedAt.UTC()
	session.CreatedAt = session.CreatedAt.UTC()
	session.UpdatedAt = session.UpdatedAt.UTC()
	if idleExpiresAt.Valid {
		session.IdleExpiresAt = idleExpiresAt.Time.UTC()
	}
	session.RevokedAt = nullableTime(revokedAt)
	return &session, nil
}

// nullTimeValue 把零值时间写为 NULL，非零值写 UTC。
func nullTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
