package cache

import stderrors "errors"

var (
	// ErrMiss 表示键不存在或已过期。
	ErrMiss = stderrors.New("cache: miss")
	// ErrNegative 表示命中空值占位（防穿透）；调用方勿再打真相源。
	ErrNegative = stderrors.New("cache: negative hit")
	// ErrInvalidConfig 表示参数不合法。
	ErrInvalidConfig = stderrors.New("cache: invalid config")
	// ErrClosed 表示缓存已关闭。
	ErrClosed = stderrors.New("cache: closed")
)
