package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// MySQLRepository 是租户仓储的 MySQL 实现。
//
// 表结构见 migrations/000008_create_tenant.up.sql；状态迁移用
// `WHERE version = ?` 做 CAS，并发下第二次写入必然影响 0 行。
type MySQLRepository struct {
	db *sql.DB
}

// NewMySQLRepository 构造 MySQL 仓储；db 为 nil 返回错误（装配期即失败）。
func NewMySQLRepository(sqlDB *sql.DB) (*MySQLRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("tenant repository requires a database handle")
	}
	return &MySQLRepository{db: sqlDB}, nil
}

// tenantColumns 固定列顺序，避免 SELECT * 与扫描顺序漂移。
const tenantColumns = "id, code, name, status, plan_id, status_reason, " +
	"contact_name, contact_email, industry, timezone, locale, external_id, " +
	"version, suspended_at, terminated_at, created_at, updated_at"

// WithinTx 在事务内执行 fn，事务注入 ctx 后仓储与 OutboxEmitter 自动复用。
func (r *MySQLRepository) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("tenant: transaction requires a function")
	}
	return db.WithinUnitOfWork(ctx, r.db, nil, fn)
}

// Create 插入租户；主键或 code 唯一键冲突返回对应领域错误。
func (r *MySQLRepository) Create(ctx context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant (`+tenantColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenant.ID, tenant.Code, tenant.Name, string(tenant.Status), nullString(tenant.PlanID), tenant.StatusReason,
		nullString(tenant.ContactName), nullString(tenant.ContactEmail), nullString(tenant.Industry),
		nullString(tenant.Timezone), nullString(tenant.Locale), nullString(tenant.ExternalID),
		tenant.Version,
		nullTime(tenant.SuspendedAt), nullTime(tenant.TerminatedAt), tenant.CreatedAt, tenant.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err) {
			// 两个唯一键冲突语义不同：ID 重复是系统事故（ID 生成器不应撞），
			// code 重复是运营输入错误，给调用方可区分的错误码。
			if strings.Contains(err.Error(), "uk_tenant_code") {
				return fmt.Errorf("%w: %s", domain.ErrCodeExists, tenant.Code)
			}
			return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, tenant.ID)
		}
		return fmt.Errorf("tenant: create %s: %w", tenant.ID, err)
	}
	return nil
}

// Get 按 ID 读取租户。
func (r *MySQLRepository) Get(ctx context.Context, id string) (domain.Tenant, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+tenantColumns+` FROM tenant WHERE id = ?`, strings.TrimSpace(id))
	tenant, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Tenant{}, domain.ErrNotFound
		}
		return domain.Tenant{}, fmt.Errorf("tenant: get %s: %w", id, err)
	}
	return tenant, nil
}

// List 按状态 / 名称过滤并分页；总数与当页数据分别查询。
func (r *MySQLRepository) List(ctx context.Context, filter domain.Filter) ([]domain.Tenant, int, error) {
	filter = filter.Normalize()

	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(filter.Status))
	}
	if filter.Keyword != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+escapeLike(filter.Keyword)+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.query(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant`+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tenant: count: %w", err)
	}

	query := `SELECT ` + tenantColumns + ` FROM tenant` + clause + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := r.query(ctx).QueryContext(ctx, query, append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("tenant: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]domain.Tenant, 0, filter.Limit)
	for rows.Next() {
		tenant, scanErr := scanTenant(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("tenant: scan: %w", scanErr)
		}
		items = append(items, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tenant: iterate: %w", err)
	}
	return items, total, nil
}

// UpdateStatus 以 `version = tenant.Version - 1` 做 CAS 写入。
func (r *MySQLRepository) UpdateStatus(ctx context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE tenant
		    SET status = ?, status_reason = ?, suspended_at = ?, terminated_at = ?,
		        version = ?, updated_at = ?
		  WHERE id = ? AND version = ?`,
		string(tenant.Status), tenant.StatusReason, nullTime(tenant.SuspendedAt), nullTime(tenant.TerminatedAt),
		tenant.Version, tenant.UpdatedAt, tenant.ID, tenant.Version-1)
	return r.finishCAS(ctx, tenant, "update status", result, err)
}

// CountByStatus 按状态统计租户数（单次聚合查询）。
func (r *MySQLRepository) CountByStatus(ctx context.Context) (map[domain.Status]int, error) {
	rows, err := r.query(ctx).QueryContext(ctx,
		`SELECT status, COUNT(*) FROM tenant GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("tenant: count by status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[domain.Status]int, 4)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("tenant: scan status count: %w", err)
		}
		counts[domain.Status(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant: iterate status counts: %w", err)
	}
	return counts, nil
}

// UpdateProfile 以 CAS 写入资料变更（T-11）。
//
// 只写资料列，不碰状态列：两条 CAS 通道共用 version 计数器，
// 但各自只更新自己负责的字段，避免一条路径把另一条的字段回写成旧值。
func (r *MySQLRepository) UpdateProfile(ctx context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE tenant
		    SET name = ?, contact_name = ?, contact_email = ?, industry = ?,
		        timezone = ?, locale = ?, external_id = ?,
		        version = ?, updated_at = ?
		  WHERE id = ? AND version = ?`,
		tenant.Name, nullString(tenant.ContactName), nullString(tenant.ContactEmail), nullString(tenant.Industry),
		nullString(tenant.Timezone), nullString(tenant.Locale), nullString(tenant.ExternalID),
		tenant.Version, tenant.UpdatedAt, tenant.ID, tenant.Version-1)
	return r.finishCAS(ctx, tenant, "update profile", result, err)
}

// finishCAS 统一处理 CAS 写入的 0 行分支：区分「不存在」与「版本被并发改动」。
func (r *MySQLRepository) finishCAS(ctx context.Context, tenant domain.Tenant, op string,
	result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("tenant: %s %s: %w", op, tenant.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant: %s %s: %w", op, tenant.ID, err)
	}
	if affected == 1 {
		return nil
	}

	// 0 行有两种原因：租户不存在，或版本已被并发改动。二者语义不同，必须区分。
	var exists int
	if err := r.query(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant WHERE id = ?`, tenant.ID).Scan(&exists); err != nil {
		return fmt.Errorf("tenant: verify %s: %w", tenant.ID, err)
	}
	if exists == 0 {
		return domain.ErrNotFound
	}
	return fmt.Errorf("%w: tenant %s version %d", domain.ErrVersionConflict, tenant.ID, tenant.Version-1)
}

// execer / queryer 让仓储在事务内复用调用方的 *sql.Tx（pkg/db.TxFromContext）。
//
// 没有这两个助手时，事务内的每次查询都会另开连接，锁与写入不在同一会话，
// 「先 FOR UPDATE 再计数」会退化成两个独立快照。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *MySQLRepository) exec(ctx context.Context) execer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

func (r *MySQLRepository) query(ctx context.Context) queryer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// Mutate 在事务内锁住租户行后执行 fn，串行化同一租户的成员变更。
//
// 选择锁 tenant 行而不是 member 行：不变量是租户级的（至少一名 owner），
// 锁租户行能一次性覆盖「计数 + 改这一行」，也不会产生间隙锁竞争。
func (r *MySQLRepository) Mutate(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("tenant: member mutation requires a function")
	}
	trimmed := strings.TrimSpace(tenantID)
	if trimmed == "" {
		return domain.ErrNotFound
	}
	return db.WithinUnitOfWork(ctx, r.db, nil, func(txCtx context.Context) error {
		tx, ok := db.TxFromContext(txCtx)
		if !ok {
			return errors.New("tenant: member mutation requires a transaction")
		}
		var id string
		err := tx.QueryRowContext(txCtx, `SELECT id FROM tenant WHERE id = ? FOR UPDATE`, trimmed).Scan(&id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("tenant: lock %s: %w", trimmed, err)
		}
		return fn(txCtx)
	})
}

// rowScanner 抽象 *sql.Row 与 *sql.Rows 的共同能力。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTenant 按 tenantColumns 的顺序扫描一行。
func scanTenant(scanner rowScanner) (domain.Tenant, error) {
	var (
		tenant         domain.Tenant
		status         string
		nullablePlanID sql.NullString
		contactName    sql.NullString
		contactEmail   sql.NullString
		industry       sql.NullString
		timezone       sql.NullString
		locale         sql.NullString
		externalID     sql.NullString
		suspendedAt    sql.NullTime
		terminatedAt   sql.NullTime
	)
	if err := scanner.Scan(
		&tenant.ID, &tenant.Code, &tenant.Name, &status, &nullablePlanID, &tenant.StatusReason,
		&contactName, &contactEmail, &industry, &timezone, &locale, &externalID,
		&tenant.Version, &suspendedAt, &terminatedAt, &tenant.CreatedAt, &tenant.UpdatedAt,
	); err != nil {
		return domain.Tenant{}, err
	}
	tenant.Status = domain.Status(status)
	tenant.PlanID = nullablePlanID.String
	tenant.ContactName = contactName.String
	tenant.ContactEmail = contactEmail.String
	tenant.Industry = industry.String
	tenant.Timezone = timezone.String
	tenant.Locale = locale.String
	tenant.ExternalID = externalID.String
	if suspendedAt.Valid {
		moment := suspendedAt.Time
		tenant.SuspendedAt = &moment
	}
	if terminatedAt.Valid {
		moment := terminatedAt.Time
		tenant.TerminatedAt = &moment
	}
	return tenant, nil
}

// nullString 把空串转 NULL：tenant.plan_id 允许未回填（领域层容忍空值）。
func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// nullTime 把可空时间转成驱动可写的值。
func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// escapeLike 转义 LIKE 通配符，避免名称里的 % / _ 变成模糊匹配。
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return replacer.Replace(value)
}

// isDuplicateKey 判断是否为 MySQL 主键 / 唯一键冲突（1062）。
func isDuplicateKey(err error) bool {
	return strings.Contains(err.Error(), "Error 1062") || strings.Contains(err.Error(), "Duplicate entry")
}

// memberColumns 固定成员列顺序。
const memberColumns = "tenant_id, subject_id, role, status, created_at, updated_at"

// UpsertMember 幂等写入成员。
//
// 冲突时不更新 created_at：它是「何时进这个租户」的事实，改角色 / 停用再启用都不应重置。
func (r *MySQLRepository) UpsertMember(ctx context.Context, member domain.Member) error {
	if err := member.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant_member (`+memberColumns+`) VALUES (?, ?, ?, ?, ?, ?) AS new
		 ON DUPLICATE KEY UPDATE role = new.role, status = new.status, updated_at = new.updated_at`,
		member.TenantID, member.SubjectID, string(member.Role), string(member.Status),
		member.CreatedAt, member.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tenant: upsert member %s/%s: %w", member.TenantID, member.SubjectID, err)
	}
	return nil
}

// GetMember 读取单个成员。
func (r *MySQLRepository) GetMember(ctx context.Context, tenantID, subjectID string) (domain.Member, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+memberColumns+` FROM tenant_member WHERE tenant_id = ? AND subject_id = ?`,
		strings.TrimSpace(tenantID), strings.TrimSpace(subjectID))
	member, err := scanMember(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Member{}, domain.ErrMemberNotFound
		}
		return domain.Member{}, fmt.Errorf("tenant: get member %s/%s: %w", tenantID, subjectID, err)
	}
	return member, nil
}

// ListMembers 分页列出租户成员（T-13），owner 在前、同角色按主体排序。
//
// limit<=0 表示全量（内部席位计数等路径复用）；返回满足条件的总数。
func (r *MySQLRepository) ListMembers(ctx context.Context, tenantID string, includeDisabled bool, limit, offset int) ([]domain.Member, int, error) {
	trimmed := strings.TrimSpace(tenantID)
	where := " WHERE tenant_id = ?"
	if !includeDisabled {
		where += ` AND status = 'active'`
	}

	var total int
	if err := r.query(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_member`+where, trimmed).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tenant: count members %s: %w", tenantID, err)
	}

	query := `SELECT ` + memberColumns + ` FROM tenant_member` + where +
		` ORDER BY FIELD(role, 'owner', 'admin', 'member'), subject_id`
	args := []any{trimmed}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	} else if offset > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, 1<<31-1, offset) // MySQL OFFSET 必须搭配 LIMIT；给 int 上限表示不限制。
	}

	rows, err := r.query(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("tenant: list members %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Member, 0, 8)
	for rows.Next() {
		member, scanErr := scanMember(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("tenant: scan member: %w", scanErr)
		}
		out = append(out, member)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tenant: iterate members: %w", err)
	}
	return out, total, nil
}

// CountActive 统计在册成员总数；事务内追加 FOR UPDATE（语义同 CountActiveByRole）。
func (r *MySQLRepository) CountActive(ctx context.Context, tenantID string) (int, error) {
	query := `SELECT COUNT(*) FROM tenant_member WHERE tenant_id = ? AND status = 'active'`
	if _, ok := db.TxFromContext(ctx); ok {
		query += ` FOR UPDATE`
	}
	var count int
	if err := r.query(ctx).QueryRowContext(ctx, query, strings.TrimSpace(tenantID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("tenant: count active members %s: %w", tenantID, err)
	}
	return count, nil
}

// CountActiveByRole 统计某角色的在册成员数。
//
// 事务内追加 FOR UPDATE：REPEATABLE READ 下普通 SELECT 可能复用事务开始时的快照，
// 让刚拿到租户锁的请求仍看到别人提交前的 owner 数，行锁就白加了。
func (r *MySQLRepository) CountActiveByRole(ctx context.Context, tenantID string, role domain.MemberRole) (int, error) {
	query := `SELECT COUNT(*) FROM tenant_member WHERE tenant_id = ? AND role = ? AND status = 'active'`
	if _, ok := db.TxFromContext(ctx); ok {
		query += ` FOR UPDATE`
	}
	var count int
	if err := r.query(ctx).QueryRowContext(ctx, query,
		strings.TrimSpace(tenantID), string(role)).Scan(&count); err != nil {
		return 0, fmt.Errorf("tenant: count members %s/%s: %w", tenantID, role, err)
	}
	return count, nil
}

// UpsertQuota 幂等写入配额上限。
func (r *MySQLRepository) UpsertQuota(ctx context.Context, quota domain.Quota) error {
	if err := quota.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant_quota (tenant_id, resource, limit_value, updated_at) VALUES (?, ?, ?, ?) AS new
		 ON DUPLICATE KEY UPDATE limit_value = new.limit_value, updated_at = new.updated_at`,
		quota.TenantID, quota.Resource, quota.Limit, quota.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tenant: upsert quota %s/%s: %w", quota.TenantID, quota.Resource, err)
	}
	return nil
}

// ListQuotas 列出租户配额。
func (r *MySQLRepository) ListQuotas(ctx context.Context, tenantID string) ([]domain.Quota, error) {
	rows, err := r.query(ctx).QueryContext(ctx,
		`SELECT tenant_id, resource, limit_value, updated_at FROM tenant_quota
		  WHERE tenant_id = ? ORDER BY resource`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, fmt.Errorf("tenant: list quotas %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Quota, 0, 8)
	for rows.Next() {
		var (
			quota domain.Quota
			value int64
		)
		if err := rows.Scan(&quota.TenantID, &quota.Resource, &value, &quota.UpdatedAt); err != nil {
			return nil, fmt.Errorf("tenant: scan quota: %w", err)
		}
		quota.Limit = value
		out = append(out, quota)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant: iterate quotas: %w", err)
	}
	return out, nil
}

// scanMember 按 memberColumns 顺序扫描一行。
func scanMember(scanner rowScanner) (domain.Member, error) {
	var (
		member domain.Member
		role   string
		status string
	)
	if err := scanner.Scan(&member.TenantID, &member.SubjectID, &role, &status,
		&member.CreatedAt, &member.UpdatedAt); err != nil {
		return domain.Member{}, err
	}
	member.Role = domain.MemberRole(role)
	member.Status = domain.MemberStatus(status)
	return member, nil
}

// planColumns 固定套餐列顺序。
const planColumns = "id, code, name, is_default, quotas, created_at, updated_at"

// DefaultPlan 返回默认套餐；多个 default 时取编码最小者（种子应保证恰好一个）。
func (r *MySQLRepository) DefaultPlan(ctx context.Context) (domain.Plan, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+planColumns+` FROM tenant_plan WHERE is_default = 1 ORDER BY code LIMIT 1`)
	plan, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Plan{}, domain.ErrNoDefaultPlan
		}
		return domain.Plan{}, fmt.Errorf("tenant: load default plan: %w", err)
	}
	return plan, nil
}

// FindByCode 按编码加载套餐。
func (r *MySQLRepository) FindByCode(ctx context.Context, code string) (domain.Plan, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+planColumns+` FROM tenant_plan WHERE code = ?`, strings.TrimSpace(code))
	plan, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Plan{}, domain.ErrPlanNotFound
		}
		return domain.Plan{}, fmt.Errorf("tenant: load plan %s: %w", code, err)
	}
	return plan, nil
}

// scanPlan 按 planColumns 顺序扫描并解析配额 JSON。
func scanPlan(scanner rowScanner) (domain.Plan, error) {
	var (
		plan     domain.Plan
		quotasJS []byte
	)
	if err := scanner.Scan(&plan.ID, &plan.Code, &plan.Name, &plan.IsDefault,
		&quotasJS, &plan.CreatedAt, &plan.UpdatedAt); err != nil {
		return domain.Plan{}, err
	}
	quotas, err := decodePlanQuotas(quotasJS)
	if err != nil {
		return domain.Plan{}, err
	}
	plan.Quotas = quotas
	return plan, nil
}

// invitationColumns 固定邀请列顺序。
const invitationColumns = "id, tenant_id, email, role, token_hash, status, invited_by, " +
	"accepted_by, expires_at, accepted_at, created_at, updated_at"

// CreateInvitation 插入邀请；pending 唯一冲突（生成列 uk_invitation_pending）
// 返回 ErrInvitationPendingExists，引导调用方走 resend。
func (r *MySQLRepository) CreateInvitation(ctx context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant_invitation (`+invitationColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		invitation.ID, invitation.TenantID, invitation.Email, string(invitation.Role),
		invitation.TokenHash, string(invitation.Status), invitation.InvitedBy,
		nullString(invitation.AcceptedBy), invitation.ExpiresAt, nullTime(invitation.AcceptedAt),
		invitation.CreatedAt, invitation.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err) {
			if strings.Contains(err.Error(), "uk_invitation_pending") {
				return fmt.Errorf("%w: %s", domain.ErrInvitationPendingExists, invitation.Email)
			}
			return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, invitation.ID)
		}
		return fmt.Errorf("tenant: create invitation %s: %w", invitation.ID, err)
	}
	return nil
}

