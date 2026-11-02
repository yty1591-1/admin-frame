// Package persistence 提供审计查询与保留清理的 SQL 实现。
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// DefaultTable 是审计事件表名；与 migrations/000006 重命名后的表一致。
const DefaultTable = "audit_event"

// SQLReader 基于 MySQL 实现审计只读仓储。
//
// 只用参数化查询拼接过滤条件；字段名来自本文件常量，不接受外部输入，
// 因此表名与列名可安全拼接到 SQL（值一律走 ? 占位符）。
type SQLReader struct {
	// DB 审计库连接；nil 时所有方法返回错误。
	DB *sql.DB
	// Table 目标表名；空值使用 DefaultTable。
	Table string
}

// NewSQLReader 构造审计查询仓储。
func NewSQLReader(db *sql.DB, table string) (*SQLReader, error) {
	if db == nil {
		return nil, fmt.Errorf("audit reader requires a database")
	}
	if strings.TrimSpace(table) == "" {
		table = DefaultTable
	}
	return &SQLReader{DB: db, Table: table}, nil
}

// selectColumns 是审计事件查询列，顺序与 scanEvent 一一对应。
const selectColumns = `event_id, action, actor_id, actor_type, channel, merchant_id,
	resource_type, resource_id, outcome, occurred_at, trace_id, service, reason, attrs`

// List 按条件分页返回事件，按 occurred_at 倒序。
func (r *SQLReader) List(ctx context.Context, filter domain.Filter) ([]audit.Event, error) {
	if r == nil || r.DB == nil {
		return nil, fmt.Errorf("audit reader is not configured")
	}
	where, args := r.buildWhere(filter)
	//nolint:gosec // G201: 表名与列名均为本文件常量，过滤值全部走 ? 占位符。
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY occurred_at DESC, event_id DESC LIMIT ? OFFSET ?`,
		selectColumns, r.table(), where)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]audit.Event, 0, filter.Limit)
	for rows.Next() {
		event, scanErr := scanEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

// Count 返回满足条件的总条数。
func (r *SQLReader) Count(ctx context.Context, filter domain.Filter) (int, error) {
	if r == nil || r.DB == nil {
		return 0, fmt.Errorf("audit reader is not configured")
	}
	where, args := r.buildWhere(filter)
	//nolint:gosec // G201: 表名与列名均为本文件常量，过滤值全部走 ? 占位符。
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s`, r.table(), where)
	var total int
	if err := r.DB.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count audit events: %w", err)
	}
	return total, nil
}

