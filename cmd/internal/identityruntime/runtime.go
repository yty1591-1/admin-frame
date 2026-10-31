// Package identityruntime 负责身份域在 admin-api / biz-api / c-api 进程内的统一装配。
//
// 装配顺序固定为：仓储 → 撤销存储 → 凭据 / 守卫 → 令牌签发 → 用例 → HTTP Handler。
// NewSessionValidator 与 Register 必须复用同一实例，否则撤销检查读取的账号状态
// 会与登录写入落在不同内存副本上（与 IAM 装配方式一致）。
//
// 本包位于 cmd/internal 下：只允许 cmd 下的进程入口引用，业务域不得依赖装配细节。
package identityruntime

import (
	"context"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	identityobs "github.com/admin-frame/admin-frame/internal/identity/observability"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/password"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// loginLimiterIdleTTL 是登录限流分桶的空闲回收阈值，避免唯一键无限增长。
const loginLimiterIdleTTL = 10 * time.Minute

// loginLimiterKeyPrefix 是 Redis 限流键的业务前缀（与平台级限流隔离）。
const loginLimiterKeyPrefix = "identity:login:"

// loginIPLimiterKeyPrefix 是 IP 级登录限流键前缀（PERF-05）。
const loginIPLimiterKeyPrefix = "identity:login:ip:"

// oauthLimiterKeyPrefix 是 OAuth authorize / callback 限流键前缀（IDN-P2-05）。
const oauthLimiterKeyPrefix = "identity:oauth:"

// refreshLimiterKeyPrefix 是 refresh / logout 端点限流键前缀（SEC-04）。
const refreshLimiterKeyPrefix = "identity:refresh:"

// Runtime 是身份域在本进程内的唯一实例集合。
type Runtime struct {
	// Checker 供 platform 注入 JWT 撤销检查；identity.enabled=false 时为 nil。
	Checker *app.RevocationChecker
	// Handler 供各端 transport 注册认证路由；identity.enabled=false 时为 nil。
	Handler *rest.Handler
	// CHandler 供 C 端注册短信 / OAuth 登录路由；非 C 端或未启用时为 nil。
	CHandler *identityc.Handler
}

// New 按配置装配身份域；identity.enabled=false 时返回空运行时（保持只验签）。
func New(deps *platform.Deps) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, apperr.Internal("identity runtime requires platform deps", nil)
	}
	cfg := deps.Config
	if !cfg.Identity.Enabled {
		return &Runtime{}, nil
	}
	if cfg.Channel != string(domain.ChannelAdmin) &&
		cfg.Channel != string(domain.ChannelBiz) &&
		cfg.Channel != string(domain.ChannelC) {
		// 配置校验已拒绝非 A/B/C 端启用，这里再兜底一次，防止绕过校验的装配路径。
		return nil, apperr.Internal("identity is only supported on admin/biz/c channel", nil)
	}
	channel := domain.Channel(cfg.Channel)

	piiHasher, err := buildHasher(cfg)
	if err != nil {
		return nil, err
	}
	// PII 主体保护器：未配置 identity.subject.keys 时返回 nil，保持纯明文路径。
	subjectProtector, err := buildSubjectProtector(cfg, piiHasher)
	if err != nil {
		return nil, err
	}
	if deps.Logger != nil && subjectProtector != nil {
		deps.Logger.Info("identity subject protection enabled",
			"stage", "S1/S2",
			"note", "明文列保留，写入 index/ciphertext；S4/S5 需单独发布",
		)
	}

	repos, err := buildRepositories(deps, subjectProtector)
	if err != nil {
		return nil, err
	}
	revocation, err := buildRevocationStore(deps)
	if err != nil {
		return nil, err
	}
	// MFA 仅 A 端实现（docs/identity-authn.md §7）：B / C 端不装配密文与票据，
	// 也不在运行时触发强制绑定，避免「配置写了 MFA、运行时不生效」的假安全。
	var (
		mfaCipher app.MFASecretCipher
		tickets   app.MFATicketStore
	)
	if channel == domain.ChannelAdmin {
		mfaCipher, err = buildMFACipher(cfg)
		if err != nil {
			return nil, err
		}
		tickets, err = buildMFATicketStore(deps)
		if err != nil {
			return nil, err
		}
	}

	hasher, err := password.New(password.Config{})
	if err != nil {
		return nil, fmt.Errorf("identity password hasher: %w", err)
	}
	passwordPolicy := passwordPolicyFromConfig(cfg)
	if err := passwordPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("identity password policy: %w", err)
	}
	// Argon2 memory-hard：限制同时校验数量，避免并发登录耗尽 CPU / 内存（IDN-P2-05）。
	maxConcurrent := cfg.Identity.Password.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 2 * goruntime.NumCPU()
	}
	if deps.Logger != nil {
		deps.Logger.Info("identity password concurrency",
			"max_concurrent", maxConcurrent,
			"wait_timeout", cfg.Identity.Password.WaitTimeout,
		)
	}
	passwordSvc, err := app.NewPasswordService(hasher, passwordPolicy,
		app.WithConcurrencyLimit(maxConcurrent, cfg.Identity.Password.WaitTimeout))
	if err != nil {
		return nil, fmt.Errorf("identity password service: %w", err)
	}

	limiter, err := buildLoginLimiter(cfg, deps)
	if err != nil {
		return nil, err
	}
	refreshLimiter, err := buildRefreshLimiter(cfg, deps)
	if err != nil {
		return nil, err
	}
	loginIPLimiter, err := buildLoginIPLimiter(cfg, deps)
	if err != nil {
		return nil, err
	}
	// 失败计数跨副本共享；Redis 缺失时守卫回退登录尝试表计数。
	var failureCounter app.LoginFailureCounter
	if deps.Redis != nil {
		failureCounter = identitycache.NewRedisLoginFailureCounter(deps.Redis.Cache())
	}
	guard, err := app.NewLoginGuard(
		repos.accounts,
		repos.attempts,
		deps.IDs,
		app.LockoutPolicy{
			MaxFailures: cfg.Identity.Lockout.MaxFailures,
			Window:      cfg.Identity.Lockout.Window,
			Duration:    cfg.Identity.Lockout.Duration,
		},
		app.WithHasher(piiHasher),
		app.WithLoginLimiter(limiter),
		app.WithIPRateLimiter(loginIPLimiter),
		app.WithLoginFailureCounter(failureCounter),
	)
	if err != nil {
		return nil, fmt.Errorf("identity login guard: %w", err)
	}

	signer, err := cfg.Auth.JWT.SigningManager(cfg.Channel)
	if err != nil {
		return nil, fmt.Errorf("identity signing manager: %w", err)
	}
	tokens, err := app.NewTokenService(
		signer,
		channel,
		cfg.Identity.AccessTTL,
		cfg.Identity.RefreshTTL,
		deps.IDs,
		app.WithTokenParser(signer),
	)
	if err != nil {
		return nil, fmt.Errorf("identity token service: %w", err)
	}

	// 核心指标与撤销快照失败指标共享同一 Registry；未配置 Metrics 时降级为无观测。
	var identityMetrics app.Metrics
	var revocationObserver app.RevocationObserver
	var revocationCacheObserver app.RevocationCacheObserver
	var identityObserver *identityobs.Metrics
	if deps.Metrics != nil {
		observer, obsErr := identityobs.NewMetrics(deps.Metrics.RegistererWithoutChannel())
		if obsErr != nil {
			return nil, obsErr
		}
		identityObserver = observer
		identityMetrics = observer
		revocationObserver = observer
		revocationCacheObserver = observer
		// PERF-08：活跃设备数按抓取时查询，作为 HPA / Dashboard 的辅助信号。
		if gaugeErr := observer.RegisterSessionGauge(func() (int64, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return repos.sessions.CountActiveFamilies(ctx, time.Now().UTC())
		}); gaugeErr != nil {
			return nil, fmt.Errorf("identity session gauge: %w", gaugeErr)
		}
	}

	// 撤销快照 L1：账号 / 会话快照先读进程内缓存，未命中再读 Redis（IDN-GAP-P1-02）。
	// 负 TTL 表示显式关闭；构造器在 ttl<=0 时原样返回 inner，不改变行为。
	revocation = identitycache.NewCachedRevocationStore(
		revocation,
		cfg.Identity.Revocation.LocalCacheTTL,
		cfg.Identity.Revocation.LocalCacheCapacity,
		revocationCacheObserver,
	)

	checker, err := app.NewRevocationChecker(
		repos.accounts,
		revocation,
		cfg.Identity.Revocation.FailureMode,
		cfg.Identity.Revocation.StateTTL,
		app.WithRevocationObserver(revocationObserver),
		app.WithRevocationCacheObserver(revocationCacheObserver),
		app.WithRevocationSessions(repos.sessions),
	)
	if err != nil {
		return nil, fmt.Errorf("identity revocation checker: %w", err)
	}

	mfaAdminRequired := cfg.Identity.MFA.AdminRequired
	if channel != domain.ChannelAdmin {
		mfaAdminRequired = false
	}
	if deps.Logger != nil {
		// 打印实际生效值，避免运维把「配置项存在」误认为「运行时已校验」。
		deps.Logger.Info("identity mfa policy",
			"channel", string(channel),
			"admin_required", mfaAdminRequired,
			"configured_admin_required", cfg.Identity.MFA.AdminRequired,
		)
	}

	// 审计出口：启用异步时写入 identity.audit Outbox（与业务写入同事务），
	// 内存模式下退化为进程日志；禁止静默丢弃。
	var auditEmitter audit.Emitter = audit.SlogEmitter{Log: deps.Logger}
	auditSink := "slog"
	if deps.Async != nil {
		auditEmitter = app.OutboxEmitter{Events: deps.Async.Store()}
		auditSink = "outbox"
		// OPS-04：identity.audit 积压与最老事件年龄必须可观测。
		if identityObserver != nil {
			store := deps.Async.Store()
			stats, ok := store.(outbox.TopicStatReader)
			if !ok {
				return nil, fmt.Errorf("identity async: outbox store does not support topic stats")
			}
			if gaugeErr := identityObserver.RegisterOutboxGauges(
				identityTopicStatsCounter(stats, app.TopicAudit),
			); gaugeErr != nil {
				return nil, fmt.Errorf("identity outbox gauges: %w", gaugeErr)
			}
		}
	}
	if deps.Logger != nil {
		// 启动日志标记审计出口，避免运维误判「审计已落库」。
		deps.Logger.Info("identity audit sink", "sink", auditSink)
	}
	var uow app.UnitOfWork = app.NoopUnitOfWork{}
	if deps.Database != nil {
		uow = persistence.NewSQLUnitOfWork(deps.Database.SQL())
	}

	authSvc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts:           repos.accounts,
		Credentials:        repos.credentials,
		Sessions:           repos.sessions,
		Guard:              guard,
		Password:           passwordSvc,
		Tokens:             tokens,
		Revocation:         revocation,
		RevocationStateTTL: cfg.Identity.Revocation.StateTTL,
		RevocationObserver: revocationObserver,
		Metrics:            identityMetrics,
		Hasher:             piiHasher,
		Logger:             deps.Logger,
		Audit:              auditEmitter,
		IDs:                deps.IDs,
		Channel:            channel,
		UoW:                uow,
		IdleTimeout:        cfg.Identity.Session.IdleTimeout,
		SessionAbsoluteTTL: cfg.Identity.Session.AbsoluteTTL,
		MaxPerAccount:      cfg.Identity.Session.MaxPerAccount,
		RefreshGrace:       cfg.Identity.Session.RefreshGrace,
		PasswordHistoryCount:  cfg.Identity.Password.HistoryCount,
		PasswordHistoryWindow: cfg.Identity.Password.HistoryWindow,
		Service:            cfg.Service,
		MFA:                repos.mfa,
		MFACipher:          mfaCipher,
		MFATickets:         tickets,
		MFAIssuer:          cfg.Identity.MFA.Issuer,
		MFAAdminRequired:   mfaAdminRequired,
	})
	if err != nil {
		return nil, fmt.Errorf("identity auth service: %w", err)
	}

	handler, err := rest.NewHandler(rest.HandlerConfig{
		Auth: authSvc, Channel: channel, ClientIP: deps.ClientIP, Hasher: piiHasher,
		EndpointLimiter: refreshLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("identity handler: %w", err)
	}

	runtime := &Runtime{Checker: checker, Handler: handler}
	if channel == domain.ChannelC {
		cHandler, cErr := buildChannelC(deps, cfg, repos.accounts, repos.oauthIdentities, guard,
			authSvc, piiHasher, identityMetrics, identityObserver, handler)
		if cErr != nil {
			return nil, cErr
		}
		runtime.CHandler = cHandler
	}

	registerHealth(deps)
	if deps.Async != nil {
		if err := RegisterAsync(deps); err != nil {
			return nil, fmt.Errorf("identity async handlers: %w", err)
		}
	}
	return runtime, nil
}