// GetInvitation 按 (tenant, invitation) 读取。
func (r *MySQLRepository) GetInvitation(ctx context.Context, tenantID, invitationID string) (domain.Invitation, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+invitationColumns+` FROM tenant_invitation WHERE id = ? AND tenant_id = ?`,
		strings.TrimSpace(invitationID), strings.TrimSpace(tenantID))
	invitation, err := scanInvitation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Invitation{}, domain.ErrInvitationNotFound
		}
		return domain.Invitation{}, fmt.Errorf("tenant: get invitation %s: %w", invitationID, err)
	}
	return invitation, nil
}

// FindByTokenHash 按摘要查找邀请（接受入口；摘要唯一索引点查）。
func (r *MySQLRepository) FindByTokenHash(ctx context.Context, tokenHash string) (domain.Invitation, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+invitationColumns+` FROM tenant_invitation WHERE token_hash = ?`,
		strings.TrimSpace(tokenHash))
	invitation, err := scanInvitation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Invitation{}, domain.ErrInvitationNotFound
		}
		return domain.Invitation{}, fmt.Errorf("tenant: find invitation by token: %w", err)
	}
	return invitation, nil
}

// ListInvitations 分页列出邀请，创建时间倒序（与租户列表排序口径一致）。
func (r *MySQLRepository) ListInvitations(ctx context.Context, tenantID, status string, limit, offset int) ([]domain.Invitation, int, error) {
	where := " WHERE tenant_id = ?"
	args := []any{strings.TrimSpace(tenantID)}
	if status != "" {
		where += " AND status = ?"
		args = append(args, status)
	}

	var total int
	if err := r.query(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_invitation`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tenant: count invitations %s: %w", tenantID, err)
	}

	query := `SELECT ` + invitationColumns + ` FROM tenant_invitation` + where +
		` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := r.query(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("tenant: list invitations %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Invitation, 0, 8)
	for rows.Next() {
		invitation, scanErr := scanInvitation(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("tenant: scan invitation: %w", scanErr)
		}
		out = append(out, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tenant: iterate invitations: %w", err)
	}
	return out, total, nil
}

// CompleteInvitation 以 `WHERE id AND status='pending'` 的 CAS 写入终态。
//
// 0 行说明邀请已被并发处理（或不存在）：返回 ErrInvitationNotPending，
// 由应用层重读并按幂等 / 冲突分流（T-14：重复接受幂等）。
func (r *MySQLRepository) CompleteInvitation(ctx context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE tenant_invitation
		    SET status = ?, accepted_by = ?, accepted_at = ?, updated_at = ?
		  WHERE id = ? AND tenant_id = ? AND status = 'pending'`,
		string(invitation.Status), nullString(invitation.AcceptedBy), nullTime(invitation.AcceptedAt),
		invitation.UpdatedAt, invitation.ID, invitation.TenantID)
	if err != nil {
		return fmt.Errorf("tenant: complete invitation %s: %w", invitation.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant: complete invitation %s: %w", invitation.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", domain.ErrInvitationNotPending, invitation.ID)
	}
	return nil
}

// RefreshInvitation 重发：覆盖摘要、顺延过期；仅 pending 可刷新。
func (r *MySQLRepository) RefreshInvitation(ctx context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	result, err := r.exec(ctx).ExecContext(ctx,
		`UPDATE tenant_invitation
		    SET token_hash = ?, expires_at = ?, updated_at = ?
		  WHERE id = ? AND tenant_id = ? AND status = 'pending'`,
		invitation.TokenHash, invitation.ExpiresAt, invitation.UpdatedAt,
		invitation.ID, invitation.TenantID)
	if err != nil {
		return fmt.Errorf("tenant: refresh invitation %s: %w", invitation.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant: refresh invitation %s: %w", invitation.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", domain.ErrInvitationNotPending, invitation.ID)
	}
	return nil
}

// scanInvitation 按 invitationColumns 顺序扫描一行。
func scanInvitation(scanner rowScanner) (domain.Invitation, error) {
	var (
		invitation domain.Invitation
		role       string
		status     string
		acceptedBy sql.NullString
		acceptedAt sql.NullTime
	)
	if err := scanner.Scan(
		&invitation.ID, &invitation.TenantID, &invitation.Email, &role, &invitation.TokenHash,
		&status, &invitation.InvitedBy, &acceptedBy, &invitation.ExpiresAt,
		&acceptedAt, &invitation.CreatedAt, &invitation.UpdatedAt,
	); err != nil {
		return domain.Invitation{}, err
	}
	invitation.Role = domain.MemberRole(role)
	invitation.Status = domain.InvitationStatus(status)
	invitation.AcceptedBy = acceptedBy.String
	if acceptedAt.Valid {
		moment := acceptedAt.Time
		invitation.AcceptedAt = &moment
	}
	return invitation, nil
}