// Stream 按条件逐条回调，供导出使用。
//
// 上限由 filter.Limit 控制；导出方在调用前已用 audit.export.max_rows 收敛。
func (r *SQLReader) Stream(ctx context.Context, filter domain.Filter, emit func(audit.Event) error) error {
	if r == nil || r.DB == nil {
		return fmt.Errorf("audit reader is not configured")
	}
	where, args := r.buildWhere(filter)
	//nolint:gosec // G201: 表名与列名均为本文件常量，过滤值全部走 ? 占位符。
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY occurred_at ASC, event_id ASC LIMIT ?`,
		selectColumns, r.table(), where)
	args = append(args, filter.Limit)

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("stream audit events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		event, scanErr := scanEvent(rows)
		if scanErr != nil {
			return scanErr
		}
		if err := emit(event); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate audit events: %w", err)
	}
	return nil
}

// buildWhere 把过滤条件转换为参数化 WHERE 子句。
//
// 时间窗始终是第一个条件且必填，保证任意查询都能命中 occurred_at 索引前缀。
func (r *SQLReader) buildWhere(filter domain.Filter) (where string, args []any) {
	conditions := []string{"occurred_at >= ?", "occurred_at < ?"}
	args = []any{filter.From.UTC(), filter.To.UTC()}

	equals := []struct {
		column string
		value  string
	}{
		{"actor_id", filter.ActorID},
		{"action", filter.Action},
		{"channel", filter.Channel},
		{"outcome", filter.Outcome},
		{"merchant_id", filter.MerchantID},
		{"resource_type", filter.ResourceType},
		{"resource_id", filter.ResourceID},
		{"trace_id", filter.TraceID},
	}
	for _, item := range equals {
		if value := strings.TrimSpace(item.value); value != "" {
			conditions = append(conditions, item.column+" = ?")
			args = append(args, value)
		}
	}
	return strings.Join(conditions, " AND "), args
}

// table 返回实际查询表名。
func (r *SQLReader) table() string {
	if r == nil || strings.TrimSpace(r.Table) == "" {
		return DefaultTable
	}
	return r.Table
}

// rowScanner 是 *sql.Rows 与 *sql.Row 的公共读取契约。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanEvent 把一行审计记录转换为审计事件。
func scanEvent(row rowScanner) (audit.Event, error) {
	var (
		event      audit.Event
		actorType  sql.NullString
		reason     sql.NullString
		attrs      []byte
		outcomeRaw string
	)
	if err := row.Scan(
		&event.EventID, &event.Action, &event.ActorID, &actorType, &event.Channel, &event.MerchantID,
		&event.ResourceType, &event.ResourceID, &outcomeRaw, &event.OccurredAt,
		&event.TraceID, &event.Service, &reason, &attrs,
	); err != nil {
		return audit.Event{}, fmt.Errorf("scan audit event: %w", err)
	}
	event.ActorType = actorType.String
	event.Reason = reason.String
	event.Outcome = audit.Outcome(outcomeRaw)
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &event.Attrs); err != nil {
			return audit.Event{}, fmt.Errorf("decode audit attrs: %w", err)
		}
	}
	return event, nil
}

// SQLRetentionStore 基于 MySQL 实现保留期清理仓储。
//
// 删除是受控动作：只按 occurred_at 分批删除，不允许按 actor / action 定向删除，
// 避免被误用作「擦除某次操作痕迹」的能力（AUDIT_STANDARDS §5）。
type SQLRetentionStore struct {
	// DB 审计库连接；nil 时所有方法返回错误。
	DB *sql.DB
	// Table 目标表名；空值使用 DefaultTable。
	Table string
}

// NewSQLRetentionStore 构造保留期清理仓储。
func NewSQLRetentionStore(db *sql.DB, table string) (*SQLRetentionStore, error) {
	if db == nil {
		return nil, fmt.Errorf("audit retention store requires a database")
	}
	if strings.TrimSpace(table) == "" {
		table = DefaultTable
	}
	return &SQLRetentionStore{DB: db, Table: table}, nil
}

// CountBefore 统计 occurred_at 早于 cutoff 的行数。
func (s *SQLRetentionStore) CountBefore(ctx context.Context, cutoff time.Time) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("audit retention store is not configured")
	}
	//nolint:gosec // G201: 表名为本文件常量，过滤值走 ? 占位符。
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE occurred_at < ?`, s.table())
	var total int
	if err := s.DB.QueryRowContext(ctx, query, cutoff.UTC()).Scan(&total); err != nil {
		return 0, fmt.Errorf("count expired audit events: %w", err)
	}
	return total, nil
}

// DeleteBefore 删除 occurred_at 早于 cutoff 的行，最多 limit 行。
func (s *SQLRetentionStore) DeleteBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("audit retention store is not configured")
	}
	if limit <= 0 {
		return 0, fmt.Errorf("delete limit must be positive")
	}
	//nolint:gosec // G201: 表名为本文件常量，过滤值走 ? 占位符。
	query := fmt.Sprintf(`DELETE FROM %s WHERE occurred_at < ? ORDER BY occurred_at LIMIT ?`, s.table())
	result, err := s.DB.ExecContext(ctx, query, cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired audit events: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted audit rows: %w", err)
	}
	return int(affected), nil
}

// table 返回实际操作表名。
func (s *SQLRetentionStore) table() string {
	if s == nil || strings.TrimSpace(s.Table) == "" {
		return DefaultTable
	}
	return s.Table
}
