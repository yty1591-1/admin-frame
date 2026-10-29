package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 默认保留期与清理批大小（IDN-P1-01）。
const (
	// DefaultSessionRetentionDays rotated / revoked 会话代的保留天数。
	DefaultSessionRetentionDays = 30
	// DefaultLoginAttemptRetentionDays 登录尝试记录保留天数。
	DefaultLoginAttemptRetentionDays = 180
	// DefaultCleanupBatchSize 单次清理的最大行数，避免长事务与锁等待。
	DefaultCleanupBatchSize = 500
)

// RetentionPolicy 是身份域数据保留策略。
type RetentionPolicy struct {
	// SessionRetention rotated / revoked / expired 会话与设备的保留时长。
	SessionRetention time.Duration
	// LoginAttemptRetention 登录尝试记录保留时长。
	LoginAttemptRetention time.Duration
	// BatchSize 单批上限；≤0 使用 DefaultCleanupBatchSize。
	BatchSize int
}

// CleanupStats 汇总一次清理的结果，供结构化日志与容量分析。
type CleanupStats struct {
	ExpiredSessions      int
	ExpiredFamilies      int
	DeletedSessions      int
	DeletedFamilies      int
	DeletedLoginAttempts int
}

// SessionCleanup 按保留期清理会话、设备与登录尝试（由 worker 周期调用）。
//
// 幂等：重复执行只处理下一个批次，不会重复计数已删除数据。
type SessionCleanup struct {
	sessions domain.SessionRepository
	attempts domain.LoginAttemptRepository
	policy   RetentionPolicy
	logger   *slog.Logger
}

// NewSessionCleanup 构造清理任务；仓储必填。
func NewSessionCleanup(
	sessions domain.SessionRepository,
	attempts domain.LoginAttemptRepository,
	policy RetentionPolicy,
	logger *slog.Logger,
) (*SessionCleanup, error) {
	if sessions == nil || attempts == nil {
		return nil, apperr.Internal("session cleanup repositories are required", nil)
	}
	if policy.SessionRetention <= 0 {
		policy.SessionRetention = DefaultSessionRetentionDays * 24 * time.Hour
	}
	if policy.LoginAttemptRetention <= 0 {
		policy.LoginAttemptRetention = DefaultLoginAttemptRetentionDays * 24 * time.Hour
	}
	if policy.BatchSize <= 0 {
		policy.BatchSize = DefaultCleanupBatchSize
	}
	return &SessionCleanup{sessions: sessions, attempts: attempts, policy: policy, logger: logger}, nil
}

// Run 执行一次清理：先标记过期，再按保留期删除，最后清理登录尝试。
func (c *SessionCleanup) Run(ctx context.Context, now time.Time) (CleanupStats, error) {
	var stats CleanupStats
	expired, err := c.sessions.ExpireStale(ctx, now.UTC(), c.policy.BatchSize)
	if err != nil {
		return stats, apperr.Internal("expire stale sessions failed", err)
	}
	stats.ExpiredSessions = expired.Sessions
	stats.ExpiredFamilies = expired.Families

	deleted, err := c.sessions.DeleteExpiredBefore(ctx, now.Add(-c.policy.SessionRetention).UTC(), c.policy.BatchSize)
	if err != nil {
		return stats, apperr.Internal("delete expired sessions failed", err)
	}
	stats.DeletedSessions = deleted.Sessions
	stats.DeletedFamilies = deleted.Families

	attempts, err := c.attempts.DeleteBefore(ctx, now.Add(-c.policy.LoginAttemptRetention).UTC(), c.policy.BatchSize)
	if err != nil {
		return stats, apperr.Internal("delete login attempts failed", err)
	}
	stats.DeletedLoginAttempts = attempts

	if c.logger != nil {
		c.logger.Info("identity cleanup completed",
			"expired_sessions", stats.ExpiredSessions,
			"expired_families", stats.ExpiredFamilies,
			"deleted_sessions", stats.DeletedSessions,
			"deleted_families", stats.DeletedFamilies,
			"deleted_login_attempts", stats.DeletedLoginAttempts,
		)
	}
	return stats, nil
}
