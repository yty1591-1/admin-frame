package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/mq"
)

// outboxSelectColumns 是 async_outbox 的显式列清单（禁止 SELECT *）。
const outboxSelectColumns = `id, topic, event_type, version, tenant_id, aggregate_type,
	aggregate_id, idempotency_key, trace_id, payload, status, attempts,
	next_retry_at, last_error, occurred_at, created_at, updated_at`

// MySQLStore 是 MySQL 8.0 的事务性 Outbox 仓储。
//
// 认领使用条件更新（pending → processing），多副本同时拉取时只有一个 Relay 投递；
// 死信写入与状态迁移在同一事务内完成，避免「标记 dead 但 DLQ 缺失」。
type MySQLStore struct {
	sql *sql.DB
}

// NewMySQLStore 基于已打开的 *sql.DB 创建 Store。
func NewMySQLStore(sqlDB *sql.DB) *MySQLStore {
	return &MySQLStore{sql: sqlDB}
}

// Append 实现 Store。
func (s *MySQLStore) Append(ctx context.Context, event *Event) error {
	if event == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if s.sql == nil {
		return fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	_, err := s.exec(ctx).ExecContext(ctx, `INSERT INTO async_outbox
		(id, topic, event_type, version, tenant_id, aggregate_type, aggregate_id,
		 idempotency_key, trace_id, payload, status, attempts, next_retry_at,
		 last_error, occurred_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.Topic, event.EventType, event.Version, event.TenantID,
		event.AggregateType, event.AggregateID, event.IdempotencyKey, event.TraceID,
		event.Payload, string(event.Status), event.Attempts, nullTime(event.NextRetryAt),
		event.LastError, event.OccurredAt.UTC(), event.CreatedAt.UTC(), event.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("append outbox event %s: %w", event.EventID, err)
	}
	return nil
}

// exec 解析写入执行器：业务事务（pkg/db.WithTx）内复用 *sql.Tx，
// 保证「业务写入 + Outbox 事件」一次提交 / 回滚（REL-04 事务性 Outbox）。
// 认领、终态迁移与死信写入不走该解析，避免嵌套事务。
func (s *MySQLStore) exec(ctx context.Context) db.Querier {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return s.sql
}

// ClaimBatch 实现 Store。
func (s *MySQLStore) ClaimBatch(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*Event, error) {
	if s.sql == nil {
		return nil, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	_ = lease
	rows, err := s.sql.QueryContext(ctx, `SELECT `+outboxSelectColumns+` FROM async_outbox
		WHERE status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)
		ORDER BY created_at, id LIMIT ?`,
		string(StatusPending), now.UTC(), normalizeBatchSize(limit))
	if err != nil {
		return nil, fmt.Errorf("list due outbox events: %w", err)
	}
	candidates := make([]*Event, 0, normalizeBatchSize(limit))
	for rows.Next() {
		event, scanErr := scanOutboxEvent(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		candidates = append(candidates, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	claimed := make([]*Event, 0, len(candidates))
	for _, event := range candidates {
		result, err := s.sql.ExecContext(ctx, `UPDATE async_outbox
			SET status = ?, updated_at = ?
			WHERE id = ? AND status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)`,
			string(StatusProcessing), now.UTC(), event.EventID, string(StatusPending), now.UTC())
		if err != nil {
			return nil, fmt.Errorf("claim outbox event %s: %w", event.EventID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			continue
		}
		event.Status = StatusProcessing
		event.UpdatedAt = now.UTC()
		claimed = append(claimed, event)
	}
	return claimed, nil
}

// MarkSucceeded 实现 Store。
func (s *MySQLStore) MarkSucceeded(ctx context.Context, eventID string, now time.Time) error {
	return s.transition(ctx, eventID,
		`UPDATE async_outbox SET status = ?, next_retry_at = NULL, last_error = '', updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(StatusSucceeded), now.UTC())
}

