package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	absolute_expires_at, ip_hash, ua_hash, device_type, os_name, browser_name, ip_masked,
	device_label, revoked_at, revoked_reason, updated_at`

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
		 absolute_expires_at, ip_hash, ua_hash, device_type, os_name, browser_name, ip_masked,
		 device_label, revoked_at, revoked_reason, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		familyID, accountID, string(family.Status), family.Version,
		family.CreatedAt.UTC(), family.LastUsedAt.UTC(), family.ExpiresAt.UTC(),
		family.AbsoluteExpiresAt.UTC(), family.IPHash, family.UAHash,
		family.Device.Type, family.Device.OS, family.Device.Browser, family.Device.IPMasked,
		family.DeviceLabel, nullTime(family.RevokedAt), string(family.RevokedReason), family.UpdatedAt.UTC())
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
		    device_type = ?, os_name = ?, browser_name = ?, ip_masked = ?,
		    revoked_at = ?, revoked_reason = ?, updated_at = ?
		WHERE family_id = ? AND version = ?`,
		string(family.Status), family.Version, family.LastUsedAt.UTC(), family.ExpiresAt.UTC(),
		family.AbsoluteExpiresAt.UTC(),
		family.Device.Type, family.Device.OS, family.Device.Browser, family.Device.IPMasked,
		nullTime(family.RevokedAt), string(family.RevokedReason),
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
//
// 依赖 000008 新增的 updated_at 索引；调用方需按批循环直到本批未满。
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

