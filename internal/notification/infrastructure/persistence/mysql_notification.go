// Package persistence 的 MySQL 实现（ENT-10）：表结构见
// migrations/000016_create_notification.up.sql。
//
// 关键约定：
//   - 状态迁移全部走 CAS（version 条件更新），并发回执与编排进度不互相覆盖；
//   - 幂等键唯一冲突 → ErrDuplicateRequest（消费端幂等吸收）；
//   - ctx 携带事务时自动复用（pkg/db.TxFromContext），通知单与明细同事务落库；
//   - 本包自带 execer / queryer / 判重助手副本：与租户域持久化包互不依赖，
//     避免为几个小助手建立跨域内部 import（分层门禁红线）。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/pkg/db"
)

type notifyExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type notifyQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type notifyDB struct {
	db *sql.DB
}

func (h notifyDB) exec(ctx context.Context) notifyExecer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return h.db
}

func (h notifyDB) query(ctx context.Context) notifyQueryer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return h.db
}

func isNotifyDuplicateKey(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "Error 1062") ||
		strings.Contains(err.Error(), "Duplicate entry"))
}

func notifyNullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}

type notifyRowScanner interface {
	Scan(dest ...any) error
}

// ---------- 通知单 ----------

// MySQLRequestRepository 是通知单仓储的 MySQL 实现。
type MySQLRequestRepository struct {
	handle notifyDB
}

// NewMySQLRequestRepository 构造通知单仓储。
func NewMySQLRequestRepository(sqlDB *sql.DB) (*MySQLRequestRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("notification request repository requires a database handle")
	}
	return &MySQLRequestRepository{handle: notifyDB{db: sqlDB}}, nil
}

// WithinTx 在事务内执行 fn（通知单与渠道明细同事务）。
func (r *MySQLRequestRepository) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("notification: transaction requires a function")
	}
	return db.WithinUnitOfWork(ctx, r.handle.db, nil, fn)
}

const requestColumns = "id, tenant_id, scene, recipient_ref, idempotency_key, channels, " +
	"template_key, locale, params_digest, priority, status, reject_reason, version, " +
	"actor, reference, expires_at, created_at, updated_at"

func joinChannels(channels []domain.Channel) string {
	parts := make([]string, 0, len(channels))
	for _, c := range channels {
		parts = append(parts, string(c))
	}
	return strings.Join(parts, ",")
}

func splitChannels(raw string) []domain.Channel {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]domain.Channel, 0, len(parts))
	for _, p := range parts {
		out = append(out, domain.Channel(strings.TrimSpace(p)))
	}
	return out
}

// Create 落库；幂等键唯一冲突返回 ErrDuplicateRequest。
func (r *MySQLRequestRepository) Create(ctx context.Context, n domain.Notification) error {
	if err := n.Validate(); err != nil {
		return err
	}
	_, err := r.handle.exec(ctx).ExecContext(ctx,
		`INSERT INTO notification_request (`+requestColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.TenantID, n.Scene, n.RecipientRef, n.IdempotencyKey, joinChannels(n.Channels),
		n.TemplateKey, n.Locale, n.ParamsDigest, string(n.Priority), string(n.Status), n.RejectReason,
		n.Version, n.Actor, n.Reference, n.ExpiresAt, n.CreatedAt, n.UpdatedAt)
	if err != nil {
		if isNotifyDuplicateKey(err) {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateRequest, n.IdempotencyKey)
		}
		return fmt.Errorf("notification: create request %s: %w", n.ID, err)
	}
	return nil
}

// Get 按 ID 读取。
func (r *MySQLRequestRepository) Get(ctx context.Context, id string) (domain.Notification, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT `+requestColumns+` FROM notification_request WHERE id = ?`, strings.TrimSpace(id))
	return scanNotification(row)
}

// FindByIdempotencyKey 按幂等键查找。
func (r *MySQLRequestRepository) FindByIdempotencyKey(ctx context.Context, key string) (domain.Notification, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT `+requestColumns+` FROM notification_request WHERE idempotency_key = ?`, strings.TrimSpace(key))
	return scanNotification(row)
}

