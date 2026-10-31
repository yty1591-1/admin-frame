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
	// DefaultCleanupBatchSize 单批清理的最大行数，避免长事务与锁等待。
	DefaultCleanupBatchSize = 1000
	// DefaultCleanupMaxBatchesPerRun 单轮清理最多连续执行的批次数，防止单次任务无限占用连接。
	// 1000 × 50 = 50,000 行/轮；按清理间隔可估算日吞吐，生产应按登录量调大。
	DefaultCleanupMaxBatchesPerRun = 50
	// DefaultSessionArchiveRetentionDays 归档表保留天数；到期后按批删除归档。
	DefaultSessionArchiveRetentionDays = 365
)

// RetentionPolicy 是身份域数据保留策略。
type RetentionPolicy struct {
	// SessionRetention rotated / revoked / expired 会话与设备的保留时长。
	SessionRetention time.Duration
	// LoginAttemptRetention 登录尝试记录保留时长。
	LoginAttemptRetention time.Duration
	// ArchiveRetention 归档表保留时长；≤0 表示关闭归档（回到只删不归档，用于回滚）。
	ArchiveRetention time.Duration
	// BatchSize 单批上限；≤0 使用 DefaultCleanupBatchSize。
	BatchSize int
	// MaxBatchesPerRun 单轮最多连续执行的批次数；≤0 使用 DefaultCleanupMaxBatchesPerRun。
	MaxBatchesPerRun int
}

// CleanupStats 汇总一次清理的结果，供结构化日志与容量分析。
type CleanupStats struct {
	ExpiredSessions        int
	ExpiredFamilies        int
	DeletedSessions        int
	DeletedFamilies        int
	ArchivedSessions       int
	ArchivedFamilies       int
	DeletedArchiveSessions int
	DeletedArchiveFamilies int
	DeletedLoginAttempts   int
	// BatchLimitHit 表示本轮至少有一类清理达到了 MaxBatchesPerRun 仍未清空。
	//
	// 为 true 说明单轮吞吐不足，需要上调批大小/上限或缩短清理间隔（OPS-02）。
	BatchLimitHit bool
}

// CleanupRunResult 是一次清理任务的执行结果，供指标与告警消费。
type CleanupRunResult struct {
	// Stats 本轮清理统计；失败时为已完成部分。
	Stats CleanupStats
	// Duration 本轮实际耗时。
	Duration time.Duration
	// Err 本轮失败原因；nil 表示成功。
	Err error
	// Skipped 表示本轮因未获得分布式锁而跳过。
	Skipped bool
	// SkipReason 跳过原因：lock_held | lock_error。
	SkipReason string
}

// CleanupObserver 接收清理任务结果；未注入时为 no-op。
type CleanupObserver interface {
	OnCleanupRun(ctx context.Context, result CleanupRunResult)
}

// CleanupLock 是清理任务的分布式互斥端口。
//
// Acquire 返回 release 与是否获得锁；acquired=false 表示其他副本正在执行。
// 实现须保证进程崩溃后锁自动释放（租约 / 连接级锁）。
type CleanupLock interface {
	Acquire(ctx context.Context) (release func(context.Context), acquired bool, err error)
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
	// 零值使用默认 365 天；显式负值表示关闭归档（保留「只删不归档」的回滚路径）。
	if policy.ArchiveRetention == 0 {
		policy.ArchiveRetention = DefaultSessionArchiveRetentionDays * 24 * time.Hour
	}
	if policy.BatchSize <= 0 {
		policy.BatchSize = DefaultCleanupBatchSize
	}
	if policy.MaxBatchesPerRun <= 0 {
		policy.MaxBatchesPerRun = DefaultCleanupMaxBatchesPerRun
	}
	return &SessionCleanup{sessions: sessions, attempts: attempts, policy: policy, logger: logger}, nil
}