// RegisterAsync 注册身份域异步 topic handler（API 与 worker 共用）。
//
// 审计事件由本 handler 投递到独立审计表（存在持久化数据库时）并旁路结构化日志；
// 审计表写入失败按可重试错误处理，由 Relay 指数退避重试（event_id 幂等）。
func RegisterAsync(deps *platform.Deps) error {
	if deps == nil || deps.Async == nil {
		return nil
	}
	emitters := audit.MultiEmitter{}
	if deps.Database != nil {
		emitters = append(emitters, audit.SQLEmitter{DB: deps.Database.SQL()})
	}
	emitters = append(emitters, audit.SlogEmitter{Log: deps.Logger})
	if err := deps.Async.Register(app.TopicAudit, app.AuditEventHandler(emitters)); err != nil {
		return fmt.Errorf("identity async handler %s: %w", app.TopicAudit, err)
	}
	return nil
}

// identityTopicStatsCounter 采集 identity.audit 积压数与最老事件延迟（OPS-04）。
func identityTopicStatsCounter(reader outbox.TopicStatReader, topic string) func() (int, float64, error) {
	return func() (int, float64, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stats, err := reader.TopicStats(ctx, topic)
		if err != nil {
			return 0, 0, err
		}
		lag := 0.0
		if !stats.OldestCreatedAt.IsZero() {
			lag = time.Since(stats.OldestCreatedAt).Seconds()
			if lag < 0 {
				lag = 0
			}
		}
		return stats.Pending, lag, nil
	}
}