// UpdateStatus CAS 写入：以 n.Version-1 为预期旧版本。
func (r *MySQLRequestRepository) UpdateStatus(ctx context.Context, n domain.Notification) error {
	res, err := r.handle.exec(ctx).ExecContext(ctx,
		`UPDATE notification_request
		 SET status = ?, reject_reason = ?, version = ?, updated_at = ?
		 WHERE id = ? AND version = ?`,
		string(n.Status), n.RejectReason, n.Version, n.UpdatedAt, n.ID, n.Version-1)
	if err != nil {
		return fmt.Errorf("notification: update request %s: %w", n.ID, err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return domain.ErrVersionConflict
	}
	return nil
}

// List 分页查询（tenant_id 强制过滤）。
func (r *MySQLRequestRepository) List(ctx context.Context, filter domain.RequestFilter) ([]domain.Notification, int, error) {
	filter = filter.Normalize()
	where := []string{"tenant_id = ?"}
	args := []any{filter.TenantID}
	if filter.Scene != "" {
		where = append(where, "scene = ?")
		args = append(args, filter.Scene)
	}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(filter.Status))
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notification_request`+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("notification: count requests: %w", err)
	}
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT `+requestColumns+` FROM notification_request`+clause+
			` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("notification: list requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Notification, 0, filter.Limit)
	for rows.Next() {
		n, scanErr := scanNotification(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, n)
	}
	return items, total, rows.Err()
}

func scanNotification(row notifyRowScanner) (domain.Notification, error) {
	var n domain.Notification
	var channels, priority, status string
	var paramsDigest, rejectReason, actor, reference, locale sql.NullString
	var expiresAt, createdAt, updatedAt time.Time
	if err := row.Scan(&n.ID, &n.TenantID, &n.Scene, &n.RecipientRef, &n.IdempotencyKey, &channels,
		&n.TemplateKey, &locale, &paramsDigest, &priority, &status, &rejectReason, &n.Version,
		&actor, &reference, &expiresAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Notification{}, domain.ErrNotFound
		}
		return domain.Notification{}, fmt.Errorf("notification: scan request: %w", err)
	}
	n.Channels = splitChannels(channels)
	n.Locale = locale.String
	n.ParamsDigest = paramsDigest.String
	n.Priority = domain.Priority(priority)
	n.Status = domain.RequestStatus(status)
	n.RejectReason = rejectReason.String
	n.Actor = actor.String
	n.Reference = reference.String
	n.ExpiresAt = expiresAt
	n.CreatedAt = createdAt
	n.UpdatedAt = updatedAt
	return n, nil
}

// ---------- 渠道投递明细 ----------

// MySQLDeliveryRepository 是投递明细仓储的 MySQL 实现。
type MySQLDeliveryRepository struct {
	handle notifyDB
}

// NewMySQLDeliveryRepository 构造明细仓储。
func NewMySQLDeliveryRepository(sqlDB *sql.DB) (*MySQLDeliveryRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("notification delivery repository requires a database handle")
	}
	return &MySQLDeliveryRepository{handle: notifyDB{db: sqlDB}}, nil
}

const deliveryColumns = "id, request_id, tenant_id, channel, provider, status, attempt, " +
	"semantic_code, provider_code, last_error, message_id, next_retry_at, finished_at, created_at, updated_at"

// Upsert 幂等写入渠道明细（重试在同一行上累加 attempt）。
func (r *MySQLDeliveryRepository) Upsert(ctx context.Context, d domain.Delivery) error {
	if err := d.Validate(); err != nil {
		return err
	}
	_, err := r.handle.exec(ctx).ExecContext(ctx,
		`INSERT INTO notification_delivery (id, request_id, tenant_id, channel, provider, status,
		   attempt, semantic_code, provider_code, last_error, message_id, next_retry_at, finished_at,
		   created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = VALUES(status), attempt = VALUES(attempt), semantic_code = VALUES(semantic_code),
		   provider_code = VALUES(provider_code), last_error = VALUES(last_error),
		   message_id = VALUES(message_id), next_retry_at = VALUES(next_retry_at),
		   finished_at = VALUES(finished_at), updated_at = VALUES(updated_at)`,
		d.ID, d.RequestID, d.TenantID, string(d.Channel), d.Provider, string(d.Status),
		d.Attempt, d.SemanticCode, d.ProviderCode, d.LastError, d.MessageID,
		notifyNullTime(d.NextRetryAt), notifyNullTime(d.FinishedAt), d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("notification: upsert delivery %s/%s: %w", d.RequestID, d.Channel, err)
	}
	return nil
}

// Get 按 (request_id, channel) 读取。
func (r *MySQLDeliveryRepository) Get(ctx context.Context, requestID string, channel domain.Channel) (domain.Delivery, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT `+deliveryColumns+` FROM notification_delivery WHERE request_id = ? AND channel = ?`,
		strings.TrimSpace(requestID), string(channel))
	return scanDelivery(row)
}

