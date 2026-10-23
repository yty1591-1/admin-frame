package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 默认锁定策略，与 docs/identity-authn.md §10 一致。
const (
	// DefaultLockoutMaxFailures 窗口内最大失败次数。
	DefaultLockoutMaxFailures = 5
	// DefaultLockoutWindow 失败计数窗口。
	DefaultLockoutWindow = 15 * time.Minute
	// DefaultLockoutDuration 锁定时长。
	DefaultLockoutDuration = 15 * time.Minute
)

// 锁定策略哨兵错误。
var (
	// ErrInvalidLockoutPolicy 锁定策略参数非法。
	ErrInvalidLockoutPolicy = errors.New("invalid lockout policy")
)

// LockoutPolicy 描述失败计数窗口与锁定时长。
type LockoutPolicy struct {
	// MaxFailures 窗口内失败次数达到该值即锁定。
	MaxFailures int
	// Window 失败计数窗口。
	Window time.Duration
	// Duration 锁定时长。
	Duration time.Duration
}

// DefaultLockoutPolicy 返回 5 次 / 15 分钟窗口 / 锁定 15 分钟。
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		MaxFailures: DefaultLockoutMaxFailures,
		Window:      DefaultLockoutWindow,
		Duration:    DefaultLockoutDuration,
	}
}

// Validate 校验策略参数。
func (p LockoutPolicy) Validate() error {
	if p.MaxFailures < 1 {
		return fmt.Errorf("%w: max failures must be >= 1", ErrInvalidLockoutPolicy)
	}
	if p.Window <= 0 {
		return fmt.Errorf("%w: window must be positive", ErrInvalidLockoutPolicy)
	}
	if p.Duration <= 0 {
		return fmt.Errorf("%w: duration must be positive", ErrInvalidLockoutPolicy)
	}
	return nil
}

// LoginLimiter 是登录限流的消费侧端口（S-07）。
//
// 由 transport / platform 用 pkg/ratelimit 适配；返回 RetryAfter 供响应头使用。
type LoginLimiter interface {
	Allow(key string) (allowed bool, retryAfter time.Duration)
}

// IDGenerator 是雪花 ID 的消费侧端口，用于生成登录尝试主键。
type IDGenerator interface {
	NextString() (string, error)
}

// LoginGuardOption 配置 LoginGuard。
type LoginGuardOption func(*LoginGuard)

// WithLoginLimiter 注入登录限流器；默认不启用。
func WithLoginLimiter(limiter LoginLimiter) LoginGuardOption {
	return func(g *LoginGuard) {
		if limiter != nil {
			g.limiter = limiter
		}
	}
}

// LoginGuard 负责登录准入与失败锁定编排。
//
// 职责边界：
//   - Authorize 只判断账号事实，不读取凭据，避免把「账号不存在」与「密码错误」区分开；
//   - RecordFailure 追加登录尝试、按窗口计数并在阈值处锁定账号；
//   - RecordSuccess 记录成功尝试，并把已到期的锁定恢复为 active。
//
// 审计事件在阶段 5 与业务写入同事务落 Outbox；本阶段只保证状态迁移与计数正确。
type LoginGuard struct {
	accounts domain.AccountRepository
	attempts domain.LoginAttemptRepository
	ids      IDGenerator
	policy   LockoutPolicy
	limiter  LoginLimiter
}

// NewLoginGuard 构造登录守卫；仓储、ID 生成器与策略均必填。
func NewLoginGuard(
	accounts domain.AccountRepository,
	attempts domain.LoginAttemptRepository,
	ids IDGenerator,
	policy LockoutPolicy,
	opts ...LoginGuardOption,
) (*LoginGuard, error) {
	if accounts == nil || attempts == nil || ids == nil {
		return nil, apperr.Internal("login guard dependencies are required", nil)
	}
	if err := policy.Validate(); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	guard := &LoginGuard{
		accounts: accounts,
		attempts: attempts,
		ids:      ids,
		policy:   policy,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(guard)
		}
	}
	return guard, nil
}

// CheckRateLimit 对登录请求执行分桶限流；未注入限流器时直接放行。
func (g *LoginGuard) CheckRateLimit(key string) error {
	if g.limiter == nil {
		return nil
	}
	allowed, retryAfter := g.limiter.Allow(key)
	if allowed {
		return nil
	}
	msg := "too many login attempts"
	if retryAfter > 0 {
		msg = fmt.Sprintf("too many login attempts, retry after %s", retryAfter.Round(time.Second))
	}
	return apperr.TooManyRequests(msg)
}

