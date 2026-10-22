package persistence

import (
	"context"
	"database/sql"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// resolveExecutor 解析当前请求应使用的 SQL 执行器。
//
// 契约（docs/rbac-iam.md M4.5）：平台与用例把 *sql.Tx 放进 context，
// 仓储一律通过本函数取执行器，从而让「业务写入 + 审计 Outbox」共享同一事务。
// 未开启事务时返回连接池，行为与改造前一致。
func resolveExecutor(ctx context.Context, sqlDB *sql.DB) db.Querier {
	if tx, ok := db.TxFromContext(ctx); ok {
		return tx
	}
	return sqlDB
}

// SQLUnitOfWork 是基于 *sql.DB 的事务边界实现（app.UnitOfWork）。
//
// 只做 begin / commit / rollback 与 context 注入；业务语义（哪些写入必须原子）
// 由用例决定。嵌套调用会复用外层事务，不会各自提交。
type SQLUnitOfWork struct {
	sql *sql.DB
}

// NewSQLUnitOfWork 基于已打开的 *sql.DB 创建事务边界。
func NewSQLUnitOfWork(sqlDB *sql.DB) *SQLUnitOfWork {
	return &SQLUnitOfWork{sql: sqlDB}
}

// WithinTx 在事务内执行 fn；已在事务中时直接复用。
func (u *SQLUnitOfWork) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if u == nil || u.sql == nil {
		return apperr.Unavailable("iam unit of work is not configured")
	}
	return db.WithinUnitOfWork(ctx, u.sql, nil, fn)
}