// ListByRequest 列出通知单全部明细（按渠道排序保证稳定输出）。
func (r *MySQLDeliveryRepository) ListByRequest(ctx context.Context, requestID string) ([]domain.Delivery, error) {
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT `+deliveryColumns+` FROM notification_delivery WHERE request_id = ? ORDER BY channel`,
		strings.TrimSpace(requestID))
	if err != nil {
		return nil, fmt.Errorf("notification: list deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Delivery, 0)
	for rows.Next() {
		d, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

// Update 覆盖更新（状态机合法性由领域层保证）。
func (r *MySQLDeliveryRepository) Update(ctx context.Context, d domain.Delivery) error {
	res, err := r.handle.exec(ctx).ExecContext(ctx,
		`UPDATE notification_delivery
		 SET provider = ?, status = ?, attempt = ?, semantic_code = ?, provider_code = ?,
		     last_error = ?, message_id = ?, next_retry_at = ?, finished_at = ?, updated_at = ?
		 WHERE request_id = ? AND channel = ?`,
		d.Provider, string(d.Status), d.Attempt, d.SemanticCode, d.ProviderCode,
		d.LastError, d.MessageID, notifyNullTime(d.NextRetryAt), notifyNullTime(d.FinishedAt),
		d.UpdatedAt, d.RequestID, string(d.Channel))
	if err != nil {
		return fmt.Errorf("notification: update delivery %s/%s: %w", d.RequestID, d.Channel, err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return domain.ErrDeliveryNotFound
	}
	return nil
}

// FindByMessageID 按渠道消息 ID 定位明细（回执对账键）。
func (r *MySQLDeliveryRepository) FindByMessageID(ctx context.Context, channel domain.Channel, messageID string) (domain.Delivery, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT `+deliveryColumns+` FROM notification_delivery WHERE channel = ? AND message_id = ? LIMIT 1`,
		string(channel), strings.TrimSpace(messageID))
	return scanDelivery(row)
}

// CountByStatus 按状态统计（notify_delivery Gauge 数据源）。
func (r *MySQLDeliveryRepository) CountByStatus(ctx context.Context) (map[domain.DeliveryStatus]int, error) {
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT status, COUNT(*) FROM notification_delivery GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("notification: count deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := make(map[domain.DeliveryStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("notification: scan delivery count: %w", err)
		}
		counts[domain.DeliveryStatus(status)] = count
	}
	return counts, rows.Err()
}

func scanDelivery(row notifyRowScanner) (domain.Delivery, error) {
	var d domain.Delivery
	var channel, provider, status string
	var semanticCode, providerCode, lastError, messageID sql.NullString
	var nextRetryAt, finishedAt sql.NullTime
	var createdAt, updatedAt time.Time
	if err := row.Scan(&d.ID, &d.RequestID, &d.TenantID, &channel, &provider, &status, &d.Attempt,
		&semanticCode, &providerCode, &lastError, &messageID, &nextRetryAt, &finishedAt,
		&createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Delivery{}, domain.ErrDeliveryNotFound
		}
		return domain.Delivery{}, fmt.Errorf("notification: scan delivery: %w", err)
	}
	d.Channel = domain.Channel(channel)
	d.Provider = provider
	d.Status = domain.DeliveryStatus(status)
	d.SemanticCode = semanticCode.String
	d.ProviderCode = providerCode.String
	d.LastError = lastError.String
	d.MessageID = messageID.String
	if nextRetryAt.Valid {
		moment := nextRetryAt.Time
		d.NextRetryAt = &moment
	}
	if finishedAt.Valid {
		moment := finishedAt.Time
		d.FinishedAt = &moment
	}
	d.CreatedAt = createdAt
	d.UpdatedAt = updatedAt
	return d, nil
}

