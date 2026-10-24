package app

import (
	"context"
)

// UnitOfWork 是身份域的事务边界（复用 IAM 的 UnitOfWork 语义）。
//
// 契约：
//   - fn 内通过仓储的写操作与审计 Outbox 写入共享同一事务；
//   - fn 返回错误时整体回滚，返回 nil 时提交；
//   - 嵌套调用复用外层事务，不会各自提交。
type UnitOfWork interface {
	// WithinTx 在事务内执行 fn。
	WithinTx(ctx context.Context, fn func(context.Context) error) error
}

// NoopUnitOfWork 是不提供事务的实现，用于内存仓储与单测。
//
// 内存仓储无法回滚，允许退化为「直接执行」；该退化仅限 db.driver=memory 的
// local / test 环境，生产必须使用 persistence.SQLUnitOfWork。
type NoopUnitOfWork struct{}

// WithinTx 直接执行 fn。
func (NoopUnitOfWork) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
