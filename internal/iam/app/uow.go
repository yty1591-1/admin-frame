package app

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// UnitOfWork 把一个用例的多次写入合并到同一个数据库事务。
//
// 为什么需要它：审计事件在 M4.4 已持久化到 iam_outbox，但「业务写 + 审计写」
// 若分两次提交，进程在两次提交之间崩溃就会出现「变更已生效、审计缺失」。
// 由用例把 Save + Record 包进 WithinTx，仓储从 ctx 取出事务复用，
// 才能在数据库层保证原子（docs/rbac-iam.md D9 / M4.5）。
//
// 实现约束：
//   - 已在事务中时必须复用（不开启嵌套事务）；
//   - fn 返回错误必须整体回滚，并把原始错误（含业务码）原样返回；
//   - 不得吞掉 fn 的错误，也不得把回滚错误覆盖业务错误。
type UnitOfWork interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// NoopUnitOfWork 直接执行 fn，不提供事务语义。
//
// 仅用于内存仓储（local / demo / 单测）：内存实现无法回滚，此处显式声明
// 「无原子性保证」，避免让调用方误以为已经具备事务保护。
type NoopUnitOfWork struct{}

// WithinTx 直接调用 fn。
func (NoopUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return apperr.Internal("unit of work func is required", nil)
	}
	return fn(ctx)
}

// 编译期断言。
var _ UnitOfWork = NoopUnitOfWork{}