// ---------- 模板 ----------

// MySQLTemplateRepository 是模板仓储的 MySQL 实现。
type MySQLTemplateRepository struct {
	handle notifyDB
}

// NewMySQLTemplateRepository 构造模板仓储。
func NewMySQLTemplateRepository(sqlDB *sql.DB) (*MySQLTemplateRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("notification template repository requires a database handle")
	}
	return &MySQLTemplateRepository{handle: notifyDB{db: sqlDB}}, nil
}

// Upsert 写入模板：同 (key, channel, locale) 已存在则版本 +1。
func (r *MySQLTemplateRepository) Upsert(ctx context.Context, t domain.Template) (domain.Template, error) {
	if err := t.Validate(); err != nil {
		return domain.Template{}, err
	}
	res, err := r.handle.exec(ctx).ExecContext(ctx,
		`INSERT INTO notification_template (id, template_key, channel, locale, version,
		   subject, body, enabled, created_by, created_at, updated_at)
		 VALUES (UUID_SHORT(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   version = version + 1, subject = VALUES(subject), body = VALUES(body),
		   enabled = VALUES(enabled), created_by = VALUES(created_by), updated_at = VALUES(updated_at)`,
		t.Key, string(t.Channel), t.Locale, t.Version, t.Subject, t.Body,
		t.Enabled, t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return domain.Template{}, fmt.Errorf("notification: upsert template %s: %w", t.Key, err)
	}
	// 版本由库内自增：重读一次拿到落库版本（吞吐要求低，可接受）。
	saved, err := r.Find(ctx, t.Key, t.Channel, t.Locale)
	if err != nil {
		if _, affectedErr := res.RowsAffected(); affectedErr == nil {
			// 停用的模板 Find 不可见；upsert 语义仍成功，返回入参。
			return t, nil
		}
		return domain.Template{}, err
	}
	return saved, nil
}

// Find 定位启用中的最新版本模板。
func (r *MySQLTemplateRepository) Find(ctx context.Context, key string, channel domain.Channel, locale string) (domain.Template, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT template_key, channel, locale, version, subject, body, enabled, created_by, created_at, updated_at
		 FROM notification_template
		 WHERE template_key = ? AND channel = ? AND locale = ? AND enabled = 1
		 ORDER BY version DESC LIMIT 1`,
		strings.TrimSpace(key), string(channel), locale)
	return scanTemplate(row)
}

// List 按模板键列出全部版本。
func (r *MySQLTemplateRepository) List(ctx context.Context, key string) ([]domain.Template, error) {
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT template_key, channel, locale, version, subject, body, enabled, created_by, created_at, updated_at
		 FROM notification_template WHERE template_key = ? ORDER BY version`,
		strings.TrimSpace(key))
	if err != nil {
		return nil, fmt.Errorf("notification: list templates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Template, 0)
	for rows.Next() {
		t, scanErr := scanTemplate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func scanTemplate(row notifyRowScanner) (domain.Template, error) {
	var t domain.Template
	var channel string
	var enabled bool
	var createdAt, updatedAt time.Time
	if err := row.Scan(&t.Key, &channel, &t.Locale, &t.Version, &t.Subject, &t.Body,
		&enabled, &t.CreatedBy, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Template{}, domain.ErrTemplateNotFound
		}
		return domain.Template{}, fmt.Errorf("notification: scan template: %w", err)
	}
	t.Channel = domain.Channel(channel)
	t.Enabled = enabled
	t.CreatedAt = createdAt
	t.UpdatedAt = updatedAt
	return t, nil
}

// ---------- 偏好 ----------

// MySQLPreferenceRepository 是偏好仓储的 MySQL 实现。
type MySQLPreferenceRepository struct {
	handle notifyDB
}

// NewMySQLPreferenceRepository 构造偏好仓储。
func NewMySQLPreferenceRepository(sqlDB *sql.DB) (*MySQLPreferenceRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("notification preference repository requires a database handle")
	}
	return &MySQLPreferenceRepository{handle: notifyDB{db: sqlDB}}, nil
}

// Upsert 幂等写入偏好。
func (r *MySQLPreferenceRepository) Upsert(ctx context.Context, p domain.Preference) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := r.handle.exec(ctx).ExecContext(ctx,
		`INSERT INTO notification_preference (id, tenant_id, subject_type, subject_id,
		   scene_class, channel, enabled, created_at, updated_at)
		 VALUES (UUID_SHORT(), ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), updated_at = VALUES(updated_at)`,
		p.TenantID, string(p.SubjectType), p.SubjectID, string(p.SceneClass),
		string(p.Channel), p.Enabled, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("notification: upsert preference: %w", err)
	}
	return nil
}

