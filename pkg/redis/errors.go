package redis

import stderrors "errors"

var (
	// ErrInvalidConfig 表示连接或参数配置不合法。
	ErrInvalidConfig = stderrors.New("redis: invalid config")
	// ErrClosed 表示客户端已关闭。
	ErrClosed = stderrors.New("redis: closed")
	// ErrMiss 表示缓存键不存在。
	ErrMiss = stderrors.New("redis: cache miss")
	// ErrNotObtained 表示未能获取分布式锁（已被占用）。
	ErrNotObtained = stderrors.New("redis: lock not obtained")
	// ErrLockNotHeld 表示解锁/续期时锁已不属于当前持有者。
	ErrLockNotHeld = stderrors.New("redis: lock not held")
)
