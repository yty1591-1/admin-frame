package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// certificationCampaignSelectColumns 是活动表的显式列清单（禁止 SELECT *，DATA-04）。
const certificationCampaignSelectColumns = `id, name, channel, merchant_id, role_ids, privileged_only,
	status, due_at, created_by, created_at, closed_at, version, task_count`

// certificationTaskSelectColumns 是任务表的显式列清单。
const certificationTaskSelectColumns = `id, campaign_id, subject_type, subject_id, merchant_id,
	role_id, binding_id, binding_fingerprint, reviewer_id, status, decision, reason, decided_by,
	decided_at, created_at, version`

// SQLCertificationCampaignRepository 是 MySQL 8.0 的访问复核活动仓储。
type SQLCertificationCampaignRepository struct {
	sql *sql.DB
}

// NewSQLCertificationCampaignRepository 基于已打开的 *sql.DB 创建活动仓储。
func NewSQLCertificationCampaignRepository(sqlDB *sql.DB) *SQLCertificationCampaignRepository {
	return &SQLCertificationCampaignRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLCertificationCampaignRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// Save 创建（expectedVersion=0）或更新活动，更新使用 version 乐观锁。
func (r *SQLCertificationCampaignRepository) Save(
	ctx context.Context, campaign *domain.CertificationCampaign, expectedVersion int64,
) error {
	if campaign == nil {
		return apperr.BadRequest("certification campaign is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam certification repository is not configured")
	}
	campaignID, err := parseID(campaign.ID)
	if err != nil {
		return err
	}
	roleIDs, err := encodeCertificationRoleIDs(campaign.RoleIDs)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO iam_certification_campaign
			(id, name, channel, merchant_id, role_ids, privileged_only, status, due_at,
			 created_by, created_at, closed_at, version, task_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			campaignID, campaign.Name, string(campaign.Channel), campaign.MerchantID, roleIDs,
			campaign.PrivilegedOnly, string(campaign.Status), campaign.DueAt.UTC(),
			campaign.CreatedBy, campaign.CreatedAt.UTC(), nullTime(campaign.ClosedAt),
			campaign.Version, campaign.TaskCount)
		return conflictOnDuplicate(err, "certification campaign id already exists")
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE iam_certification_campaign
		SET name = ?, role_ids = ?, privileged_only = ?, status = ?, due_at = ?,
			closed_at = ?, version = ?, task_count = ?
		WHERE id = ? AND version = ?`,
		campaign.Name, roleIDs, campaign.PrivilegedOnly, string(campaign.Status),
		campaign.DueAt.UTC(), nullTime(campaign.ClosedAt), campaign.Version,
		campaign.TaskCount, campaignID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// MySQL 默认 RowsAffected 只统计实际变更行：幂等重放（如重复激活已激活活动）
		// 会返回 0，并不代表版本冲突。用当前版本区分「无变更」与「乐观锁失败」。
		var currentVersion int64
		err := r.exec(ctx).QueryRowContext(ctx,
			`SELECT version FROM iam_certification_campaign WHERE id = ?`, campaignID).Scan(&currentVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.NotFound("certification campaign not found")
		}
		if err != nil {
			return err
		}
		if currentVersion != expectedVersion {
			return apperr.Conflict("certification campaign version conflict")
		}
	}
	return nil
}

// FindByID 按主键加载活动。
func (r *SQLCertificationCampaignRepository) FindByID(
	ctx context.Context, id string,
) (*domain.CertificationCampaign, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam certification repository is not configured")
	}
	campaignID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+certificationCampaignSelectColumns+` FROM iam_certification_campaign WHERE id = ?`,
		campaignID)
	campaign, err := scanCertificationCampaign(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("certification campaign not found")
	}
	if err != nil {
		return nil, err
	}
	return campaign, nil
}

// List 按条件分页返回活动，按创建时间倒序。
func (r *SQLCertificationCampaignRepository) List(
	ctx context.Context, filter domain.CertificationCampaignFilter,
) ([]*domain.CertificationCampaign, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam certification repository is not configured")
	}
	if err := validateCertificationCampaignFilter(filter); err != nil {
		return nil, err
	}
	clause, args := certificationCampaignFilterClause(filter)
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+certificationCampaignSelectColumns+` FROM iam_certification_campaign
		 WHERE 1 = 1`+clause+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*domain.CertificationCampaign, 0, normalizeLimit(filter.Limit))
	for rows.Next() {
		campaign, scanErr := scanCertificationCampaign(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, campaign)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 返回满足条件的活动总数。
func (r *SQLCertificationCampaignRepository) Count(
	ctx context.Context, filter domain.CertificationCampaignFilter,
) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam certification repository is not configured")
	}
	if err := validateCertificationCampaignFilter(filter); err != nil {
		return 0, err
	}
	clause, args := certificationCampaignFilterClause(filter)
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_certification_campaign WHERE 1 = 1`+clause, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// validateCertificationCampaignFilter 强制 B 端必须带商户，防止漏过滤导致越权列表。
func validateCertificationCampaignFilter(filter domain.CertificationCampaignFilter) error {
	if filter.Channel == domain.ChannelBiz && strings.TrimSpace(filter.MerchantID) == "" {
		return apperr.BadRequest("merchant id is required on channel biz")
	}
	return nil
}

// certificationCampaignFilterClause 生成 List / Count 的过滤子句。
func certificationCampaignFilterClause(filter domain.CertificationCampaignFilter) (clause string, args []any) {
	if filter.Channel != "" {
		clause += ` AND channel = ?`
		args = append(args, string(filter.Channel))
	}
	if filter.MerchantID != "" {
		clause += ` AND merchant_id = ?`
		args = append(args, filter.MerchantID)
	}
	if filter.Status != "" {
		clause += ` AND status = ?`
		args = append(args, string(filter.Status))
	}
	return clause, args
}

// scanCertificationCampaign 扫描一行活动；role_ids 为 JSON 数组，closed_at 可为 NULL。
func scanCertificationCampaign(row scanRow) (*domain.CertificationCampaign, error) {
	var (
		campaign   domain.CertificationCampaign
		roleIDs    []byte
		closedAt   sql.NullTime
		privileged bool
	)
	err := row.Scan(
		&campaign.ID, &campaign.Name, &campaign.Channel, &campaign.MerchantID, &roleIDs,
		&privileged, &campaign.Status, &campaign.DueAt, &campaign.CreatedBy,
		&campaign.CreatedAt, &closedAt, &campaign.Version, &campaign.TaskCount)
	if err != nil {
		return nil, err
	}
	campaign.PrivilegedOnly = privileged
	if len(roleIDs) > 0 {
		if err := json.Unmarshal(roleIDs, &campaign.RoleIDs); err != nil {
			return nil, apperr.Internal("decode certification role ids", err)
		}
	}
	if closedAt.Valid {
		value := closedAt.Time.UTC()
		campaign.ClosedAt = &value
	}
	return &campaign, nil
}

// SQLCertificationTaskRepository 是 MySQL 8.0 的访问复核任务仓储。
type SQLCertificationTaskRepository struct {
	sql *sql.DB
}

// NewSQLCertificationTaskRepository 基于已打开的 *sql.DB 创建任务仓储。
func NewSQLCertificationTaskRepository(sqlDB *sql.DB) *SQLCertificationTaskRepository {
	return &SQLCertificationTaskRepository{sql: sqlDB}
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLCertificationTaskRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// SaveAll 幂等批量写入任务；唯一键冲突时 ON DUPLICATE KEY UPDATE id = id 保留既有行。
// certificationTaskInsertBatch 是批量插入的单批行数。
//
// 16 列 × 500 行 = 8000 个占位符，远低于 MySQL 65535 的上限，
// 单批约 100KB，也不会触碰 max_allowed_packet。取值偏小是为了让单条语句
// 的锁持有时间短、失败定位仍能落到具体批次。
const certificationTaskInsertBatch = 500

// SaveAll 批量插入活动任务。
//
// 逐条 INSERT 在 1 万任务时会形成 1 万次往返（实测 13.8s），而季度复核
// 常在业务库上执行，必须压成按批的多值插入（同规模实测 1s 级）。
// 分批仍整体位于调用方事务内：任一批失败即返回错误，由事务回滚全部批次，
// 因此 SaveAll 的「全成或全不成」契约不变。
func (r *SQLCertificationTaskRepository) SaveAll(ctx context.Context, tasks []*domain.CertificationTask) error {
	if r.sql == nil {
		return apperr.Unavailable("iam certification repository is not configured")
	}
	const columnsPerRow = 16
	for start := 0; start < len(tasks); start += certificationTaskInsertBatch {
		end := start + certificationTaskInsertBatch
		if end > len(tasks) {
			end = len(tasks)
		}
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*columnsPerRow)
		for _, task := range tasks[start:end] {
			if task == nil {
				return apperr.BadRequest("certification task is required")
			}
			taskID, err := parseID(task.ID)
			if err != nil {
				return err
			}
			campaignID, err := parseID(task.CampaignID)
			if err != nil {
				return err
			}
			roleID, err := parseID(task.RoleID)
			if err != nil {
				return err
			}
			bindingID, err := parseID(task.BindingID)
			if err != nil {
				return err
			}
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				taskID, campaignID, string(task.SubjectType), task.SubjectID, task.MerchantID,
				roleID, bindingID, task.BindingFingerprint, task.ReviewerID, string(task.Status),
				string(task.Decision), task.Reason, task.DecidedBy, nullTime(task.DecidedAt),
				task.CreatedAt.UTC(), task.Version,
			)
		}
		query := `INSERT INTO iam_certification_task
			(id, campaign_id, subject_type, subject_id, merchant_id, role_id, binding_id,
			 binding_fingerprint, reviewer_id, status, decision, reason, decided_by, decided_at,
			 created_at, version)
			VALUES ` + strings.Join(placeholders, ", ") + `
			ON DUPLICATE KEY UPDATE id = id`
		if _, err := r.exec(ctx).ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// FindByID 按主键加载任务。
func (r *SQLCertificationTaskRepository) FindByID(
	ctx context.Context, id string,
) (*domain.CertificationTask, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam certification repository is not configured")
	}
	taskID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+certificationTaskSelectColumns+` FROM iam_certification_task WHERE id = ?`, taskID)
	task, err := scanCertificationTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("certification task not found")
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

// ListByCampaign 返回活动下全部任务，按创建时间、主键升序。
func (r *SQLCertificationTaskRepository) ListByCampaign(
	ctx context.Context, campaignID string,
) ([]*domain.CertificationTask, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam certification repository is not configured")
	}
	id, err := parseID(campaignID)
	if err != nil {
		return nil, err
	}
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+certificationTaskSelectColumns+` FROM iam_certification_task
		 WHERE campaign_id = ? ORDER BY created_at ASC, id ASC`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCertificationTaskRows(rows)
}

// ListPage 按活动与状态分页列举任务。
func (r *SQLCertificationTaskRepository) ListPage(
	ctx context.Context, filter domain.CertificationTaskFilter,
) ([]*domain.CertificationTask, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam certification repository is not configured")
	}
	campaignID, err := parseID(filter.CampaignID)
	if err != nil {
		return nil, err
	}
	clause := ""
	args := []any{campaignID}
	if filter.Status != "" {
		clause = ` AND status = ?`
		args = append(args, string(filter.Status))
	}
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))
	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+certificationTaskSelectColumns+` FROM iam_certification_task
		 WHERE campaign_id = ?`+clause+` ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCertificationTaskRows(rows)
}

// CountByCampaign 返回活动下的任务总数。
func (r *SQLCertificationTaskRepository) CountByCampaign(ctx context.Context, campaignID string) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam certification repository is not configured")
	}
	id, err := parseID(campaignID)
	if err != nil {
		return 0, err
	}
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_certification_task WHERE campaign_id = ?`, id).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// Save 更新任务并执行乐观锁。
func (r *SQLCertificationTaskRepository) Save(
	ctx context.Context, task *domain.CertificationTask, expectedVersion int64,
) error {
	if task == nil {
		return apperr.BadRequest("certification task is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam certification repository is not configured")
	}
	taskID, err := parseID(task.ID)
	if err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE iam_certification_task
		SET status = ?, decision = ?, reason = ?, decided_by = ?, decided_at = ?, version = ?
		WHERE id = ? AND version = ?`,
		string(task.Status), string(task.Decision), task.Reason, task.DecidedBy,
		nullTime(task.DecidedAt), task.Version, taskID, expectedVersion)
	if err != nil {
		return conflictOnDuplicate(err, "certification task subject and role already exist in campaign")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperr.Conflict("certification task version conflict")
	}
	return nil
}

// scanCertificationTaskRows 扫描任务结果集。
func scanCertificationTaskRows(rows *sql.Rows) ([]*domain.CertificationTask, error) {
	out := make([]*domain.CertificationTask, 0)
	for rows.Next() {
		task, err := scanCertificationTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanCertificationTask 扫描一行任务；decided_at 可为 NULL。
func scanCertificationTask(row scanRow) (*domain.CertificationTask, error) {
	var (
		task      domain.CertificationTask
		decidedAt sql.NullTime
	)
	err := row.Scan(
		&task.ID, &task.CampaignID, &task.SubjectType, &task.SubjectID, &task.MerchantID,
		&task.RoleID, &task.BindingID, &task.BindingFingerprint, &task.ReviewerID,
		&task.Status, &task.Decision, &task.Reason, &task.DecidedBy, &decidedAt,
		&task.CreatedAt, &task.Version)
	if err != nil {
		return nil, err
	}
	if decidedAt.Valid {
		value := decidedAt.Time.UTC()
		task.DecidedAt = &value
	}
	return &task, nil
}

// encodeCertificationRoleIDs 把角色范围编码为 JSON 数组；空范围写 `[]`。
func encodeCertificationRoleIDs(roleIDs []string) ([]byte, error) {
	if len(roleIDs) == 0 {
		return []byte("[]"), nil
	}
	payload, err := json.Marshal(roleIDs)
	if err != nil {
		return nil, apperr.Internal("encode certification role ids", err)
	}
	return payload, nil
}
