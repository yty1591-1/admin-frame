package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
)

// DefaultExportTable 是导出任务表名；与 migrations/000007 一致。
const DefaultExportTable = "audit_export_job"

// exportColumns 与 scanExportJob 一一对应。
const exportColumns = `export_id, actor_id, actor_type, channel, merchant_id, trace_id,
	filter_json, status, attempts, row_count, object_key, last_error,
	created_at, started_at, finished_at, expires_at`

// SQLExportStore 基于 MySQL 实现导出任务仓储。
//
// 多副本安全依赖 InnoDB 行锁：抢占用 SELECT ... FOR UPDATE SKIP LOCKED，
// 避免两个 worker 同时拿到同一条任务。
type SQLExportStore struct {
	// DB 审计库连接；nil 时所有方法返回错误。
	DB *sql.DB
	// Table 目标表名；空值使用 DefaultExportTable。
	Table string
}

// NewSQLExportStore 构造导出任务仓储。
func NewSQLExportStore(db *sql.DB, table string) (*SQLExportStore, error) {
	if db == nil {
		return nil, fmt.Errorf("audit export store requires a database")
	}
	if strings.TrimSpace(table) == "" {
		table = DefaultExportTable
	}
	return &SQLExportStore{DB: db, Table: table}, nil
}

func (s *SQLExportStore) table() string {
	if s == nil || strings.TrimSpace(s.Table) == "" {
		return DefaultExportTable
	}
	return s.Table
}

// Create 写入新任务。
func (s *SQLExportStore) Create(ctx context.Context, job domain.ExportJob) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("audit export store is not configured")
	}
	if job.ID == "" || job.ActorID == "" || job.Channel == "" {
		return fmt.Errorf("audit export job requires id, actor and channel")
	}
	payload, err := json.Marshal(job.Filter)
	if err != nil {
		return fmt.Errorf("encode export filter: %w", err)
	}
	//nolint:gosec // G201: 表名来自本文件常量，值全部走 ? 占位符。
	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, s.table(), exportColumns)
	_, err = s.DB.ExecContext(ctx, query,
		job.ID, job.ActorID, job.ActorType, job.Channel, job.MerchantID, job.TraceID,
		payload, job.Status, job.Attempts, job.RowCount, job.ObjectKey, job.LastError,
		job.CreatedAt.UTC(), nullableTime(job.StartedAt), nullableTime(job.FinishedAt), job.ExpiresAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create export job: %w", err)
	}
	return nil
}

