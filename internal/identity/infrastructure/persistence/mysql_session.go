package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// sessionSelectColumns 是会话表的显式列清单。
const sessionSelectColumns = `id, account_id, family_id, refresh_token_hash, status, token_version, version,
	expires_at, idle_expires_at, last_used_at, revoked_at, revoked_reason, created_at, updated_at`

// familySelectColumns 是设备会话表的显式列清单。
const familySelectColumns = `family_id, account_id, status, version, created_at, last_used_at, expires_at,
	absolute_expires_at, ip_hash, ua_hash, device_label, revoked_at, revoked_reason, updated_at`

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

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLSessionRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// FindByID 按主键加载会话。
func (r *SQLSessionRepository) FindByID(ctx context.Context, id string) (*domain.Session, error) {
	sessionID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+sessionSelectColumns+` FROM identity_session WHERE id = ?`, sessionID)
	session, err := scanSession(row)
	if err != nil {
		return nil, resolveNotFound(err, "session not found")
	}
	return session, nil
}

// FindByRefreshHash 按刷新令牌摘要加载会话。
func (r *SQLSessionRepository) FindByRefreshHash(ctx context.Context, hash string) (*domain.Session, error) {
	row := r.exec(ctx).QueryRowContext(ctx,
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
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_session
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
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session
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
	err = r.exec(ctx).QueryRowContext(ctx, `SELECT version FROM identity_session WHERE id = ?`, sessionID).Scan(&current)
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
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session
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
	if _, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session_family
		SET status = 'revoked', revoked_at = ?, revoked_reason = ?, updated_at = ?, version = version + 1
		WHERE family_id = ? AND status = 'active'`,
		now.UTC(), string(reason), now.UTC(), family); err != nil {
		return 0, err
	}
	return int(affected), nil
}

// RevokeByAccount 撤销账号下所有 active / rotated 会话，返回受影响条数。
func (r *SQLSessionRepository) RevokeByAccount(ctx context.Context, accountID string, reason domain.RevokeReason, now time.Time) (int, error) {
	if err := reason.Validate(); err != nil {
		return 0, err
	}
	id, err := parseID(accountID)
	if err != nil {
		return 0, err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session
		SET status = 'revoked', revoked_at = ?, revoked_reason = ?, updated_at = ?, version = version + 1
		WHERE account_id = ? AND status IN ('active', 'rotated')`,
		now.UTC(), string(reason), now.UTC(), id)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session_family
		SET status = 'revoked', revoked_at = ?, revoked_reason = ?, updated_at = ?, version = version + 1
		WHERE account_id = ? AND status = 'active'`,
		now.UTC(), string(reason), now.UTC(), id); err != nil {
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
	rows, err := r.exec(ctx).QueryContext(ctx,
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

// FindFamily 按主键加载设备会话。
func (r *SQLSessionRepository) FindFamily(ctx context.Context, familyID string) (*domain.SessionFamily, error) {
	id, err := parseID(familyID)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+familySelectColumns+` FROM identity_session_family WHERE family_id = ?`, id)
	family, err := scanFamily(row)
	if err != nil {
		return nil, resolveNotFound(err, "session family not found")
	}
	return family, nil
}

// SaveFamily 创建（expectedVersion=0）或更新设备会话。
func (r *SQLSessionRepository) SaveFamily(ctx context.Context, family *domain.SessionFamily, expectedVersion int64) error {
	if family == nil {
		return apperr.BadRequest("session family is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity session repository is not configured")
	}
	if expectedVersion == 0 {
		return r.insertFamily(ctx, family)
	}
	return r.updateFamily(ctx, family, expectedVersion)
}

// insertFamily 写入新设备会话。
func (r *SQLSessionRepository) insertFamily(ctx context.Context, family *domain.SessionFamily) error {
	familyID, err := parseID(family.ID)
	if err != nil {
		return err
	}
	accountID, err := parseID(family.AccountID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_session_family
		(family_id, account_id, status, version, created_at, last_used_at, expires_at,
		 absolute_expires_at, ip_hash, ua_hash, device_label, revoked_at, revoked_reason, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		familyID, accountID, string(family.Status), family.Version,
		family.CreatedAt.UTC(), family.LastUsedAt.UTC(), family.ExpiresAt.UTC(),
		family.AbsoluteExpiresAt.UTC(), family.IPHash, family.UAHash, family.DeviceLabel,
		nullTime(family.RevokedAt), string(family.RevokedReason), family.UpdatedAt.UTC())
	return conflictOnDuplicate(err, "session family id already exists")
}

// updateFamily 以乐观锁更新设备会话状态与时间字段。
func (r *SQLSessionRepository) updateFamily(ctx context.Context, family *domain.SessionFamily, expectedVersion int64) error {
	familyID, err := parseID(family.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session_family
		SET status = ?, version = ?, last_used_at = ?, expires_at = ?, absolute_expires_at = ?,
		    revoked_at = ?, revoked_reason = ?, updated_at = ?
		WHERE family_id = ? AND version = ?`,
		string(family.Status), family.Version, family.LastUsedAt.UTC(), family.ExpiresAt.UTC(),
		family.AbsoluteExpiresAt.UTC(), nullTime(family.RevokedAt), string(family.RevokedReason),
		family.UpdatedAt.UTC(), familyID, expectedVersion)
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
	err = r.exec(ctx).QueryRowContext(ctx,
		`SELECT version FROM identity_session_family WHERE family_id = ?`, familyID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("session family not found")
	}
	if err != nil {
		return err
	}
	return apperr.Conflict("session family version conflict")
}

