package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
)

// acquireLock 获取迁移咨询锁并返回释放函数。
//
// PostgreSQL 使用会话级 pg_advisory_lock，MySQL 使用 GET_LOCK；锁绑定在独占连接上，
// 释放函数同时关闭该连接。其他方言返回空释放函数（无锁），须由部署侧保证单实例执行。
func (r *Runner) acquireLock(ctx context.Context) (func(context.Context) error, error) {
	switch r.dialect {
	case DialectPostgres:
		return r.acquirePostgresLock(ctx)
	case DialectMySQL:
		return r.acquireMySQLLock(ctx)
	default:
		return func(context.Context) error { return nil }, nil
	}
}

func (r *Runner) acquirePostgresLock(ctx context.Context) (func(context.Context) error, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire postgres lock connection: %w", err)
	}
	key := r.lockKey()

	lockCtx, cancel := r.lockContext(ctx)
	defer cancel()
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", key); err != nil {
		closeConn(conn)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%w: postgres advisory lock %d", ErrLockTimeout, key)
		}
		return nil, fmt.Errorf("migrate: postgres advisory lock: %w", err)
	}

	return func(releaseCtx context.Context) error {
		_, unlockErr := conn.ExecContext(releaseCtx, "SELECT pg_advisory_unlock($1)", key)
		closeErr := conn.Close()
		if unlockErr != nil {
			return fmt.Errorf("migrate: postgres advisory unlock: %w", unlockErr)
		}
		return closeErr
	}, nil
}

func (r *Runner) acquireMySQLLock(ctx context.Context) (func(context.Context) error, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire mysql lock connection: %w", err)
	}
	name := r.lockName()
	seconds := -1 // MySQL：负值表示无限等待
	if r.cfg.LockTimeout > 0 {
		seconds = int(math.Ceil(r.cfg.LockTimeout.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
	}

	lockCtx, cancel := r.lockContext(ctx)
	defer cancel()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, ?)", name, seconds).Scan(&acquired); err != nil {
		closeConn(conn)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%w: mysql GET_LOCK %s", ErrLockTimeout, name)
		}
		return nil, fmt.Errorf("migrate: mysql GET_LOCK: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		closeConn(conn)
		return nil, fmt.Errorf("%w: mysql GET_LOCK %s not acquired", ErrLockTimeout, name)
	}

	return func(releaseCtx context.Context) error {
		var released sql.NullInt64
		releaseErr := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", name).Scan(&released)
		if releaseErr == nil && (!released.Valid || released.Int64 != 1) {
			releaseErr = fmt.Errorf("%w: mysql RELEASE_LOCK %s", ErrLockTimeout, name)
		}
		closeErr := conn.Close()
		if releaseErr != nil {
			return fmt.Errorf("migrate: mysql release lock: %w", releaseErr)
		}
		return closeErr
	}, nil
}

// lockContext 为获取锁附加 LockTimeout；未配置时沿用调用方 ctx。
func (r *Runner) lockContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.cfg.LockTimeout > 0 {
		return context.WithTimeout(ctx, r.cfg.LockTimeout)
	}
	return context.WithCancel(ctx)
}

// lockKey 由 schema.table 派生稳定的 64 位咨询锁键（跨进程一致）。
func (r *Runner) lockKey() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.cfg.Schema + "." + r.cfg.Table)) // hash.Hash.Write 不返回错误
	//nolint:gosec // 锁键使用 FNV-1a 的完整 64 位，符号位不影响 advisory lock 唯一性。
	return int64(h.Sum64())
}

// lockName 派生 MySQL 命名锁（<= 64 字符）。
func (r *Runner) lockName() string {
	//nolint:gosec // 与 lockKey 使用同一 64 位哈希的十六进制表示，仅用于命名锁。
	return fmt.Sprintf("admin_frame_migrate_%016x", uint64(r.lockKey()))
}

// closeConn 忽略关闭错误：连接关闭失败时锁会随数据库会话终止自动释放。
func closeConn(conn *sql.Conn) {
	_ = conn.Close()
}