// Reschedule 实现 Store。
func (s *MySQLStore) Reschedule(ctx context.Context, eventID string, attempts int, nextRetryAt, now time.Time, lastError string) error {
	return s.transition(ctx, eventID,
		`UPDATE async_outbox SET status = ?, attempts = ?, next_retry_at = ?, last_error = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(StatusPending), attempts, nextRetryAt.UTC(), truncateCause(lastError), now.UTC())
}

// MarkDead 实现 Store。
func (s *MySQLStore) MarkDead(ctx context.Context, eventID string, now time.Time, lastError string) error {
	return s.terminate(ctx, eventID, StatusDead, now, lastError)
}

// MarkFailed 实现 Store。
func (s *MySQLStore) MarkFailed(ctx context.Context, eventID string, now time.Time, lastError string) error {
	return s.terminate(ctx, eventID, StatusFailed, now, lastError)
}

// ReclaimStale 实现 Store。
func (s *MySQLStore) ReclaimStale(ctx context.Context, staleBefore, now time.Time) (int, error) {
	if s.sql == nil {
		return 0, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	result, err := s.sql.ExecContext(ctx, `UPDATE async_outbox
		SET status = ?, next_retry_at = NULL, updated_at = ?
		WHERE status = ? AND updated_at < ?`,
		string(StatusPending), now.UTC(), string(StatusProcessing), staleBefore.UTC())
	if err != nil {
		return 0, fmt.Errorf("reclaim stale outbox events: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// Stats 实现 Store。
func (s *MySQLStore) Stats(ctx context.Context) (Stats, error) {
	if s.sql == nil {
		return Stats{}, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	var (
		pending int
		oldest  sql.NullTime
	)
	err := s.sql.QueryRowContext(ctx, `SELECT COUNT(*), MIN(created_at) FROM async_outbox
		WHERE status IN (?, ?, ?, ?)`,
		string(StatusPending), string(StatusProcessing),
		string(StatusDead), string(StatusFailed)).Scan(&pending, &oldest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Stats{}, fmt.Errorf("outbox stats: %w", err)
	}
	var dlq int
	if err := s.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM async_dlq WHERE replayed_at IS NULL`).Scan(&dlq); err != nil {
		return Stats{}, fmt.Errorf("outbox dlq stats: %w", err)
	}
	stats := Stats{Pending: pending, DLQ: dlq}
	if oldest.Valid {
		stats.OldestCreatedAt = oldest.Time.UTC()
	}
	return stats, nil
}

