package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// changeRequestSelectColumns 是审批单表的显式列清单（禁止 SELECT *，DATA-04）。
const changeRequestSelectColumns = `id, channel, merchant_id, action, target_type, target_id, payload,
	payload_version, status, reason, requested_by, requested_at, expires_at, approved_by, approved_at,
	decision_reason, applied_by, applied_at, version`

// SQLChangeRequestRepository 是 MySQL 8.0 的审批单仓储实现。
//
// 与角色仓储一致，更新使用 version 做乐观锁：审批人可能在两个页面同时点「通过」，
// 后到的请求必须收到 40900 而不是静默覆盖前一次决定。
type SQLChangeRequestRepository struct {
	sql *sql.DB
}

// exec 返回当前请求的执行器（UnitOfWork 事务内为 *sql.Tx）。
func (r *SQLChangeRequestRepository) exec(ctx context.Context) db.Querier {
	return resolveExecutor(ctx, r.sql)
}

// NewSQLChangeRequestRepository 基于已打开的 *sql.DB 创建审批单仓储。
func NewSQLChangeRequestRepository(sqlDB *sql.DB) *SQLChangeRequestRepository {
	return &SQLChangeRequestRepository{sql: sqlDB}
}

// Save 创建（expectedVersion=0）或更新审批单。
//
// channel / action / target_type 在更新时不可变：审批单的目标一旦确定，
// 不能用同一张单子改投其它聚合（否则审批人批准的内容与实际执行不一致）。
func (r *SQLChangeRequestRepository) Save(ctx context.Context, request *domain.ChangeRequest, expectedVersion int64) error {
	if request == nil {
		return apperr.BadRequest("change request is required")
	}
	if r.sql == nil {
		return apperr.Unavailable("iam change request repository is not configured")
	}
	requestID, err := parseID(request.ID)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		_, err = r.exec(ctx).ExecContext(ctx, `INSERT INTO iam_change_request
			(id, channel, merchant_id, action, target_type, target_id, payload, payload_version,
				status, reason, requested_by, requested_at, expires_at, approved_by, approved_at,
				decision_reason, applied_by, applied_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			requestID, string(request.Channel), request.MerchantID, string(request.Action),
			string(request.TargetType), request.TargetID, request.Payload, request.PayloadVersion,
			string(request.Status), request.Reason, request.RequestedBy, request.RequestedAt.UTC(),
			nullTime(request.ExpiresAt), request.ApprovedBy, nullTime(request.ApprovedAt),
			request.DecisionReason, request.AppliedBy, nullTime(request.AppliedAt), request.Version)
		return conflictOnDuplicate(err, "change request already pending for this target")
	}

	result, err := r.exec(ctx).ExecContext(ctx, `UPDATE iam_change_request
		SET target_id = ?, status = ?, reason = ?, approved_by = ?, approved_at = ?,
			decision_reason = ?, applied_by = ?, applied_at = ?, version = ?
		WHERE id = ? AND version = ?`,
		request.TargetID, string(request.Status), request.Reason, request.ApprovedBy,
		nullTime(request.ApprovedAt), request.DecisionReason, request.AppliedBy,
		nullTime(request.AppliedAt), request.Version, requestID, expectedVersion)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 行不存在或版本过期统一返回冲突：区分二者会让调用方探测任意 ID 是否存在。
		return apperr.Conflict("change request version conflict")
	}
	return nil
}

// FindByID 按主键加载审批单；不存在返回 NotFound。
func (r *SQLChangeRequestRepository) FindByID(ctx context.Context, id string) (*domain.ChangeRequest, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam change request repository is not configured")
	}
	requestID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := r.exec(ctx).QueryRowContext(ctx,
		`SELECT `+changeRequestSelectColumns+` FROM iam_change_request WHERE id = ?`, requestID)
	request, err := scanChangeRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("change request not found")
	}
	if err != nil {
		return nil, err
	}
	return request, nil
}

// List 按条件分页返回审批单，按申请时间倒序（同一时刻按 ID 倒序），保证分页稳定。
func (r *SQLChangeRequestRepository) List(ctx context.Context, filter domain.ChangeRequestFilter) ([]*domain.ChangeRequest, error) {
	if r.sql == nil {
		return nil, apperr.Unavailable("iam change request repository is not configured")
	}
	clause, args := changeRequestFilterClause(filter)
	args = append(args, normalizeLimit(filter.Limit), normalizeOffset(filter.Offset))

	rows, err := r.exec(ctx).QueryContext(ctx,
		`SELECT `+changeRequestSelectColumns+` FROM iam_change_request WHERE 1 = 1`+clause+
			` ORDER BY requested_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.ChangeRequest, 0, normalizeLimit(filter.Limit))
	for rows.Next() {
		request, scanErr := scanChangeRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, request)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 返回与 List 相同条件的审批单总数（索引 idx_iam_change_status 覆盖）。
func (r *SQLChangeRequestRepository) Count(ctx context.Context, filter domain.ChangeRequestFilter) (int, error) {
	if r.sql == nil {
		return 0, apperr.Unavailable("iam change request repository is not configured")
	}
	clause, args := changeRequestFilterClause(filter)
	var total int
	if err := r.exec(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_change_request WHERE 1 = 1`+clause, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// changeRequestFilterClause 生成 List / Count 的过滤子句，空条件表示不过滤。
func changeRequestFilterClause(filter domain.ChangeRequestFilter) (clause string, args []any) {
	var builder strings.Builder
	if filter.Channel != "" {
		builder.WriteString(` AND channel = ?`)
		args = append(args, string(filter.Channel))
	}
	if filter.MerchantID != "" {
		builder.WriteString(` AND merchant_id = ?`)
		args = append(args, filter.MerchantID)
	}
	if filter.Status != "" {
		builder.WriteString(` AND status = ?`)
		args = append(args, string(filter.Status))
	}
	if filter.RequestedBy != "" {
		builder.WriteString(` AND requested_by = ?`)
		args = append(args, filter.RequestedBy)
	}
	if filter.Action != "" {
		builder.WriteString(` AND action = ?`)
		args = append(args, string(filter.Action))
	}
	return builder.String(), args
}

// scanChangeRequest 扫描一行审批单；expires_at / approved_at / applied_at 允许为 NULL。
func scanChangeRequest(row scanRow) (*domain.ChangeRequest, error) {
	var (
		request    domain.ChangeRequest
		expiresAt  sql.NullTime
		approvedAt sql.NullTime
		appliedAt  sql.NullTime
	)
	err := row.Scan(
		&request.ID, &request.Channel, &request.MerchantID, &request.Action, &request.TargetType,
		&request.TargetID, &request.Payload, &request.PayloadVersion, &request.Status,
		&request.Reason, &request.RequestedBy, &request.RequestedAt, &expiresAt,
		&request.ApprovedBy, &approvedAt, &request.DecisionReason, &request.AppliedBy,
		&appliedAt, &request.Version,
	)
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		request.ExpiresAt = &value
	}
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		request.ApprovedAt = &value
	}
	if appliedAt.Valid {
		value := appliedAt.Time.UTC()
		request.AppliedAt = &value
	}
	request.RequestedAt = request.RequestedAt.UTC()
	return &request, nil
}
