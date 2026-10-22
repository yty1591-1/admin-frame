package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// outboxSelectColumns 是 outbox 表的显式列清单（禁止 SELECT *，DATA-04）。
const outboxSelectColumns = `id, event_type, aggregate_type, aggregate_id, payload,
	status, attempts, next_retry_at, created_at, updated_at`

// SQLOutboxRepository 是 MySQL 8.0 的事务性 Outbox 仓储。
//
// 认领采用「条件更新」而不是 SELECT ... FOR UPDATE：多实例同时拉取时，
// 只有把 status 从 pending 改成 processing 成功的那一个会投递，避免重复发送。
type SQLOutboxRepository struct {
	sql *sql.DB
}

// NewSQLOutboxRepository 基于已打开的 *sql.DB 创建 outbox 仓储。
func NewSQLOutboxRepository(sqlDB *sql.DB) *SQLOutboxRepository {
	return &SQLOutboxRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
//
// outbox 与业务写入同事务是 D9 的核心：append 落在业务事务里，
// 业务回滚时审计事件一并回滚，避免「审计记录了未发生的变更」。
func (r *SQLOutboxRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// Append 写入一条 pending 事件。
func (r *SQLOutboxRepository) Append(ctx context.Context, event *domain.OutboxEvent) error {
	if event == nil {
		return apperr.BadRequest("outbox event is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam outbox repository is not configured")
	}
	eventID, err := parseID(event.ID)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO iam_outbox
		(id, event_type, aggregate_type, aggregate_id, payload, status,
			attempts, next_retry_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, string(event.Type), event.AggregateType, event.AggregateID, event.Payload,
		string(event.Status), event.Attempts, nullTime(event.NextRetryAt),
		event.CreatedAt.UTC(), event.UpdatedAt.UTC())
	return conflictOnDuplicate(err, "outbox event id already exists")
}

// ListDue 拉取到期可投递事件，按主键升序保证 FIFO。
func (r *SQLOutboxRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.OutboxEvent, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam outbox repository is not configured")
	}
	rows, err := r.exec(ctx).QueryContext(ctx, `SELECT `+outboxSelectColumns+` FROM iam_outbox
		WHERE status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)
		ORDER BY id LIMIT ?`,
		string(domain.OutboxPending), now.UTC(), normalizeOutboxLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]*domain.OutboxEvent, 0, normalizeOutboxLimit(limit))
	for rows.Next() {
		event, scanErr := scanOutboxEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// Claim 原子认领事件；条件更新影响 0 行表示已被其它 worker 认领。
func (r *SQLOutboxRepository) Claim(ctx context.Context, id string, now time.Time) (bool, error) {
	if r.sql == nil {
		return false, apperr.Unavailable("iam outbox repository is not configured")
	}
	eventID, err := parseID(id)
	if err != nil {
		return false, err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE iam_outbox
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(domain.OutboxProcessing), now.UTC(), eventID, string(domain.OutboxPending))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// ReclaimStale 把租约过期的 in-flight 事件回收为 pending。
//
// 只按 updated_at 判断：投递成功后标记失败的进程可能已退出，避免事件永久滞留 processing。
func (r *SQLOutboxRepository) ReclaimStale(ctx context.Context, staleBefore, now time.Time) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam outbox repository is not configured")
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE iam_outbox
		SET status = ?, next_retry_at = NULL, updated_at = ?
		WHERE status = ? AND updated_at < ?`,
		string(domain.OutboxPending), now.UTC(),
		string(domain.OutboxProcessing), staleBefore.UTC())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// MarkSucceeded 标记投递成功；非 processing 状态返回冲突（防止重复投递覆盖终态）。
func (r *SQLOutboxRepository) MarkSucceeded(ctx context.Context, id string, now time.Time) error {
	return r.transition(ctx, id,
		`UPDATE iam_outbox SET status = ?, next_retry_at = NULL, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(domain.OutboxSucceeded), now.UTC())
}

// Reschedule 记录失败并放回 pending，等待退避重试。
func (r *SQLOutboxRepository) Reschedule(ctx context.Context, id string, attempts int, nextRetryAt, now time.Time) error {
	return r.transition(ctx, id,
		`UPDATE iam_outbox SET status = ?, attempts = ?, next_retry_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(domain.OutboxPending), attempts, nextRetryAt.UTC(), now.UTC())
}

// MarkDead 标记重试耗尽（死信）。
func (r *SQLOutboxRepository) MarkDead(ctx context.Context, id string, now time.Time) error {
	return r.transition(ctx, id,
		`UPDATE iam_outbox SET status = ?, next_retry_at = NULL, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(domain.OutboxDead), now.UTC())
}

// MarkPoison 标记载荷永久不可投递。
func (r *SQLOutboxRepository) MarkPoison(ctx context.Context, id string, now time.Time) error {
	return r.transition(ctx, id,
		`UPDATE iam_outbox SET status = ?, next_retry_at = NULL, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(domain.OutboxFailed), now.UTC())
}

// Stats 返回积压统计；dead / failed 同样计入，避免死信静默。
func (r *SQLOutboxRepository) Stats(ctx context.Context) (domain.OutboxStats, error) {
	if r.sql == nil {
		return domain.OutboxStats{}, apperr.Unavailable("iam outbox repository is not configured")
	}
	var (
		pending int
		oldest  sql.NullTime
	)
	err := r.exec(ctx).QueryRowContext(ctx, `SELECT COUNT(*), MIN(created_at) FROM iam_outbox
		WHERE status IN (?, ?, ?, ?)`,
		string(domain.OutboxPending), string(domain.OutboxProcessing),
		string(domain.OutboxDead), string(domain.OutboxFailed)).Scan(&pending, &oldest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.OutboxStats{}, err
	}
	stats := domain.OutboxStats{Pending: pending}
	if oldest.Valid {
		stats.OldestCreatedAt = oldest.Time.UTC()
	}
	return stats, nil
}

// transition 以 processing 为前提更新状态；query 必须是完整 SQL 字面量。
func (r *SQLOutboxRepository) transition(ctx context.Context, id, query string, args ...any) error {
	if r.sql == nil {
		return apperr.Unavailable("iam outbox repository is not configured")
	}
	eventID, err := parseID(id)
	if err != nil {
		return err
	}
	args = append(args, eventID, string(domain.OutboxProcessing))
	result, err := r.exec(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.Conflict("outbox event is not processing")
	}
	return nil
}

// scanOutboxEvent 从结果集读取一条事件。
func scanOutboxEvent(scanner interface{ Scan(...any) error }) (*domain.OutboxEvent, error) {
	var (
		eventID       int64
		eventType     string
		aggregateType string
		aggregateID   string
		payload       []byte
		status        string
		attempts      int
		nextRetryAt   sql.NullTime
		createdAt     time.Time
		updatedAt     time.Time
	)
	if err := scanner.Scan(&eventID, &eventType, &aggregateType, &aggregateID, &payload,
		&status, &attempts, &nextRetryAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	event := &domain.OutboxEvent{
		ID:            strconv.FormatInt(eventID, 10),
		Type:          domain.OutboxEventType(eventType),
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		Payload:       payload,
		Status:        domain.OutboxStatus(status),
		Attempts:      attempts,
		CreatedAt:     createdAt.UTC(),
		UpdatedAt:     updatedAt.UTC(),
	}
	if nextRetryAt.Valid {
		next := nextRetryAt.Time.UTC()
		event.NextRetryAt = &next
	}
	return event, nil
}
