package persistence

// ENT-15 商户数据硬删除边界（D-05 级联任务）。
//
// 删除范围：tenant 终止后按 merchant_id 清理本域商户级数据——角色模板绑定
// （iam_role_binding）、商户角色（iam_role，权限/数据范围随复合外键级联）、
// 变更单（iam_change_request）。审计事件流不在本边界（保留期独立，处置
// 本身必须有据可查——tenant-data-lifecycle.md §各域数据与硬删除边界）。
//
// 层级说明：iam_role 的 parent 自引用复合外键使多行删除顺序不可控，
// 统一在事务内临时关闭会话级 FOREIGN_KEY_CHECKS（作用域仅限本连接 +
// 本事务，提交即恢复）——这是层级数据批量清除的标准做法。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MySQLMerchantPurger 清理 IAM 域的商户级数据。
type MySQLMerchantPurger struct {
	db *sql.DB
}

// NewMySQLMerchantPurger 构造；空句柄拒绝。
func NewMySQLMerchantPurger(sqlDB *sql.DB) (*MySQLMerchantPurger, error) {
	if sqlDB == nil {
		return nil, errors.New("iam merchant purger requires a database handle")
	}
	return &MySQLMerchantPurger{db: sqlDB}, nil
}

// Name 边界名。
func (p *MySQLMerchantPurger) Name() string { return "iam" }

// Purge 删除该商户的 IAM 数据，返回删除行数。幂等：重跑只删剩余行。
func (p *MySQLMerchantPurger) Purge(ctx context.Context, merchantID string, _ int) (int64, error) {
	id := strings.TrimSpace(merchantID)
	if id == "" {
		return 0, errors.New("iam: purge requires a merchant id")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("iam: begin purge tx %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	// 会话级关闭外键检查（仅本事务）：iam_role 的自引用复合外键
	// （parent_id, channel, merchant_id）使批量删除顺序不可静态确定。
	if _, err := tx.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		return 0, fmt.Errorf("iam: disable fk checks: %w", err)
	}
	defer func() { _, _ = tx.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 1`) }()

	var total int64
	// 顺序：绑定 → 授权关系/数据范围（经商户角色 join，两表无 merchant_id
	// 列且外键 RESTRICT）→ 变更单 → 商户角色（模板与权限点是平台级，不动）。
	statements := []struct{ name, query string }{
		{"iam_role_binding", `DELETE FROM iam_role_binding WHERE merchant_id = ? AND merchant_id != ''`},
		{"iam_role_permission", `DELETE rp FROM iam_role_permission rp
			 JOIN iam_role r ON rp.role_id = r.id
			 WHERE r.merchant_id = ? AND r.merchant_id != ''`},
		{"iam_role_data_scope", `DELETE ds FROM iam_role_data_scope ds
			 JOIN iam_role r ON ds.role_id = r.id AND ds.channel = r.channel
			 WHERE r.merchant_id = ? AND r.merchant_id != ''`},
		{"iam_change_request", `DELETE FROM iam_change_request WHERE merchant_id = ? AND merchant_id != ''`},
		{"iam_role", `DELETE FROM iam_role WHERE merchant_id = ? AND merchant_id != ''`},
	}
	for _, stmt := range statements {
		res, err := tx.ExecContext(ctx, stmt.query, id)
		if err != nil {
			return 0, fmt.Errorf("iam: purge %s %s: %w", stmt.name, id, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("iam: purge %s %s affected: %w", stmt.name, id, err)
		}
		total += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("iam: commit purge %s: %w", id, err)
	}
	return total, nil
}
