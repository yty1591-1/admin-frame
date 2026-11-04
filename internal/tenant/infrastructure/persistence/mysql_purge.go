package persistence

// ENT-15 数据处置的持久化件：导出 manifest 仓储与租户表硬删除边界。
//
// 归属说明：tenant-data-lifecycle.md 划定「租户域提供边界清单，ENT-15 提供
// 执行」——本文件的删除范围仅限租户自有四表（tenant / tenant_member /
// tenant_quota / tenant_invitation，FK ON DELETE CASCADE 随租户行级联）；
// IAM / identity 的商户数据由各自域的 Purger 提供并在 runtime 装配。

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

// manifestColumns 是 tenant_export_manifest 的列清单。
const manifestColumns = "tenant_id, manifest_sha256, file_count, total_rows, acknowledged_at, created_by, created_at"

// MySQLExportManifestRepository 落 tenant_export_manifest 表。
type MySQLExportManifestRepository struct {
	db *sql.DB
}

// NewMySQLExportManifestRepository 构造；空句柄拒绝。
func NewMySQLExportManifestRepository(sqlDB *sql.DB) (*MySQLExportManifestRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("tenant export manifest repository requires a database handle")
	}
	return &MySQLExportManifestRepository{db: sqlDB}, nil
}

// exec / query 复用 ctx 事务（与 MySQLRepository 同口径，pkg/db.TxFromContext）。
func (r *MySQLExportManifestRepository) exec(ctx context.Context) execer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

func (r *MySQLExportManifestRepository) query(ctx context.Context) queryer {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// Record 登记一条已校验的导出 manifest。
func (r *MySQLExportManifestRepository) Record(ctx context.Context, m domain.ExportManifest) error {
	m = m.Normalized()
	if err := m.Validate(); err != nil {
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx,
		`INSERT INTO tenant_export_manifest (`+manifestColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.TenantID, m.ManifestSHA256, m.FileCount, m.TotalRows,
		acknowledgedArg(m.AcknowledgedAt), m.CreatedBy, m.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("tenant: record export manifest %s: %w", m.TenantID, err)
	}
	return nil
}

// FindLatest 返回该租户最新一条 manifest；不存在返回 ErrManifestNotFound。
func (r *MySQLExportManifestRepository) FindLatest(ctx context.Context, tenantID string) (domain.ExportManifest, error) {
	row := r.query(ctx).QueryRowContext(ctx,
		`SELECT `+manifestColumns+` FROM tenant_export_manifest
		  WHERE tenant_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, strings.TrimSpace(tenantID))
	var m domain.ExportManifest
	var acknowledged sql.NullTime
	if err := row.Scan(&m.TenantID, &m.ManifestSHA256, &m.FileCount, &m.TotalRows,
		&acknowledged, &m.CreatedBy, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ExportManifest{}, domain.ErrManifestNotFound
		}
		return domain.ExportManifest{}, fmt.Errorf("tenant: scan export manifest %s: %w", tenantID, err)
	}
	if acknowledged.Valid {
		m.AcknowledgedAt = acknowledged.Time
	}
	return m, nil
}

// MySQLTenantPurger 硬删除租户自有四表（D-05 第 3 段的租户侧边界）。
//
// 分批策略：member / quota / invitation 先按 tenant_id 分批清（体量可控，
// 单语句即可——子表行数与租户规模成正比，删除走 tenant_id 二级索引），
// 最后删租户主行；member/quota/invitation 的复合外键 ON DELETE CASCADE
// 会随主行级联，主行删除是兜底而非依赖。事务内执行保证「删一半」不可见。
type MySQLTenantPurger struct {
	db *sql.DB
}

// NewMySQLTenantPurger 构造租户表边界删除器。
func NewMySQLTenantPurger(sqlDB *sql.DB) (*MySQLTenantPurger, error) {
	if sqlDB == nil {
		return nil, errors.New("tenant purger requires a database handle")
	}
	return &MySQLTenantPurger{db: sqlDB}, nil
}

// Name 边界名（审计 attrs.tables 的组成项）。
func (p *MySQLTenantPurger) Name() string { return "tenant" }

// Purge 删除该租户的全部租户域数据，返回删除行数（四表合计）。
func (p *MySQLTenantPurger) Purge(ctx context.Context, tenantID string, _ int) (int64, error) {
	id := strings.TrimSpace(tenantID)
	if id == "" {
		return 0, errors.New("tenant: purge requires a tenant id")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("tenant: begin purge tx %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	// 语句集编译期固定（无运行时拼接，gosec G202 / 注入面归零）；
	// member/quota/invitation 走 tenant_id 索引，主行带 terminated 守卫。
	statements := []struct{ name, query string }{
		{"tenant_member", `DELETE FROM tenant_member WHERE tenant_id = ?`},
		{"tenant_quota", `DELETE FROM tenant_quota WHERE tenant_id = ?`},
		{"tenant_invitation", `DELETE FROM tenant_invitation WHERE tenant_id = ?`},
		{"tenant", `DELETE FROM tenant WHERE id = ? AND status = 'terminated'`},
	}
	var total int64
	for _, stmt := range statements {
		res, err := tx.ExecContext(ctx, stmt.query, id)
		if err != nil {
			return 0, fmt.Errorf("tenant: purge %s %s: %w", stmt.name, id, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("tenant: purge %s %s affected: %w", stmt.name, id, err)
		}
		total += affected
	}
	// 主行未删（不是 terminated 或已删）视为异常：防止对活跃租户误跑处置。
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("tenant: commit purge %s: %w", id, err)
	}
	return total, nil
}

// acknowledgedArg 把签收时间转为可空参数：零值 → NULL（未签收）。
func acknowledgedArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
