package db

import stderrors "errors"

var (
	// ErrInvalidConfig 表示连接配置不合法。
	ErrInvalidConfig = stderrors.New("db: invalid config")
	// ErrClosed 表示连接池已关闭。
	ErrClosed = stderrors.New("db: closed")
	// ErrTxFailed 表示事务回调失败（已 rollback）；可用 errors.Unwrap 取根因。
	ErrTxFailed = stderrors.New("db: tx failed")
)
