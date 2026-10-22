package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// txContextKey 是 *sql.Tx 在 context 中的键。
//
// 使用未导出的空结构体：外部包无法伪造键，只能通过 WithTx 写入。
type txContextKey struct{}

// TxFromContext 取出当前请求已开启的事务。
//
// 仓储据此在「事务内」复用调用方的 *sql.Tx，实现一个请求只有一次提交 / 回滚：
// 业务写入与审计 Outbox 要么同时可见，要么同时不可见。
func TxFromContext(ctx context.Context) (*sql.Tx, bool) {
	if ctx == nil {
		return nil, false
	}
	tx, ok := ctx.Value(txContextKey{}).(*sql.Tx)
	return tx, ok && tx != nil
}

// WithTx 返回携带事务的 context；显式传 nil 时视为清除事务。
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil {
		return context.WithValue(ctx, txContextKey{}, (*sql.Tx)(nil))
	}
	return context.WithValue(ctx, txContextKey{}, tx)
}

// WithinUnitOfWork 在一个事务内执行 fn，并把事务注入 ctx。
//
// 与 WithinTx 的差异：
//   - fn 接收带事务的 ctx，调用链上的仓储自动复用该事务（无需层层传 *sql.Tx）；
//   - ctx 已处于事务中时直接复用，不开启嵌套事务（MySQL 不支持真正的嵌套事务，
//     重复 Begin 会各自提交，破坏原子性）。
//
// 语义与 WithinTx 一致：fn 返回错误或 panic 即回滚，且错误保留 ErrTxFailed 因果链。
func WithinUnitOfWork(ctx context.Context, sqlDB *sql.DB, opts *sql.TxOptions, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil unit of work func", ErrInvalidConfig)
	}
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx)
	}
	if sqlDB == nil {
		return fmt.Errorf("%w: nil *sql.DB", ErrInvalidConfig)
	}
	return WithinTx(ctx, sqlDB, opts, func(tx *sql.Tx) error {
		return fn(WithTx(ctx, tx))
	})
}

// IsTxFailed 报告错误是否来自事务回调失败（含回滚）。
func IsTxFailed(err error) bool {
	return errors.Is(err, ErrTxFailed)
}