// cleanupBatchTimeout 是单轮清理的硬上限；停机时等待在途批次最多这么久。
const cleanupBatchTimeout = 2 * time.Minute

// CleanupHandle 持有清理循环的取消与完成信号，供优雅停机等待在途批次。
type CleanupHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Stop 停止调度并等待当前批次完成；调用方 ctx 超时表示停机窗口已用尽。
func (h *CleanupHandle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.cancel()
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartCleanup 启动身份域保留期清理任务（仅在 worker 调用）。
//
// worker 的 identity.enabled 必须保持 false（鉴权配置校验不允许 worker 启用身份域），
// 但清理仍必须运行：这里只以“存在持久化数据库”为开关，避免生产清理任务被静默跳过。
// 内存仓储（deps.Database=nil）不做清理：重启即丢数据，无需保留期。
//
// 返回句柄用于优雅停机：Stop 会先停止 ticker，再等待当前批次完成（REL-08）。
func StartCleanup(ctx context.Context, deps *platform.Deps) (*CleanupHandle, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	repos, err := buildRepositories(deps, nil)
	if err != nil {
		return nil, err
	}
	retention := deps.Config.Identity.Retention
	policy := app.RetentionPolicy{
		SessionRetention:      time.Duration(retention.SessionDays) * 24 * time.Hour,
		LoginAttemptRetention: time.Duration(retention.LoginAttemptDays) * 24 * time.Hour,
		ArchiveRetention:      time.Duration(retention.ArchiveDays) * 24 * time.Hour,
	}
	cleanup, err := app.NewSessionCleanup(repos.sessions, repos.attempts, policy, deps.Logger)
	if err != nil {
		return nil, err
	}
	interval := retention.CleanupInterval
	if interval <= 0 {
		interval = time.Hour
	}
	var observer app.CleanupObserver
	if deps.Metrics != nil {
		cleanupMetrics, metricsErr := identityobs.NewCleanupMetrics(deps.Metrics.RegistererWithoutChannel())
		if metricsErr != nil {
			return nil, metricsErr
		}
		observer = cleanupMetrics
	}
	// 生产多副本 worker 必须互斥；MySQL GET_LOCK 为连接级锁，进程退出自动释放。
	var locker app.CleanupLock
	if deps.Config.DB.Driver == "mysql" {
		mysqlLock, lockErr := persistence.NewMySQLCleanupLock(deps.Database.SQL(), "identity:cleanup")
		if lockErr != nil {
			return nil, lockErr
		}
		locker = mysqlLock
	} else if deps.Logger != nil {
		deps.Logger.Warn("identity cleanup runs without distributed lock", "db_driver", deps.Config.DB.Driver)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	handle := &CleanupHandle{cancel: cancel, done: make(chan struct{})}
	go runCleanupLoop(loopCtx, handle.done, cleanup, interval, deps.Logger, locker, observer)
	if deps.Logger != nil {
		deps.Logger.Info("identity cleanup started",
			"interval", interval.String(),
			"session_days", retention.SessionDays,
			"login_attempt_days", retention.LoginAttemptDays,
			"archive_days", retention.ArchiveDays,
			"distributed_lock", locker != nil,
		)
	}
	return handle, nil
}

// runCleanupLoop 立即执行一次清理后按间隔重复，直到进程退出。
//
// 停机时停止调度，但当前批次使用独立超时 context 继续完成（REL-08）；
// 批次结束后关闭 done，供 Stop 等待。
func runCleanupLoop(
	ctx context.Context,
	done chan struct{},
	cleanup *app.SessionCleanup,
	interval time.Duration,
	logger *slog.Logger,
	locker app.CleanupLock,
	observer app.CleanupObserver,
) {
	defer close(done)
	run := func() {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupBatchTimeout)
		defer cancel()
		runCleanupOnce(workCtx, cleanup, locker, observer, logger, time.Now().UTC())
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// runCleanupOnce 执行一轮清理：先抢分布式锁，未抢到则本轮跳过（REL-02）。
func runCleanupOnce(
	ctx context.Context,
	cleanup *app.SessionCleanup,
	locker app.CleanupLock,
	observer app.CleanupObserver,
	logger *slog.Logger,
	now time.Time,
) {
	started := time.Now()
	if locker != nil {
		release, acquired, err := locker.Acquire(ctx)
		if err != nil {
			result := app.CleanupRunResult{
				Duration:   time.Since(started),
				Err:        err,
				Skipped:    true,
				SkipReason: "lock_error",
			}
			if observer != nil {
				observer.OnCleanupRun(ctx, result)
			}
			if logger != nil {
				logger.Error("identity cleanup lock failed", "error", err)
			}
			return
		}
		if !acquired {
			result := app.CleanupRunResult{
				Duration:   time.Since(started),
				Skipped:    true,
				SkipReason: "lock_held",
			}
			if observer != nil {
				observer.OnCleanupRun(ctx, result)
			}
			if logger != nil {
				logger.Info("identity cleanup skipped", "reason", "lock_held")
			}
			return
		}
		defer func() {
			if release != nil {
				release(ctx)
			}
		}()
	}
	stats, err := cleanup.Run(ctx, now)
	result := app.CleanupRunResult{
		Stats:    stats,
		Duration: time.Since(started),
		Err:      err,
	}
	if observer != nil {
		observer.OnCleanupRun(ctx, result)
	}
	if err != nil {
		if logger != nil {
			logger.Error("identity cleanup failed", "error", err)
		}
		return
	}
	if stats.BatchLimitHit && logger != nil {
		logger.Warn("identity cleanup reached batch limit", "detail", "increase batch_size/max_batches_per_run or shorten interval")
	}
}

// repositories 汇总身份域六个仓储，避免装配函数返回过多位置参数。
type repositories struct {
	accounts        domain.AccountRepository
	credentials     domain.CredentialRepository
	sessions        domain.SessionRepository
	attempts        domain.LoginAttemptRepository
	mfa             domain.MFAEnrollmentRepository
	oauthIdentities domain.OAuthIdentityRepository
}

// buildRepositories 按 db.driver 选择内存或 MySQL 仓储。
//
// db.driver 非 memory 却没有数据库连接时直接失败，禁止静默回退内存仓储（IDN-P2-01）。
func buildRepositories(deps *platform.Deps, protector persistence.SubjectProtector) (repositories, error) {
	if deps.Database == nil {
		if deps.Config != nil && deps.Config.DB.Driver != "" && deps.Config.DB.Driver != "memory" {
			return repositories{}, fmt.Errorf(
				"identity repositories require db.driver=%s but no database connection is available",
				deps.Config.DB.Driver,
			)
		}
		return repositories{
			accounts:        persistence.NewMemoryAccountRepository(),
			credentials:     persistence.NewMemoryCredentialRepository(),
			sessions:        persistence.NewMemorySessionRepository(),
			attempts:        persistence.NewMemoryLoginAttemptRepository(),
			mfa:             persistence.NewMemoryMFAEnrollmentRepository(),
			oauthIdentities: persistence.NewMemoryOAuthIdentityRepository(),
		}, nil
	}
	sqlDB := deps.Database.SQL()
	return repositories{
		accounts:        persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(protector)),
		credentials:     persistence.NewSQLCredentialRepository(sqlDB),
		sessions:        persistence.NewSQLSessionRepository(sqlDB),
		attempts:        persistence.NewSQLLoginAttemptRepository(sqlDB),
		mfa:             persistence.NewSQLMFAEnrollmentRepository(sqlDB),
		oauthIdentities: persistence.NewSQLOAuthIdentityRepository(sqlDB,
			persistence.WithOAuthSubjectProtector(protector)),
	}, nil
}

// passwordPolicyFromConfig 将配置密码策略映射为应用层策略；零值字段保留默认值。
func passwordPolicyFromConfig(cfg *platform.Config) app.PasswordPolicy {
	policy := app.DefaultPasswordPolicy()
	if cfg.Identity.Password.MinLength > 0 {
		policy.MinLength = cfg.Identity.Password.MinLength
	}
	if cfg.Identity.Password.MaxLength > 0 {
		policy.MaxLength = cfg.Identity.Password.MaxLength
	}
	if cfg.Identity.Password.RequireComplexity != nil {
		policy.RequireComplexity = *cfg.Identity.Password.RequireComplexity
	}
	return policy
}

// buildHasher 解析 PII 摘要密钥；未配置时返回 nil（仅本地/tests 回退无盐算法）。
func buildHasher(cfg *platform.Config) (*crypto.Hasher, error) {
	active, keys, err := cfg.Identity.DecodeHashKeys()
	if err != nil {
		return nil, fmt.Errorf("identity hash keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	hasher, err := crypto.NewHasher(active, keys)
	if err != nil {
		return nil, fmt.Errorf("identity hasher: %w", err)
	}
	return hasher, nil
}

// buildSubjectProtector 构造 PII 主体保护器；未配置 subject 密钥环时返回 nil。
//
// subject_index 复用 identity.hash_key，subject_ciphertext 使用独立 PII 密钥环；
// 只配置加密密钥却缺少 hash_key 时直接失败，避免写出无法按索引查找的账号。
func buildSubjectProtector(cfg *platform.Config, hasher *crypto.Hasher) (persistence.SubjectProtector, error) {
	keys, err := cfg.Identity.Subject.DecodeKeys()
	if err != nil {
		return nil, fmt.Errorf("identity subject keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if hasher == nil {
		return nil, apperr.Internal("identity.hash_key is required when identity.subject.keys is set", nil)
	}
	legacy, err := cfg.Identity.Subject.DecodeLegacyKey()
	if err != nil {
		return nil, fmt.Errorf("identity subject legacy key: %w", err)
	}
	active := cfg.Identity.Subject.ActiveKeyID
	ring, err := crypto.NewKeyRing(keys, active, legacy)
	if err != nil {
		return nil, fmt.Errorf("identity subject key ring: %w", err)
	}
	return persistence.NewSubjectProtector(hasher, ring), nil
}

// buildMFACipher 解析 MFA 密钥环；未配置时返回 nil（MFA 端点失败关闭）。
//
// 仅配置 secret_key 时沿用单密钥；配置 keys 时启用按 key id 轮换，secret_key 作为旧密文回退。
func buildMFACipher(cfg *platform.Config) (app.MFASecretCipher, error) {
	legacyKey, err := cfg.Identity.MFA.DecodeSecretKey()
	if err != nil {
		return nil, fmt.Errorf("identity mfa secret key: %w", err)
	}
	keys, err := cfg.Identity.MFA.DecodeKeys()
	if err != nil {
		return nil, fmt.Errorf("identity mfa keys: %w", err)
	}
	if len(keys) == 0 && legacyKey == nil {
		if cfg.Identity.MFA.AdminRequired {
			return nil, apperr.Internal("identity.mfa.secret_key or identity.mfa.keys is required when admin_required=true", nil)
		}
		return nil, nil
	}
	if len(keys) > 0 {
		ring, ringErr := crypto.NewKeyRing(keys, cfg.Identity.MFA.ActiveKeyID, legacyKey)
		if ringErr != nil {
			return nil, fmt.Errorf("identity mfa key ring: %w", ringErr)
		}
		return ring, nil
	}
	aead, err := crypto.NewAES256GCM(legacyKey)
	if err != nil {
		return nil, fmt.Errorf("identity mfa cipher: %w", err)
	}
	return aead, nil
}

// buildMFATicketStore 构造两步登录票据存储。
//
// 票据必须跨副本原子消费；identity.revocation.driver=redis 已要求 Redis，缺失即装配失败。
func buildMFATicketStore(deps *platform.Deps) (app.MFATicketStore, error) {
	if deps.Redis == nil {
		return nil, apperr.Internal("identity mfa ticket store requires redis", nil)
	}
	return identitycache.NewRedisMFATicketStore(deps.Redis.Cache()), nil
}

// buildRevocationStore 构造撤销存储；当前配置仅允许 redis（跨副本生效）。
func buildRevocationStore(deps *platform.Deps) (app.RevocationStore, error) {
	cfg := deps.Config.Identity.Revocation
	switch cfg.Driver {
	case platform.IdentityRevocationDriverRedis:
		if deps.Redis == nil {
			return nil, apperr.Internal("identity revocation driver=redis but redis is not configured", nil)
		}
		return identitycache.NewRedisRevocationStore(deps.Redis.Cache()), nil
	default:
		return nil, apperr.Internal("unsupported identity revocation driver: "+cfg.Driver, nil)
	}
}

// buildLoginLimiter 构造账号级 / IP 级登录限流器。
//
// 有 Redis 时使用跨副本共享的令牌桶（生产 fail_closed）；未配置 Redis 时退化为进程内
// 实现，仅适用于单副本 / 本地调试（多副本会按副本数放大配额）。
func buildLoginLimiter(cfg *platform.Config, deps *platform.Deps) (app.LoginLimiter, error) {
	rate := cfg.Identity.LoginRateLimit
	return buildKeyedLimiter(deps, rate.RPS, rate.Burst, rate.FailureMode, loginLimiterKeyPrefix, "login")
}

// buildLoginIPLimiter 构造 IP 级登录限流器，使用独立配额（PERF-05）。
func buildLoginIPLimiter(cfg *platform.Config, deps *platform.Deps) (app.LoginLimiter, error) {
	rate := cfg.Identity.LoginRateLimit
	return buildKeyedLimiter(deps, rate.IPRPS, rate.IPBurst, rate.FailureMode, loginIPLimiterKeyPrefix, "login-ip")
}

// buildOAuthLimiter 构造 authorize / callback 的 IP 级与全局限流器（IDN-P2-05）。
func buildOAuthLimiter(cfg *platform.Config, deps *platform.Deps) (app.LoginLimiter, error) {
	rate := cfg.Identity.OAuth.RateLimit
	return buildKeyedLimiter(deps, rate.RPS, rate.Burst, rate.FailureMode, oauthLimiterKeyPrefix, "oauth")
}

// buildRefreshLimiter 构造 refresh / logout 的 IP + 主体双维度限流器（SEC-04）。
func buildRefreshLimiter(cfg *platform.Config, deps *platform.Deps) (app.LoginLimiter, error) {
	rate := cfg.Identity.RefreshRateLimit
	return buildKeyedLimiter(deps, rate.RPS, rate.Burst, rate.FailureMode, refreshLimiterKeyPrefix, "refresh")
}

// buildKeyedLimiter 按 Redis 可用性选择跨副本或进程内令牌桶。
func buildKeyedLimiter(
	deps *platform.Deps,
	rps float64,
	burst int,
	failureMode, keyPrefix, label string,
) (app.LoginLimiter, error) {
	var buckets appLoginBuckets
	if deps != nil && deps.Redis != nil {
		shared, err := ratelimit.NewRedisKeyed(deps.Redis, ratelimit.RedisKeyedConfig{
			RPS:         rps,
			Burst:       burst,
			IdleTTL:     loginLimiterIdleTTL,
			FailureMode: failureMode,
			KeyPrefix:   keyPrefix,
		})
		if err != nil {
			return nil, fmt.Errorf("identity %s redis limiter: %w", label, err)
		}
		buckets = shared
	} else {
		keyed, err := ratelimit.NewKeyed(
			func() (ratelimit.Limiter, error) {
				return ratelimit.NewTokenBucket(rps, burst)
			},
			ratelimit.WithIdleTTL(loginLimiterIdleTTL),
		)
		if err != nil {
			return nil, fmt.Errorf("identity %s limiter: %w", label, err)
		}
		buckets = keyed
	}
	return loginLimiter{buckets: buckets}, nil
}

// appLoginBuckets 是内存 Keyed 与 RedisKeyed 的共同能力。
type appLoginBuckets interface {
	Allow(key string) ratelimit.Result
}

// loginLimiter 把 pkg/ratelimit 适配为 app.LoginLimiter。
type loginLimiter struct {
	buckets appLoginBuckets
}

// Allow 尝试消耗一次配额，返回是否放行与建议等待时长。
func (l loginLimiter) Allow(key string) (bool, time.Duration) {
	result := l.buckets.Allow(key)
	return result.Allowed, result.RetryAfter
}

// registerHealth 把身份域真实外部依赖注册到 /readyz（OBS-03）。
//
// 只把数据库作为就绪硬依赖：Redis 是撤销加速层，故障时账号/会话快照回退 DB，
// 若把 Redis 也绑定到 readiness，共享 Redis 的一次抖动会让全部副本同时摘流，
// 把局部依赖故障放大为全站不可用。Redis 故障通过指标/告警与请求级 503 暴露。
//
// 内存仓储 / 未配置 DB 时不注册假检查，避免就绪探针永远成功。
func registerHealth(deps *platform.Deps) {
	if deps.Health == nil {
		return
	}
	if deps.Database != nil {
		deps.Health.Register("identity_db", deps.Database.Checker())
	}
}
