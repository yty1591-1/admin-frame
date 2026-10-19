package db

import (
	"context"
	"database/sql"
)

// Querier 抽象 *sql.DB 与 *sql.Tx 的公共查询面，便于仓储在事务内外复用。
//
// 与 sqlc 生成的 DBTX 语义一致；domain / app 层仍不得直接依赖本接口以外的 SQL 细节。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// 编译期断言：标准库类型满足 Querier。
var (
	_ Querier = (*sql.DB)(nil)
	_ Querier = (*sql.Tx)(nil)
)