// ListFamiliesByAccount 按 last_used_at / family_id 倒序返回设备会话。
func (r *SQLSessionRepository) ListFamiliesByAccount(
	ctx context.Context,
	accountID string,
	afterLastUsed time.Time,
	afterID string,
	limit int,
) ([]*domain.SessionFamily, error) {
	id, err := parseID(accountID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT ` + familySelectColumns + ` FROM identity_session_family
		WHERE account_id = ? AND status = 'active'`
	args := []any{id}
	if !afterLastUsed.IsZero() && afterID != "" {
		cursorID, cursorErr := parseID(afterID)
		if cursorErr != nil {
			return nil, cursorErr
		}
		query += ` AND (last_used_at < ? OR (last_used_at = ? AND family_id < ?))`
		args = append(args, afterLastUsed.UTC(), afterLastUsed.UTC(), cursorID)
	}
	query += ` ORDER BY last_used_at DESC, family_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.exec(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.SessionFamily, 0, limit)
	for rows.Next() {
		family, scanErr := scanFamily(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, family)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListByFamily 返回 family 下全部会话，按创建时间倒序、ID 升序。
func (r *SQLSessionRepository) ListByFamily(ctx context.Context, familyID string) ([]*domain.Session, error) {
	id, err := parseID(familyID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+sessionSelectColumns+` FROM identity_session WHERE family_id = ?
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

// ExpireStale 把到期的会话与设备标记为 expired 单次最多 limit 条。
func (r *SQLSessionRepository) ExpireStale(ctx context.Context, now time.Time, limit int) (domain.SessionExpireResult, error) {
	if limit <= 0 {
		limit = 500
	}
	var result domain.SessionExpireResult
	sessionResult, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session
		SET status = 'expired', updated_at = ?, version = version + 1
		WHERE status IN ('active', 'rotated')
		  AND (expires_at <= ? OR (idle_expires_at IS NOT NULL AND idle_expires_at <= ?))
		ORDER BY expires_at LIMIT ?`, now.UTC(), now.UTC(), now.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := sessionResult.RowsAffected(); rowsErr == nil {
		result.Sessions = int(affected)
	}
	familyResult, err := r.exec(ctx).ExecContext(ctx, `UPDATE identity_session_family
		SET status = 'expired', updated_at = ?, version = version + 1
		WHERE status = 'active' AND (expires_at <= ? OR absolute_expires_at <= ?)
		ORDER BY expires_at LIMIT ?`, now.UTC(), now.UTC(), now.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := familyResult.RowsAffected(); rowsErr == nil {
		result.Families = int(affected)
	}
	return result, nil
}

// DeleteExpiredBefore 删除保留期外的会话与设备，单次最多 limit 条。
func (r *SQLSessionRepository) DeleteExpiredBefore(ctx context.Context, before time.Time, limit int) (domain.SessionDeleteResult, error) {
	if limit <= 0 {
		limit = 500
	}
	var result domain.SessionDeleteResult
	sessionResult, err := r.exec(ctx).ExecContext(ctx, `DELETE FROM identity_session
		WHERE status IN ('rotated', 'revoked', 'expired') AND updated_at < ?
		ORDER BY updated_at LIMIT ?`, before.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := sessionResult.RowsAffected(); rowsErr == nil {
		result.Sessions = int(affected)
	}
	familyResult, err := r.exec(ctx).ExecContext(ctx, `DELETE FROM identity_session_family
		WHERE status IN ('revoked', 'expired') AND updated_at < ?
		ORDER BY updated_at LIMIT ?`, before.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := familyResult.RowsAffected(); rowsErr == nil {
		result.Families = int(affected)
	}
	return result, nil
}

// scanFamily 扫描一行设备会话；可空列允许 NULL。
func scanFamily(row scanRow) (*domain.SessionFamily, error) {
	var (
		family    domain.SessionFamily
		revokedAt sql.NullTime
	)
	err := row.Scan(
		&family.ID, &family.AccountID, &family.Status, &family.Version,
		&family.CreatedAt, &family.LastUsedAt, &family.ExpiresAt, &family.AbsoluteExpiresAt,
		&family.IPHash, &family.UAHash, &family.DeviceLabel, &revokedAt,
		&family.RevokedReason, &family.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	family.CreatedAt = family.CreatedAt.UTC()
	family.LastUsedAt = family.LastUsedAt.UTC()
	family.ExpiresAt = family.ExpiresAt.UTC()
	family.AbsoluteExpiresAt = family.AbsoluteExpiresAt.UTC()
	family.UpdatedAt = family.UpdatedAt.UTC()
	family.RevokedAt = nullableTime(revokedAt)
	return &family, nil
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