// Authorize 判断账号当前是否允许登录；拒绝时统一返回 40100 文案。
//
// 真实原因通过 domain 哨兵保留在错误链中（locked / disabled），供审计记录。
func (g *LoginGuard) Authorize(account *domain.Account, now time.Time) error {
	if account == nil {
		return invalidCredentials()
	}
	if account.IsDeleted() || account.Status == domain.AccountStatusDisabled {
		return authFailure(domain.ErrAccountDisabled)
	}
	if account.Status == domain.AccountStatusLocked &&
		account.LockedUntil != nil && account.LockedUntil.After(now) {
		return authFailure(domain.ErrAccountLocked)
	}
	return nil
}

// RecordFailure 记录失败尝试，并在窗口内失败次数达到阈值时锁定账号。
//
// since 取「窗口起点」与「最近一次成功登录时间」的较晚者：
// 成功登录后窗口重新计数，但无需删除历史审计记录。
func (g *LoginGuard) RecordFailure(
	ctx context.Context,
	account *domain.Account,
	reason, ipHash, uaHash string,
	now time.Time,
) error {
	if account == nil {
		return apperr.Internal("account is required", nil)
	}
	accountRef := HashAccountRef(account.Channel, account.Subject)
	attempt, err := g.newAttempt(account.Channel, accountRef, domain.LoginOutcomeFailure, normalizeReason(reason), ipHash, uaHash, now)
	if err != nil {
		return err
	}
	if err := g.attempts.Append(ctx, attempt); err != nil {
		return err
	}

	since := now.Add(-g.policy.Window)
	lastSuccess, found, err := g.attempts.LastSuccessAt(ctx, account.Channel, accountRef)
	if err != nil {
		return err
	}
	if found && lastSuccess.After(since) {
		since = lastSuccess
	}
	failures, err := g.attempts.CountFailuresSince(ctx, account.Channel, accountRef, since)
	if err != nil {
		return err
	}
	if failures < g.policy.MaxFailures || account.Status != domain.AccountStatusActive {
		return nil
	}

	expectedVersion := account.Version
	if err := account.Lock(now.Add(g.policy.Duration), now); err != nil {
		return err
	}
	return g.accounts.Save(ctx, account, expectedVersion)
}

// RecordSuccess 记录成功尝试，并把已到期的锁定恢复为 active。
func (g *LoginGuard) RecordSuccess(
	ctx context.Context,
	account *domain.Account,
	ipHash, uaHash string,
	now time.Time,
) error {
	if account == nil {
		return apperr.Internal("account is required", nil)
	}
	accountRef := HashAccountRef(account.Channel, account.Subject)
	attempt, err := g.newAttempt(account.Channel, accountRef, domain.LoginOutcomeSuccess, "", ipHash, uaHash, now)
	if err != nil {
		return err
	}
	if err := g.attempts.Append(ctx, attempt); err != nil {
		return err
	}
	if account.Status != domain.AccountStatusLocked {
		return nil
	}
	expectedVersion := account.Version
	unlocked, err := account.TryUnlockExpired(now)
	if err != nil {
		return err
	}
	if !unlocked {
		return nil
	}
	return g.accounts.Save(ctx, account, expectedVersion)
}

// newAttempt 生成并校验一条登录尝试。
func (g *LoginGuard) newAttempt(
	channel domain.Channel,
	accountRef string,
	outcome domain.LoginOutcome,
	reason, ipHash, uaHash string,
	now time.Time,
) (*domain.LoginAttempt, error) {
	id, err := g.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate login attempt id failed", err)
	}
	return domain.NewLoginAttempt(domain.NewLoginAttemptParams{
		ID:         id,
		Channel:    channel,
		AccountRef: accountRef,
		Outcome:    outcome,
		Reason:     reason,
		IPHash:     ipHash,
		UAHash:     uaHash,
		OccurredAt: now,
	})
}

// HashAccountRef 返回账号主体的不可逆摘要（含端别前缀）。
//
// 登录尝试与审计只允许保存该摘要，禁止落明文手机号 / 邮箱。
func HashAccountRef(channel domain.Channel, subject string) string {
	sum := sha256.Sum256([]byte(string(channel) + ":" + domain.NormalizeSubject(subject)))
	return hex.EncodeToString(sum[:])
}

// authFailure 构造带领域原因的 40100 错误，对外文案保持统一。
func authFailure(sentinel error) error {
	return apperr.New(apperr.CodeUnauthorized, "invalid credentials",
		errors.Join(sentinel, apperr.ErrUnauthorized))
}