// Run 执行一次清理：先标记过期，再按保留期删除，最后清理登录尝试。
//
// 每一类操作都按批循环到「本批未满」或达到 MaxBatchesPerRun，避免固定单批
// 在登录量上升后永久积压；批大小与循环上限由 RetentionPolicy 控制。
func (c *SessionCleanup) Run(ctx context.Context, now time.Time) (CleanupStats, error) {
	var stats CleanupStats
	for batch := 0; batch < c.policy.MaxBatchesPerRun; batch++ {
		expired, err := c.sessions.ExpireStale(ctx, now.UTC(), c.policy.BatchSize)
		if err != nil {
			return stats, apperr.Internal("expire stale sessions failed", err)
		}
		stats.ExpiredSessions += expired.Sessions
		stats.ExpiredFamilies += expired.Families
		if expired.Sessions < c.policy.BatchSize && expired.Families < c.policy.BatchSize {
			break
		}
		if batch == c.policy.MaxBatchesPerRun-1 {
			stats.BatchLimitHit = true
		}
	}

	// 会话保留期处理：默认归档 + 删除同事务；ArchiveRetention<=0 时回退到只删不归档。
	if c.policy.ArchiveRetention > 0 {
		cutoff := now.Add(-c.policy.SessionRetention).UTC()
		archivedAt := now.UTC()
		for batch := 0; batch < c.policy.MaxBatchesPerRun; batch++ {
			archived, err := c.sessions.ArchiveExpiredBefore(ctx, cutoff, c.policy.BatchSize, archivedAt)
			if err != nil {
				return stats, apperr.Internal("archive expired sessions failed", err)
			}
			stats.ArchivedSessions += archived.Sessions
			stats.ArchivedFamilies += archived.Families
			if archived.Sessions < c.policy.BatchSize && archived.Families < c.policy.BatchSize {
				break
			}
			if batch == c.policy.MaxBatchesPerRun-1 {
				stats.BatchLimitHit = true
			}
		}
		archiveCutoff := now.Add(-c.policy.ArchiveRetention).UTC()
		for batch := 0; batch < c.policy.MaxBatchesPerRun; batch++ {
			removed, err := c.sessions.DeleteArchivedBefore(ctx, archiveCutoff, c.policy.BatchSize)
			if err != nil {
				return stats, apperr.Internal("delete archived sessions failed", err)
			}
			stats.DeletedArchiveSessions += removed.Sessions
			stats.DeletedArchiveFamilies += removed.Families
			if removed.Sessions < c.policy.BatchSize && removed.Families < c.policy.BatchSize {
				break
			}
			if batch == c.policy.MaxBatchesPerRun-1 {
				stats.BatchLimitHit = true
			}
		}
	} else {
		for batch := 0; batch < c.policy.MaxBatchesPerRun; batch++ {
			deleted, err := c.sessions.DeleteExpiredBefore(ctx, now.Add(-c.policy.SessionRetention).UTC(), c.policy.BatchSize)
			if err != nil {
				return stats, apperr.Internal("delete expired sessions failed", err)
			}
			stats.DeletedSessions += deleted.Sessions
			stats.DeletedFamilies += deleted.Families
			if deleted.Sessions < c.policy.BatchSize && deleted.Families < c.policy.BatchSize {
				break
			}
			if batch == c.policy.MaxBatchesPerRun-1 {
				stats.BatchLimitHit = true
			}
		}
	}

	for batch := 0; batch < c.policy.MaxBatchesPerRun; batch++ {
		attempts, err := c.attempts.DeleteBefore(ctx, now.Add(-c.policy.LoginAttemptRetention).UTC(), c.policy.BatchSize)
		if err != nil {
			return stats, apperr.Internal("delete login attempts failed", err)
		}
		stats.DeletedLoginAttempts += attempts
		if attempts < c.policy.BatchSize {
			break
		}
		if batch == c.policy.MaxBatchesPerRun-1 {
			stats.BatchLimitHit = true
		}
	}

	if c.logger != nil {
		c.logger.Info("identity cleanup completed",
			"expired_sessions", stats.ExpiredSessions,
			"expired_families", stats.ExpiredFamilies,
			"deleted_sessions", stats.DeletedSessions,
			"deleted_families", stats.DeletedFamilies,
			"archived_sessions", stats.ArchivedSessions,
			"archived_families", stats.ArchivedFamilies,
			"deleted_archived_sessions", stats.DeletedArchiveSessions,
			"deleted_archived_families", stats.DeletedArchiveFamilies,
			"deleted_login_attempts", stats.DeletedLoginAttempts,
			"batch_limit_hit", stats.BatchLimitHit,
		)
	}
	return stats, nil
}
