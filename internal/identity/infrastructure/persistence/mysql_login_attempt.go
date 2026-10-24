package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// SQLLoginAttemptRepository 是 MySQL 8.0 的登录尝试仓储实现，只追加、不更新。
type SQLLoginAttemptRepository struct {
	sql *sql.DB
}

// 编译期断言。
var _ domain.LoginAttemptRepository = (*SQLLoginAttemptRepository)(nil)

// NewSQLLoginAttemptRepository 基于已打开的 *sql.DB 创建登录尝试仓储。
func NewSQLLoginAttemptRepository(sqlDB *sql.DB) *SQLLoginAttemptRepository {
	return &SQLLoginAttemptRepository{sql: sqlDB}
}

// exec 返回当前请求应使用的执行器：事务上下文优先。
func (r *SQLLoginAttemptRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// Append 追加一条登录尝试。
func (r *SQLLoginAttemptRepository) Append(ctx context.Context, attempt *domain.LoginAttempt) error {
	if attempt == nil {
		return apperr.BadRequest("login attempt is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("identity login attempt repository is not configured")
	}
	id, err := parseID(attempt.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO identity_login_attempt
		(id, channel, account_ref, outcome, reason, ip_hash, ua_hash, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(attempt.Channel), attempt.AccountRef, string(attempt.Outcome),
		attempt.Reason, attempt.IPHash, attempt.UAHash, attempt.OccurredAt.UTC())
	return conflictOnDuplicate(err, "login attempt id already exists")
}

// CountFailuresSince 统计 since（含）之后指定账号摘要的失败次数。
func (r *SQLLoginAttemptRepository) CountFailuresSince(ctx context.Context, channel domain.Channel, accountRef string, since time.Time) (int, error) {
	var count int
	err := r.exec(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM identity_login_attempt
		WHERE channel = ? AND account_ref = ? AND outcome = ? AND occurred_at >= ?`,
		string(channel), accountRef, string(domain.LoginOutcomeFailure), since.UTC()).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// LastSuccessAt 返回最近一次成功登录时间；没有记录时 found 为 false。
func (r *SQLLoginAttemptRepository) LastSuccessAt(ctx context.Context, channel domain.Channel, accountRef string) (time.Time, bool, error) {
	var occurredAt sql.NullTime
	err := r.exec(ctx).QueryRowContext(ctx, `SELECT MAX(occurred_at) FROM identity_login_attempt
		WHERE channel = ? AND account_ref = ? AND outcome = ?`,
		string(channel), accountRef, string(domain.LoginOutcomeSuccess)).Scan(&occurredAt)
	if err != nil {
		return time.Time{}, false, err
	}
	if !occurredAt.Valid {
		return time.Time{}, false, nil
	}
	return occurredAt.Time.UTC(), true, nil
}