// TopicStats 实现 TopicStatReader。
func (s *MySQLStore) TopicStats(ctx context.Context, topic string) (Stats, error) {
	if s.sql == nil {
		return Stats{}, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	var (
		pending int
		oldest  sql.NullTime
	)
	err := s.sql.QueryRowContext(ctx, `SELECT COUNT(*), MIN(created_at) FROM async_outbox
		WHERE topic = ? AND status IN (?, ?, ?, ?)`,
		topic, string(StatusPending), string(StatusProcessing),
		string(StatusDead), string(StatusFailed)).Scan(&pending, &oldest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Stats{}, fmt.Errorf("outbox topic stats: %w", err)
	}
	var dlq int
	if err := s.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM async_dlq
		WHERE topic = ? AND replayed_at IS NULL`, topic).Scan(&dlq); err != nil {
		return Stats{}, fmt.Errorf("outbox dlq topic stats: %w", err)
	}
	stats := Stats{Pending: pending, DLQ: dlq}
	if oldest.Valid {
		stats.OldestCreatedAt = oldest.Time.UTC()
	}
	return stats, nil
}

// terminate 在单事务内迁移终态并写入死信。
func (s *MySQLStore) terminate(ctx context.Context, eventID string, status Status, now time.Time, lastError string) error {
	if s.sql == nil {
		return fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	tx, err := s.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outbox dead-letter tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `UPDATE async_outbox
		SET status = ?, next_retry_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(status), truncateCause(lastError), now.UTC(), eventID, string(StatusProcessing))
	if err != nil {
		return fmt.Errorf("mark outbox event %s %s: %w", eventID, status, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotClaimed
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO async_dlq
		(source_id, topic, event_type, payload, attempts, last_error, failed_at)
		SELECT id, topic, event_type, payload, attempts, last_error, ?
		FROM async_outbox WHERE id = ?
		ON DUPLICATE KEY UPDATE attempts = VALUES(attempts),
			last_error = VALUES(last_error), failed_at = VALUES(failed_at),
			replayed_at = NULL`,
		now.UTC(), eventID); err != nil {
		return fmt.Errorf("record outbox dead letter %s: %w", eventID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbox dead-letter tx: %w", err)
	}
	return nil
}

// transition 以 processing 为前提更新状态。
func (s *MySQLStore) transition(ctx context.Context, eventID, query string, args ...any) error {
	if s.sql == nil {
		return fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	args = append(args, eventID, string(StatusProcessing))
	result, err := s.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("transition outbox event %s: %w", eventID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotClaimed
	}
	return nil
}

func scanOutboxEvent(scanner interface{ Scan(...any) error }) (*Event, error) {
	var (
		eventID        string
		topic          string
		eventType      string
		version        string
		tenantID       string
		aggregateType  string
		aggregateID    string
		idempotencyKey string
		traceID        string
		payload        []byte
		status         string
		attempts       int
		nextRetryAt    sql.NullTime
		lastError      string
		occurredAt     time.Time
		createdAt      time.Time
		updatedAt      time.Time
	)
	if err := scanner.Scan(&eventID, &topic, &eventType, &version, &tenantID,
		&aggregateType, &aggregateID, &idempotencyKey, &traceID, &payload,
		&status, &attempts, &nextRetryAt, &lastError, &occurredAt,
		&createdAt, &updatedAt); err != nil {
		return nil, err
	}
	event := &Event{
		Envelope: mqEnvelope(eventID, eventType, version, tenantID, traceID,
			idempotencyKey, occurredAt, payload),
		Topic:         topic,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		Status:        Status(status),
		Attempts:      attempts,
		LastError:     lastError,
		CreatedAt:     createdAt.UTC(),
		UpdatedAt:     updatedAt.UTC(),
	}
	if nextRetryAt.Valid {
		next := nextRetryAt.Time.UTC()
		event.NextRetryAt = &next
	}
	return event, nil
}

func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func mqEnvelope(eventID, eventType, version, tenantID, traceID, idempotencyKey string,
	occurredAt time.Time, payload []byte) mq.Envelope {
	return mq.Envelope{
		EventID:        eventID,
		EventType:      eventType,
		Version:        version,
		TenantID:       tenantID,
		TraceID:        traceID,
		IdempotencyKey: idempotencyKey,
		OccurredAt:     occurredAt.UTC(),
		Payload:        payload,
	}
}

var _ Store = (*MySQLStore)(nil)

var _ TopicStatReader = (*MySQLStore)(nil)

// ReplayDeadLetter 实现 DeadLetterReplayer。
//
// 单事务内把事件从 dead/failed 重置为 pending（清零 attempts 与退避）并标记 DLQ。
func (s *MySQLStore) ReplayDeadLetter(ctx context.Context, sourceID string, now time.Time) (bool, error) {
	if s.sql == nil {
		return false, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	tx, err := s.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin outbox replay tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `UPDATE async_outbox
		SET status = ?, attempts = 0, next_retry_at = NULL, last_error = '', updated_at = ?
		WHERE id = ? AND status IN (?, ?)`,
		string(StatusPending), now.UTC(), sourceID, string(StatusDead), string(StatusFailed))
	if err != nil {
		return false, fmt.Errorf("replay outbox event %s: %w", sourceID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE async_dlq SET replayed_at = ?
		WHERE source_id = ? AND replayed_at IS NULL`, now.UTC(), sourceID); err != nil {
		return false, fmt.Errorf("mark outbox dead letter %s replayed: %w", sourceID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit outbox replay tx: %w", err)
	}
	return true, nil
}

// ReplayAllDeadLetters 实现 DeadLetterReplayer。
func (s *MySQLStore) ReplayAllDeadLetters(ctx context.Context, now time.Time) (int, error) {
	if s.sql == nil {
		return 0, fmt.Errorf("%w: mysql store is not configured", ErrInvalidEvent)
	}
	rows, err := s.sql.QueryContext(ctx, `SELECT source_id FROM async_dlq
		WHERE replayed_at IS NULL ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("list outbox dead letters: %w", err)
	}
	ids := make([]string, 0, 64)
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, sourceID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	replayed := 0
	for _, sourceID := range ids {
		ok, err := s.ReplayDeadLetter(ctx, sourceID, now)
		if err != nil {
			return replayed, err
		}
		if ok {
			replayed++
		}
	}
	return replayed, nil
}

var _ DeadLetterReplayer = (*MySQLStore)(nil)

// MySQLDeduper 基于 async_consumed 实现消费幂等，支持多副本接管过期 processing 记录。
type MySQLDeduper struct {
	sql *sql.DB
	now func() time.Time
}

// NewMySQLDeduper 基于已打开的 *sql.DB 创建 Deduper。
func NewMySQLDeduper(sqlDB *sql.DB) *MySQLDeduper {
	return &MySQLDeduper{sql: sqlDB, now: time.Now}
}

// Acquire 实现 Deduper。
//
// 使用 SELECT ... FOR UPDATE 串行化同一 (event_id, consumer) 的并发抢占，
// processing 记录超过 visibility 后可被接管（至少一次投递下的崩溃恢复）。
func (d *MySQLDeduper) Acquire(ctx context.Context, eventID, consumer string, now time.Time, visibility time.Duration) (DedupeOutcome, error) {
	if eventID == "" {
		return DedupeAcquired, fmt.Errorf("%w: event id is required", ErrInvalidEvent)
	}
	if d.sql == nil {
		return DedupeAcquired, fmt.Errorf("%w: mysql deduper is not configured", ErrInvalidEvent)
	}
	now = now.UTC()
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return DedupeAcquired, fmt.Errorf("begin consumer lease tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status    string
		updatedAt time.Time
	)
	err = tx.QueryRowContext(ctx, `SELECT status, updated_at FROM async_consumed
		WHERE event_id = ? AND consumer = ? FOR UPDATE`, eventID, consumer).Scan(&status, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO async_consumed
			(event_id, consumer, status, updated_at) VALUES (?, ?, ?, ?)`,
			eventID, consumer, dedupeStatusProcessing, now); err != nil {
			return DedupeAcquired, fmt.Errorf("insert consumer record %s/%s: %w", eventID, consumer, err)
		}
		if err := tx.Commit(); err != nil {
			return DedupeAcquired, fmt.Errorf("commit consumer lease tx: %w", err)
		}
		return DedupeAcquired, nil
	case err != nil:
		return DedupeAcquired, fmt.Errorf("load consumer record %s/%s: %w", eventID, consumer, err)
	}

	if status == dedupeStatusDone {
		return DedupeDone, nil
	}
	if updatedAt.UTC().Add(normalizeVisibility(visibility)).After(now) {
		return DedupeBusy, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE async_consumed SET status = ?, updated_at = ?
		WHERE event_id = ? AND consumer = ?`,
		dedupeStatusProcessing, now, eventID, consumer); err != nil {
		return DedupeAcquired, fmt.Errorf("take over consumer record %s/%s: %w", eventID, consumer, err)
	}
	if err := tx.Commit(); err != nil {
		return DedupeAcquired, fmt.Errorf("commit consumer lease tx: %w", err)
	}
	return DedupeAcquired, nil
}

// Complete 实现 Deduper；记录已是 done 时视为幂等成功。
func (d *MySQLDeduper) Complete(ctx context.Context, eventID, consumer string) error {
	if d.sql == nil {
		return fmt.Errorf("%w: mysql deduper is not configured", ErrInvalidEvent)
	}
	result, err := d.sql.ExecContext(ctx, `UPDATE async_consumed SET status = ?, updated_at = ?
		WHERE event_id = ? AND consumer = ? AND status = ?`,
		dedupeStatusDone, d.now().UTC(), eventID, consumer, dedupeStatusProcessing)
	if err != nil {
		return fmt.Errorf("complete consumer record %s/%s: %w", eventID, consumer, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var status string
	err = d.sql.QueryRowContext(ctx, `SELECT status FROM async_consumed
		WHERE event_id = ? AND consumer = ?`, eventID, consumer).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: consumer record %s/%s", ErrNotClaimed, eventID, consumer)
	}
	if err != nil {
		return fmt.Errorf("load consumer record %s/%s: %w", eventID, consumer, err)
	}
	if status == dedupeStatusDone {
		return nil
	}
	return fmt.Errorf("%w: consumer record %s/%s is %s", ErrNotClaimed, eventID, consumer, status)
}

// Release 实现 Deduper。
func (d *MySQLDeduper) Release(ctx context.Context, eventID, consumer string) error {
	if d.sql == nil {
		return fmt.Errorf("%w: mysql deduper is not configured", ErrInvalidEvent)
	}
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM async_consumed
		WHERE event_id = ? AND consumer = ? AND status = ?`,
		eventID, consumer, dedupeStatusProcessing); err != nil {
		return fmt.Errorf("release consumer record %s/%s: %w", eventID, consumer, err)
	}
	return nil
}

var _ Deduper = (*MySQLDeduper)(nil)