// Get 读取单条偏好。
func (r *MySQLPreferenceRepository) Get(ctx context.Context, tenantID string, subjectType domain.SubjectType,
	subjectID string, class domain.SceneClass, channel domain.Channel) (domain.Preference, error) {
	row := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT tenant_id, subject_type, subject_id, scene_class, channel, enabled, created_at, updated_at
		 FROM notification_preference
		 WHERE tenant_id = ? AND subject_type = ? AND subject_id = ? AND scene_class = ? AND channel = ?`,
		tenantID, string(subjectType), subjectID, string(class), string(channel))
	var p domain.Preference
	var subjectTypeRaw, sceneClassRaw, channelRaw string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&p.TenantID, &subjectTypeRaw, &p.SubjectID, &sceneClassRaw,
		&channelRaw, &p.Enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Preference{}, domain.ErrNotFound
		}
		return domain.Preference{}, fmt.Errorf("notification: scan preference: %w", err)
	}
	p.SubjectType = domain.SubjectType(subjectTypeRaw)
	p.SceneClass = domain.SceneClass(sceneClassRaw)
	p.Channel = domain.Channel(channelRaw)
	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	return p, nil
}

// ListForSubject 列出主体全部偏好。
func (r *MySQLPreferenceRepository) ListForSubject(ctx context.Context, tenantID string,
	subjectType domain.SubjectType, subjectID string) ([]domain.Preference, error) {
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT tenant_id, subject_type, subject_id, scene_class, channel, enabled, created_at, updated_at
		 FROM notification_preference
		 WHERE tenant_id = ? AND subject_type = ? AND subject_id = ? ORDER BY scene_class, channel`,
		tenantID, string(subjectType), subjectID)
	if err != nil {
		return nil, fmt.Errorf("notification: list preferences: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Preference, 0)
	for rows.Next() {
		var p domain.Preference
		var subjectTypeRaw, sceneClassRaw, channelRaw string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&p.TenantID, &subjectTypeRaw, &p.SubjectID, &sceneClassRaw,
			&channelRaw, &p.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("notification: scan preference: %w", err)
		}
		p.SubjectType = domain.SubjectType(subjectTypeRaw)
		p.SceneClass = domain.SceneClass(sceneClassRaw)
		p.Channel = domain.Channel(channelRaw)
		p.CreatedAt = createdAt
		p.UpdatedAt = updatedAt
		items = append(items, p)
	}
	return items, rows.Err()
}

// ---------- 黑名单 ----------

// MySQLBlacklistRepository 是黑名单仓储的 MySQL 实现。
type MySQLBlacklistRepository struct {
	handle notifyDB
}

// NewMySQLBlacklistRepository 构造黑名单仓储。
func NewMySQLBlacklistRepository(sqlDB *sql.DB) (*MySQLBlacklistRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("notification blacklist repository requires a database handle")
	}
	return &MySQLBlacklistRepository{handle: notifyDB{db: sqlDB}}, nil
}