// Get 按 ID 读取任务。
func (s *SQLExportStore) Get(ctx context.Context, id string) (domain.ExportJob, error) {
	if s == nil || s.DB == nil {
		return domain.ExportJob{}, fmt.Errorf("audit export store is not configured")
	}
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE export_id = ?`, exportColumns, s.table())
	job, err := scanExportJob(s.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExportJob{}, domain.ErrExportNotFound
	}
	if err != nil {
		return domain.ExportJob{}, fmt.Errorf("get export job: %w", err)
	}
	return job, nil
}

// ClaimPending 抢占最早的一条 pending 任务。
//
// 事务内 SKIP LOCKED：已加锁的行直接跳过，不排队等待，多副本并行抢占时
// 各自拿到不同任务。
func (s *SQLExportStore) ClaimPending(ctx context.Context, now time.Time) (domain.ExportJob, bool, error) {
	if s == nil || s.DB == nil {
		return domain.ExportJob{}, false, fmt.Errorf("audit export store is not configured")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.ExportJob{}, false, fmt.Errorf("begin claim export job: %w", err)
	}
	defer func() {
		// Commit 成功后 Rollback 返回 ErrTxDone，忽略即可。
		_ = tx.Rollback()
	}()

	selectSQL := fmt.Sprintf(
		`SELECT %s FROM %s WHERE status = ? ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED`,
		exportColumns, s.table())
	job, err := scanExportJob(tx.QueryRowContext(ctx, selectSQL, string(domain.ExportPending)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExportJob{}, false, nil
	}
	if err != nil {
		return domain.ExportJob{}, false, fmt.Errorf("select pending export job: %w", err)
	}

	//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
	updateSQL := fmt.Sprintf(
		`UPDATE %s SET status = ?, started_at = ?, attempts = attempts + 1 WHERE export_id = ?`,
		s.table())
	if _, err := tx.ExecContext(ctx, updateSQL, string(domain.ExportRunning), now.UTC(), job.ID); err != nil {
		return domain.ExportJob{}, false, fmt.Errorf("mark export job running: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ExportJob{}, false, fmt.Errorf("commit claim export job: %w", err)
	}
	job.Status = domain.ExportRunning
	job.StartedAt = now.UTC()
	job.Attempts++
	return job, true, nil
}

// RequeueStale 回收长时间停在 running 的任务。
//
// worker 崩溃会留下 running 行且无人推进；超过 stale_timeout 未更新即视为失效。
// 其中已用尽重试次数的任务直接置为 failed——重排回 pending 会让它永远占据
// 「最早一条」的位置（ClaimPending 按 created_at 取），把后续任务全部饿死。
func (s *SQLExportStore) RequeueStale(ctx context.Context, cutoff time.Time, maxAttempts int) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("audit export store is not configured")
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	staleCutoff := cutoff.UTC()

	// 先用尽重试的置为终态失败，再重排其余；顺序反了会把它们一起捞回 pending。
	//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
	failSQL := fmt.Sprintf(
		`UPDATE %s SET status = ?, started_at = NULL, last_error = ?, finished_at = ? WHERE status = ? AND started_at < ? AND attempts >= ?`,
		s.table())
	if _, err := s.DB.ExecContext(ctx, failSQL,
		string(domain.ExportFailed), "failed: worker lost after max attempts",
		time.Now().UTC(), string(domain.ExportRunning), staleCutoff, maxAttempts); err != nil {
		return 0, fmt.Errorf("fail exhausted stale export jobs: %w", err)
	}

	//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
	requeueSQL := fmt.Sprintf(
		`UPDATE %s SET status = ?, started_at = NULL, last_error = ? WHERE status = ? AND started_at < ? AND attempts < ?`,
		s.table())
	result, err := s.DB.ExecContext(ctx, requeueSQL,
		string(domain.ExportPending), "requeued: stale running job",
		string(domain.ExportRunning), staleCutoff, maxAttempts)
	if err != nil {
		return 0, fmt.Errorf("requeue stale export jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("requeue stale export jobs: %w", err)
	}
	return int(affected), nil
}

// MarkSucceeded 置为成功。
func (s *SQLExportStore) MarkSucceeded(ctx context.Context, id string, rows int, objectKey string, finishedAt time.Time) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("audit export store is not configured")
	}
	//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
	query := fmt.Sprintf(
		`UPDATE %s SET status = ?, row_count = ?, object_key = ?, last_error = '', finished_at = ? WHERE export_id = ?`,
		s.table())
	if _, err := s.DB.ExecContext(ctx, query,
		string(domain.ExportSucceeded), rows, objectKey, finishedAt.UTC(), id); err != nil {
		return fmt.Errorf("mark export job succeeded: %w", err)
	}
	return nil
}

// MarkFailed 记录一次失败：canRetry 为 true 时回到 pending。
func (s *SQLExportStore) MarkFailed(ctx context.Context, id, reason string, finishedAt time.Time, canRetry bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("audit export store is not configured")
	}
	reason = truncate(reason, 512)
	if canRetry {
		//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
		query := fmt.Sprintf(
			`UPDATE %s SET status = ?, started_at = NULL, last_error = ? WHERE export_id = ?`, s.table())
		if _, err := s.DB.ExecContext(ctx, query, string(domain.ExportPending), reason, id); err != nil {
			return fmt.Errorf("mark export job for retry: %w", err)
		}
		return nil
	}
	//nolint:gosec // G201: 表名均为本文件常量，值走 ? 占位符。
	query := fmt.Sprintf(
		`UPDATE %s SET status = ?, last_error = ?, finished_at = ? WHERE export_id = ?`, s.table())
	if _, err := s.DB.ExecContext(ctx, query, string(domain.ExportFailed), reason, finishedAt.UTC(), id); err != nil {
		return fmt.Errorf("mark export job failed: %w", err)
	}
	return nil
}

// ListExpired 返回已到保留期且未标记过期的任务。
func (s *SQLExportStore) ListExpired(ctx context.Context, now time.Time, limit int) ([]domain.ExportJob, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("audit export store is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	//nolint:gosec // G201: 表名与列名均为本文件常量，值走 ? 占位符。
	query := fmt.Sprintf(
		`SELECT %s FROM %s WHERE expires_at <= ? AND status <> ? ORDER BY expires_at ASC LIMIT ?`,
		exportColumns, s.table())
	rows, err := s.DB.QueryContext(ctx, query, now.UTC(), string(domain.ExportExpired), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired export jobs: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	jobs := make([]domain.ExportJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanExportJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired export jobs: %w", err)
	}
	return jobs, nil
}

// MarkExpired 批量置为 expired。
func (s *SQLExportStore) MarkExpired(ctx context.Context, ids []string, finishedAt time.Time) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("audit export store is not configured")
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+2)
	args = append(args, string(domain.ExportExpired), finishedAt.UTC())
	for _, id := range ids {
		args = append(args, id)
	}
	//nolint:gosec // G201: 表名均为本文件常量；IN 占位符按切片长度生成，值走参数。
	query := fmt.Sprintf(`UPDATE %s SET status = ?, finished_at = ? WHERE export_id IN (%s)`, s.table(), placeholders)
	if _, err := s.DB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("mark export jobs expired: %w", err)
	}
	return nil
}

// scanExportJob 读取一行任务；filter_json 反序列化为快照。
func scanExportJob(row rowScanner) (domain.ExportJob, error) {
	var (
		job        domain.ExportJob
		payload    []byte
		status     string
		startedAt  sql.NullTime
		finishedAt sql.NullTime
	)
	if err := row.Scan(
		&job.ID, &job.ActorID, &job.ActorType, &job.Channel, &job.MerchantID, &job.TraceID,
		&payload, &status, &job.Attempts, &job.RowCount, &job.ObjectKey, &job.LastError,
		&job.CreatedAt, &startedAt, &finishedAt, &job.ExpiresAt,
	); err != nil {
		return domain.ExportJob{}, err
	}
	if err := json.Unmarshal(payload, &job.Filter); err != nil {
		return domain.ExportJob{}, fmt.Errorf("decode export filter: %w", err)
	}
	job.Status = domain.ExportStatus(status)
	if !job.Status.Valid() {
		return domain.ExportJob{}, fmt.Errorf("unknown export status %q", status)
	}
	job.CreatedAt = job.CreatedAt.UTC()
	job.ExpiresAt = job.ExpiresAt.UTC()
	if startedAt.Valid {
		job.StartedAt = startedAt.Time.UTC()
	}
	if finishedAt.Valid {
		job.FinishedAt = finishedAt.Time.UTC()
	}
	return job, nil
}

// nullableTime 把零值时间写成 NULL，避免 0000-00-00 触发严格模式报错。
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

// truncate 按字节截断字符串，保证不超过列宽。
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
