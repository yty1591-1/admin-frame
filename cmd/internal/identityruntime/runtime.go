// Package identityruntime 负责身份域在 admin-api / biz-api 进程内的统一装配。
//
// 装配顺序固定为：仓储 → 撤销存储 → 凭据 / 守卫 → 令牌签发 → 用例 → HTTP Handler。
// NewSessionValidator 与 Register 必须复用同一实例，否则撤销检查读取的账号状态
// 会与登录写入落在不同内存副本上（与 IAM 装配方式一致）。
//
// 本包位于 cmd/internal 下：只允许 cmd 下的进程入口引用，业务域不得依赖装配细节。
package identityruntime

import (
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/password"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// loginLimiterIdleTTL 是登录限流分桶的空闲回收阈值，避免唯一键无限增长。
const loginLimiterIdleTTL = 10 * time.Minute

// Runtime 是身份域在本进程内的唯一实例集合。
type Runtime struct {
	// Checker 供 platform 注入 JWT 撤销检查；identity.enabled=false 时为 nil。
	Checker *app.RevocationChecker
	// Handler 供各端 transport 注册认证路由；identity.enabled=false 时为 nil。
	Handler *rest.Handler
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
	if cfg.Channel != string(domain.ChannelAdmin) && cfg.Channel != string(domain.ChannelBiz) {
		// 配置校验已拒绝非 A/B 端启用，这里再兜底一次，防止绕过校验的装配路径。
		return nil, apperr.Internal("identity is only supported on admin/biz channel", nil)
	}
	channel := domain.Channel(cfg.Channel)

	accounts, credentials, sessions, attempts, mfa, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
	revocation, err := buildRevocationStore(deps)
	if err != nil {
		return nil, err
	}
	mfaCipher, err := buildMFACipher(cfg)
	if err != nil {
		return nil, err
	}
	tickets, err := buildMFATicketStore(deps)
	if err != nil {
		return nil, err
	}

	hasher, err := password.New(password.Config{})
	if err != nil {
		return nil, fmt.Errorf("identity password hasher: %w", err)
	}
	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		return nil, fmt.Errorf("identity password service: %w", err)
	}

	limiter, err := buildLoginLimiter(cfg)
	if err != nil {
		return nil, err
	}
	guard, err := app.NewLoginGuard(
		accounts,
		attempts,
		deps.IDs,
		app.LockoutPolicy{
			MaxFailures: cfg.Identity.Lockout.MaxFailures,
			Window:      cfg.Identity.Lockout.Window,
			Duration:    cfg.Identity.Lockout.Duration,
		},
		app.WithLoginLimiter(limiter),
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

	checker, err := app.NewRevocationChecker(
		accounts,
		revocation,
		cfg.Identity.Revocation.FailureMode,
		cfg.Identity.AccessTTL+time.Minute,
	)
	if err != nil {
		return nil, fmt.Errorf("identity revocation checker: %w", err)
	}

	authSvc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts:         accounts,
		Credentials:      credentials,
		Sessions:         sessions,
		Guard:            guard,
		Password:         passwordSvc,
		Tokens:           tokens,
		Revocation:       revocation,
		Audit:            audit.SlogEmitter{Log: deps.Logger},
		IDs:              deps.IDs,
		Channel:          channel,
		IdleTimeout:      cfg.Identity.Session.IdleTimeout,
		Service:          cfg.Service,
		MFA:              mfa,
		MFACipher:        mfaCipher,
		MFATickets:       tickets,
		MFAIssuer:        cfg.Identity.MFA.Issuer,
		MFAAdminRequired: cfg.Identity.MFA.AdminRequired,
	})
	if err != nil {
		return nil, fmt.Errorf("identity auth service: %w", err)
	}

	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: authSvc, Channel: channel})
	if err != nil {
		return nil, fmt.Errorf("identity handler: %w", err)
	}

	registerHealth(deps)
	return &Runtime{Checker: checker, Handler: handler}, nil
}

// buildRepositories 按 db.driver 选择内存或 MySQL 仓储。
func buildRepositories(deps *platform.Deps) (
	domain.AccountRepository,
	domain.CredentialRepository,
	domain.SessionRepository,
	domain.LoginAttemptRepository,
	domain.MFAEnrollmentRepository,
	error,
) {
	if deps.Database == nil {
		return persistence.NewMemoryAccountRepository(),
			persistence.NewMemoryCredentialRepository(),
			persistence.NewMemorySessionRepository(),
			persistence.NewMemoryLoginAttemptRepository(),
			persistence.NewMemoryMFAEnrollmentRepository(),
			nil
	}
	sqlDB := deps.Database.SQL()
	return persistence.NewSQLAccountRepository(sqlDB),
		persistence.NewSQLCredentialRepository(sqlDB),
		persistence.NewSQLSessionRepository(sqlDB),
		persistence.NewSQLLoginAttemptRepository(sqlDB),
		persistence.NewSQLMFAEnrollmentRepository(sqlDB),
		nil
}

// buildMFACipher 解析 identity.mfa.secret_key；未配置时返回 nil（MFA 端点失败关闭）。
func buildMFACipher(cfg *platform.Config) (*crypto.AEAD, error) {
	key, err := cfg.Identity.MFA.DecodeSecretKey()
	if err != nil {
		return nil, fmt.Errorf("identity mfa secret key: %w", err)
	}
	if key == nil {
		if cfg.Identity.MFA.AdminRequired {
			return nil, apperr.Internal("identity.mfa.secret_key is required when admin_required=true", nil)
		}
		return nil, nil
	}
	aead, err := crypto.NewAES256GCM(key)
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

// buildLoginLimiter 构造按「端别 + 账号摘要 + IP 摘要」分桶的登录限流器。
func buildLoginLimiter(cfg *platform.Config) (app.LoginLimiter, error) {
	rps := cfg.Identity.LoginRateLimit.RPS
	burst := cfg.Identity.LoginRateLimit.Burst
	keyed, err := ratelimit.NewKeyed(
		func() (ratelimit.Limiter, error) {
			return ratelimit.NewTokenBucket(rps, burst)
		},
		ratelimit.WithIdleTTL(loginLimiterIdleTTL),
	)
	if err != nil {
		return nil, fmt.Errorf("identity login limiter: %w", err)
	}
	return loginLimiter{keyed: keyed}, nil
}

// loginLimiter 把 pkg/ratelimit 适配为 app.LoginLimiter。
type loginLimiter struct {
	keyed *ratelimit.Keyed
}

// Allow 尝试消耗一次配额，返回是否放行与建议等待时长。
func (l loginLimiter) Allow(key string) (bool, time.Duration) {
	result := l.keyed.Allow(key)
	return result.Allowed, result.RetryAfter
}

// registerHealth 把身份域真实外部依赖注册到 /readyz（OBS-03）。
//
// 内存仓储 / 未配置 Redis 时不注册假检查，避免就绪探针永远成功。
func registerHealth(deps *platform.Deps) {
	if deps.Health == nil {
		return
	}
	if deps.Database != nil {
		deps.Health.Register("identity_db", deps.Database.Checker())
	}
	if deps.Redis != nil {
		deps.Health.Register("identity_redis", deps.Redis.Checker())
	}
}