// ArchiveExpiredBefore 在同一事务内把过期会话 / 设备写入归档表并删除在线行（IDN-GAP-P1-04）。
//
// 先按 updated_at 顺序选出本批主键，再以主键集合做 INSERT ... SELECT 与 DELETE，
// 保证归档内容与删除行完全一致；任一步失败整体回滚，不会出现「已归档未删除」。
// 仅由 worker 清理任务调用，不支持嵌套在请求事务中（需要独立的 sql.Tx 控制提交时机）。
func (r *SQLSessionRepository) ArchiveExpiredBefore(
	ctx context.Context,
	before time.Time,
	limit int,
	archivedAt time.Time,
) (domain.SessionDeleteResult, error) {
	if r.sql == nil {
		return domain.SessionDeleteResult{}, apperr.Unavailable("identity session repository is not configured")
	}
	if limit <= 0 {
		limit = 500
	}
	tx, err := r.sql.BeginTx(ctx, nil)
	if err != nil {
		return domain.SessionDeleteResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var result domain.SessionDeleteResult
	sessionIDs, err := selectArchiveIDs(ctx, tx, "identity_session", "id",
		"status IN ('rotated', 'revoked', 'expired') AND updated_at < ?", before, limit)
	if err != nil {
		return result, err
	}
	if len(sessionIDs) > 0 {
		if err := insertArchivedSessions(ctx, tx, sessionIDs, archivedAt); err != nil {
			return result, err
		}
		if err := deleteByIDs(ctx, tx, "identity_session", "id", sessionIDs); err != nil {
			return result, err
		}
		result.Sessions = len(sessionIDs)
	}

	familyIDs, err := selectArchiveIDs(ctx, tx, "identity_session_family", "family_id",
		"status IN ('revoked', 'expired') AND updated_at < ?", before, limit)
	if err != nil {
		return result, err
	}
	if len(familyIDs) > 0 {
		if err := insertArchivedFamilies(ctx, tx, familyIDs, archivedAt); err != nil {
			return result, err
		}
		if err := deleteByIDs(ctx, tx, "identity_session_family", "family_id", familyIDs); err != nil {
			return result, err
		}
		result.Families = len(familyIDs)
	}

	if err := tx.Commit(); err != nil {
		return domain.SessionDeleteResult{}, err
	}
	return result, nil
}

// DeleteArchivedBefore 删除归档保留期外的历史会话 / 设备，单次最多 limit 条。
func (r *SQLSessionRepository) DeleteArchivedBefore(
	ctx context.Context,
	before time.Time,
	limit int,
) (domain.SessionDeleteResult, error) {
	if limit <= 0 {
		limit = 500
	}
	var result domain.SessionDeleteResult
	sessionResult, err := r.exec(ctx).ExecContext(ctx, `DELETE FROM identity_session_archive
		WHERE archived_at < ? ORDER BY archived_at LIMIT ?`, before.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := sessionResult.RowsAffected(); rowsErr == nil {
		result.Sessions = int(affected)
	}
	familyResult, err := r.exec(ctx).ExecContext(ctx, `DELETE FROM identity_session_family_archive
		WHERE archived_at < ? ORDER BY archived_at LIMIT ?`, before.UTC(), limit)
	if err != nil {
		return result, err
	}
	if affected, rowsErr := familyResult.RowsAffected(); rowsErr == nil {
		result.Families = int(affected)
	}
	return result, nil
}

// selectArchiveIDs 选出本批待归档主键；idColumn 为对应用户表主键列名。
func selectArchiveIDs(
	ctx context.Context,
	tx *sql.Tx,
	table, idColumn, condition string,
	before time.Time,
	limit int,
) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+idColumn+` FROM `+table+` WHERE `+condition+` ORDER BY updated_at LIMIT ?`,
		before.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// insertArchivedSessions 以主键集合把在线会话复制到归档表。
func insertArchivedSessions(ctx context.Context, tx *sql.Tx, ids []int64, archivedAt time.Time) error {
	placeholders, args := idPlaceholders(ids, archivedAt.UTC())
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_session_archive
		(id, account_id, family_id, refresh_token_hash, status, token_version, version,
		 expires_at, idle_expires_at, last_used_at, revoked_at, revoked_reason,
		 created_at, updated_at, archived_at)
		SELECT id, account_id, family_id, refresh_token_hash, status, token_version, version,
		 expires_at, idle_expires_at, last_used_at, revoked_at, revoked_reason,
		 created_at, updated_at, ?
		FROM identity_session WHERE id IN (`+placeholders+`)`, args...)
	return err
}

// insertArchivedFamilies 以主键集合把在线设备会话复制到归档表。
func insertArchivedFamilies(ctx context.Context, tx *sql.Tx, ids []int64, archivedAt time.Time) error {
	placeholders, args := idPlaceholders(ids, archivedAt.UTC())
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_session_family_archive
		(family_id, account_id, status, version, created_at, last_used_at, expires_at,
		 absolute_expires_at, ip_hash, ua_hash, device_label, revoked_at, revoked_reason,
		 updated_at, archived_at)
		SELECT family_id, account_id, status, version, created_at, last_used_at, expires_at,
		 absolute_expires_at, ip_hash, ua_hash, device_label, revoked_at, revoked_reason,
		 updated_at, ?
		FROM identity_session_family WHERE family_id IN (`+placeholders+`)`, args...)
	return err
}

// deleteByIDs 按主键集合删除在线行。
func deleteByIDs(ctx context.Context, tx *sql.Tx, table, idColumn string, ids []int64) error {
	placeholders, args := idPlaceholders(ids)
	_, err := tx.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE `+idColumn+` IN (`+placeholders+`)`, args...)
	return err
}

// idPlaceholders 构造 IN 列表占位符；extra 为插入 SELECT 的额外参数（放在最前）。
func idPlaceholders(ids []int64, extra ...any) (string, []any) {
	args := make([]any, 0, len(extra)+len(ids))
	args = append(args, extra...)
	var builder strings.Builder
	for i, id := range ids {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("?")
		args = append(args, id)
	}
	return builder.String(), args
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
		&family.IPHash, &family.UAHash,
		&family.Device.Type, &family.Device.OS, &family.Device.Browser, &family.Device.IPMasked,
		&family.DeviceLabel, &revokedAt,
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
