package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// cleanupLockReleaseTimeout 是释放清理锁的超时，避免停机时卡在已取消的请求 ctx。
const cleanupLockReleaseTimeout = 2 * time.Second

// MySQLCleanupLock 使用 MySQL 连接级 advisory lock 实现清理任务互斥。
//
// GET_LOCK 的作用域是单个连接：锁由专用 *sql.Conn 持有，临界区结束显式
// RELEASE_LOCK 并关闭连接；进程崩溃时连接断开，MySQL 自动释放锁。
type MySQLCleanupLock struct {
	db   *sql.DB
	name string
}

// 编译期断言。
var _ app.CleanupLock = (*MySQLCleanupLock)(nil)

// NewMySQLCleanupLock 创建清理锁；name 必须非空且不超过 MySQL 64 字符上限。
func NewMySQLCleanupLock(sqlDB *sql.DB, name string) (*MySQLCleanupLock, error) {
	if sqlDB == nil {
		return nil, apperr.Internal("identity cleanup lock: database is required", nil)
	}
	if name == "" {
		return nil, apperr.BadRequest("identity cleanup lock: name is required")
	}
	if len(name) > 64 {
		return nil, apperr.BadRequest("identity cleanup lock: name exceeds 64 bytes")
	}
	return &MySQLCleanupLock{db: sqlDB, name: name}, nil
}

// Acquire 尝试获取锁；已被其他副本持有时返回 acquired=false。
func (l *MySQLCleanupLock) Acquire(ctx context.Context) (releaseFn func(context.Context), acquired bool, err error) {
	if l == nil || l.db == nil {
		return nil, false, apperr.Unavailable("identity cleanup lock is not configured")
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("identity cleanup lock: acquire connection: %w", err)
	}
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, l.name).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("identity cleanup lock: get_lock: %w", err)
	}
	if !got.Valid || got.Int64 != 1 {
		// 未获得锁或 MySQL 返回 NULL：关闭专用连接，避免占用池。
		_ = conn.Close()
		return nil, false, nil
	}
	release := func(context.Context) {
		// 使用独立超时：调用方 ctx 可能已因停机取消，但释放锁仍需尽力完成。
		releaseCtx, cancel := context.WithTimeout(context.Background(), cleanupLockReleaseTimeout)
		defer cancel()
		var released sql.NullInt64
		_ = conn.QueryRowContext(releaseCtx, `SELECT RELEASE_LOCK(?)`, l.name).Scan(&released)
		// 关闭连接同样会释放连接级 advisory lock，作为释放失败时的兜底。
		_ = conn.Close()
	}
	return release, true, nil
}