// Add 加入黑名单（幂等）。
func (r *MySQLBlacklistRepository) Add(ctx context.Context, b domain.Blacklist) error {
	if err := b.Validate(); err != nil {
		return err
	}
	_, err := r.handle.exec(ctx).ExecContext(ctx,
		`INSERT INTO notification_blacklist (id, tenant_id, channel, target_hmac, source, reason, created_by, created_at)
		 VALUES (UUID_SHORT(), ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE source = VALUES(source), reason = VALUES(reason)`,
		b.TenantID, string(b.Channel), b.TargetHMAC, string(b.Source), b.Reason, b.CreatedBy, b.CreatedAt)
	if err != nil {
		return fmt.Errorf("notification: add blacklist entry: %w", err)
	}
	return nil
}

// Exists 判断目标是否在黑名单（租户级或平台级命中即 true）。
func (r *MySQLBlacklistRepository) Exists(ctx context.Context, tenantID string, channel domain.Channel, targetHMAC string) (bool, error) {
	var count int
	err := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notification_blacklist
		 WHERE channel = ? AND target_hmac = ? AND tenant_id IN (?, ?)`,
		string(channel), targetHMAC, tenantID, domain.PlatformTenantID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("notification: check blacklist: %w", err)
	}
	return count > 0, nil
}

// Remove 移除黑名单条目。
func (r *MySQLBlacklistRepository) Remove(ctx context.Context, tenantID string, channel domain.Channel, targetHMAC string) error {
	res, err := r.handle.exec(ctx).ExecContext(ctx,
		`DELETE FROM notification_blacklist WHERE tenant_id = ? AND channel = ? AND target_hmac = ?`,
		tenantID, string(channel), targetHMAC)
	if err != nil {
		return fmt.Errorf("notification: remove blacklist entry: %w", err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// List 列出租户黑名单（含平台级条目）。
func (r *MySQLBlacklistRepository) List(ctx context.Context, tenantID string) ([]domain.Blacklist, error) {
	rows, err := r.handle.query(ctx).QueryContext(ctx,
		`SELECT tenant_id, channel, target_hmac, source, reason, created_by, created_at
		 FROM notification_blacklist WHERE tenant_id IN (?, ?) ORDER BY created_at`,
		tenantID, domain.PlatformTenantID)
	if err != nil {
		return nil, fmt.Errorf("notification: list blacklist: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.Blacklist, 0)
	for rows.Next() {
		var b domain.Blacklist
		var channel, source string
		var createdAt time.Time
		if err := rows.Scan(&b.TenantID, &channel, &b.TargetHMAC, &source, &b.Reason, &b.CreatedBy, &createdAt); err != nil {
			return nil, fmt.Errorf("notification: scan blacklist: %w", err)
		}
		b.Channel = domain.Channel(channel)
		b.Source = domain.BlacklistSource(source)
		b.CreatedAt = createdAt
		items = append(items, b)
	}
	return items, rows.Err()
}

// ---------- 死信载荷读取（Replay 数据源） ----------

// MySQLDLQReader 从 async_dlq 读取死信载荷（ENT-03 建表，只读不写）。
type MySQLDLQReader struct {
	handle notifyDB
}

// NewMySQLDLQReader 构造死信读取器。
func NewMySQLDLQReader(sqlDB *sql.DB) (*MySQLDLQReader, error) {
	if sqlDB == nil {
		return nil, errors.New("notification dlq reader requires a database handle")
	}
	return &MySQLDLQReader{handle: notifyDB{db: sqlDB}}, nil
}

// PayloadBySource 按原事件 ID 读取死信载荷；不存在返回 ErrNotFound。
func (r *MySQLDLQReader) PayloadBySource(ctx context.Context, sourceEventID string) ([]byte, error) {
	var payload []byte
	err := r.handle.query(ctx).QueryRowContext(ctx,
		`SELECT payload FROM async_dlq WHERE source_id = ?`, strings.TrimSpace(sourceEventID)).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("notification: read dlq payload %s: %w", sourceEventID, err)
	}
	return payload, nil
}
