package db

import (
	"context"
	"database/sql"
	"fmt"
)

// TxFunc 在事务内执行的业务回调；返回错误则 rollback。
type TxFunc func(tx *sql.Tx) error

// WithinTx 在 db 上开启事务，执行 fn：
//   - fn 返回 nil → Commit
//   - fn 返回错误或 panic → Rollback，并将错误（或 panic 转 error）返回
//
// opts 可为 nil（默认隔离级别）。必须尊重 ctx 取消。
func WithinTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions, fn TxFunc) (err error) {
	if db == nil {
		return fmt.Errorf("%w: nil *sql.DB", ErrInvalidConfig)
	}
	if fn == nil {
		return fmt.Errorf("%w: nil TxFunc", ErrInvalidConfig)
	}

	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// rollback 错误不覆盖业务错误；panic 路径同样尽量回滚。
		_ = tx.Rollback()
	}()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: panic: %v", ErrTxFailed, r)
		}
	}()

	if err = fn(tx); err != nil {
		return fmt.Errorf("%w: %w", ErrTxFailed, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	committed = true
	return nil
}

// WithinTxDB 是 WithinTx 的 *DB 便捷封装。
func WithinTxDB(ctx context.Context, d *DB, opts *sql.TxOptions, fn TxFunc) error {
	if d == nil || d.sql == nil || d.closed.Load() {
		return ErrClosed
	}
	return WithinTx(ctx, d.sql, opts, fn)
}
