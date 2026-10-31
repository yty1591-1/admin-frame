package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/crypto"
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

// LoginFailureCounter 是跨副本共享的失败计数端口（Redis INCR + EXPIRE）。
//
// 未注入时回退登录尝试表计数；注入后即使多副本并发，阈值判定也不会因
// 事务隔离或未提交记录而漏判。计数错误由守卫回退数据库计数，不阻断登录。
type LoginFailureCounter interface {
	// Increment 在窗口内自增一次失败并返回最新计数。
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)
	// Reset 清除窗口计数（登录成功后调用）。
	Reset(ctx context.Context, key string) error
}

// LoginGuardOption 配置 LoginGuard。
type LoginGuardOption func(*LoginGuard)

// WithLoginLimiter 注入登录限流器；默认不启用。
func WithLoginLimiter(limiter LoginLimiter) (option LoginGuardOption) {
	return func(g *LoginGuard) {
		if limiter != nil {
			g.limiter = limiter
		}
	}
}

// WithIPRateLimiter 注入 IP 级登录限流器；与账号级独立配置（PERF-05）。
//
// 未注入时 IP 维度回退账号级限流器，保持旧行为与单测兼容。
func WithIPRateLimiter(limiter LoginLimiter) LoginGuardOption {
	return func(g *LoginGuard) {
		if limiter != nil {
			g.ipLimiter = limiter
		}
	}
}

// WithLoginFailureCounter 注入跨副本失败计数器；默认回退数据库计数。
func WithLoginFailureCounter(counter LoginFailureCounter) LoginGuardOption {
	return func(g *LoginGuard) {
		if counter != nil {
			g.counter = counter
		}
	}
}

// WithHasher 注入 PII 摘要器；nil 时回退历史无盐算法（仅限本地/tests）。
func WithHasher(hasher *crypto.Hasher) LoginGuardOption {
	return func(g *LoginGuard) {
		if hasher != nil {
			g.hasher = hasher
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
	ipLimiter LoginLimiter
	counter  LoginFailureCounter
	hasher   *crypto.Hasher
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

// CheckRateLimit 对登录请求执行多维度限流；未注入限流器时直接放行。
//
// 传入的 key 相互独立（账号级、IP 级），任一超限即拒绝；返回建议等待时长为各维度最大值。
func (g *LoginGuard) CheckRateLimit(keys ...string) error {
	return g.checkRateLimitWith(g.limiter, keys...)
}

// CheckRateLimitPair 使用账号级与 IP 级独立限流器校验登录（PERF-05）。
//
// accountKey 为空时跳过账号维度；ipKey 为空时跳过 IP 维度；
// IP 限流器未注入时回退账号级限流器。
func (g *LoginGuard) CheckRateLimitPair(accountKey, ipKey string) error {
	if g.limiter == nil && g.ipLimiter == nil {
		return nil
	}
	accountLimiter := g.limiter
	if accountLimiter == nil {
		accountLimiter = g.ipLimiter
	}
	if err := g.checkRateLimitWith(accountLimiter, accountKey); err != nil {
		return err
	}
	ipLimiter := g.ipLimiter
	if ipLimiter == nil {
		ipLimiter = accountLimiter
	}
	return g.checkRateLimitWith(ipLimiter, ipKey)
}

// checkRateLimitWith 用指定限流器校验一组 key。
func (g *LoginGuard) checkRateLimitWith(limiter LoginLimiter, keys ...string) error {
	if limiter == nil {
		return nil
	}
	var retryAfter time.Duration
	limited := false
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		allowed, after := limiter.Allow(trimmed)
		if allowed {
			continue
		}
		limited = true
		if after > retryAfter {
			retryAfter = after
		}
	}
	if !limited {
		return nil
	}
	msg := "too many login attempts"
	if retryAfter > 0 {
		msg = fmt.Sprintf("too many login attempts, retry after %s", retryAfter.Round(time.Second))
	}
	return NewRateLimitedError(msg, retryAfter)
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
// 返回值 lockedNow 表示本次调用是否真正触发了 active/pending → locked 迁移，
// 调用方据此只发一次 auth.account.locked，避免已锁定账号的每次失败重复发审计。
//
// 计数优先使用跨副本计数器；未注入或计数失败时回退登录尝试表。since 取
// 「窗口起点」与「最近一次成功登录时间」的较晚者，成功登录后窗口重新计数。
func (g *LoginGuard) RecordFailure(
	ctx context.Context,
	account *domain.Account,
	reason, ipHash, uaHash string,
	now time.Time,
) (bool, error) {
	if account == nil {
		return false, apperr.Internal("account is required", nil)
	}
	accountRef := AccountRef(g.hasher, account.Channel, account.Subject)
	attempt, err := g.newAttempt(account.Channel, accountRef, domain.LoginOutcomeFailure, normalizeReason(reason), ipHash, uaHash, now)
	if err != nil {
		return false, err
	}
	if err := g.attempts.Append(ctx, attempt); err != nil {
		return false, err
	}

	// 锁定未到期：允许试密码但保持锁定，不重复迁移、不重复发锁审计。
	if account.Status == domain.AccountStatusLocked && account.LockedUntil != nil && account.LockedUntil.After(now) {
		return false, nil
	}
	// 锁定已到期：先归一为 active，再按当前窗口重新计数，否则永远无法重新锁定。
	if account.Status == domain.AccountStatusLocked {
		unlocked, err := g.unlockExpired(ctx, account, now)
		if err != nil {
			return false, err
		}
		if !unlocked && account.Status == domain.AccountStatusLocked && account.LockedUntil != nil &&
			account.LockedUntil.After(now) {
			// 并发的卸载失败已把账号重新锁定，本次失败不再计数。
			return false, nil
		}
	}
	if account.IsDeleted() || account.Status == domain.AccountStatusDisabled {
		return false, nil
	}

	failures, err := g.countFailures(ctx, account.Channel, accountRef, now)
	if err != nil {
		return false, err
	}
	if failures < g.policy.MaxFailures ||
		(account.Status != domain.AccountStatusActive && account.Status != domain.AccountStatusPending) {
		return false, nil
	}

	return g.lockAccount(ctx, account, now)
}

// unlockExpired 把过期锁定的账号归一为 active，并同步调用方持有的账号对象。
//
// 在副本上修改、保存成功后才写回调用方，避免版本冲突失败时污染入参。
func (g *LoginGuard) unlockExpired(ctx context.Context, account *domain.Account, now time.Time) (bool, error) {
	candidate := *account
	unlocked, err := candidate.UnlockIfExpired(now)
	if err != nil || !unlocked {
		return false, err
	}
	if err := g.accounts.Save(ctx, &candidate, account.Version); err != nil {
		if !errors.Is(err, apperr.ErrConflict) {
			return false, err
		}
		// 并发请求已解锁或已重新锁定：以最新状态为准，不把冲突暴露成 500。
		fresh, loadErr := g.accounts.FindByID(ctx, account.ID)
		if loadErr != nil {
			return false, nil
		}
		*account = *fresh
		return false, nil
	}
	*account = candidate
	return true, nil
}

// lockAccount 锁定账号；版本冲突时重读并重试一次，仍冲突按「并发方已处理」返回。
func (g *LoginGuard) lockAccount(ctx context.Context, account *domain.Account, now time.Time) (bool, error) {
	candidate := *account
	if err := candidate.Lock(now.Add(g.policy.Duration), now); err != nil {
		return false, err
	}
	if err := g.accounts.Save(ctx, &candidate, account.Version); err == nil {
		*account = candidate
		return true, nil
	} else if !errors.Is(err, apperr.ErrConflict) {
		return false, err
	}
	fresh, loadErr := g.accounts.FindByID(ctx, account.ID)
	if loadErr != nil {
		return false, nil
	}
	if fresh.Status == domain.AccountStatusLocked || fresh.IsDeleted() {
		*account = *fresh
		return false, nil
	}
	// 重读后仍处于可锁定状态：再尝试一次；再次冲突说明并发方已完成迁移。
	retry := *fresh
	if lockErr := retry.Lock(now.Add(g.policy.Duration), now); lockErr != nil {
		*account = *fresh
		return false, nil
	}
	if saveErr := g.accounts.Save(ctx, &retry, fresh.Version); saveErr != nil {
		*account = *fresh
		return false, nil
	}
	*account = retry
	return true, nil
}

// countFailures 统计窗口内失败次数：优先跨副本计数器，失败或未注入时回退数据库。
func (g *LoginGuard) countFailures(ctx context.Context, channel domain.Channel, accountRef string, now time.Time) (int, error) {
	if g.counter != nil {
		count, err := g.counter.Increment(ctx, loginFailureKey(channel, accountRef), g.policy.Window)
		if err == nil {
			return int(count), nil
		}
		// Redis 故障时回退数据库计数：保持锁定能力，不把登录整体打成 503。
	}
	since := now.Add(-g.policy.Window)
	lastSuccess, found, err := g.attempts.LastSuccessAt(ctx, channel, accountRef)
	if err != nil {
		return 0, err
	}
	if found && lastSuccess.After(since) {
		since = lastSuccess
	}
	return g.attempts.CountFailuresSince(ctx, channel, accountRef, since)
}

// RecordSuccess 记录成功尝试，重置失败窗口，并把已到期的锁定恢复为 active。
func (g *LoginGuard) RecordSuccess(
	ctx context.Context,
	account *domain.Account,
	ipHash, uaHash string,
	now time.Time,
) error {
	if account == nil {
		return apperr.Internal("account is required", nil)
	}
	accountRef := AccountRef(g.hasher, account.Channel, account.Subject)
	attempt, err := g.newAttempt(account.Channel, accountRef, domain.LoginOutcomeSuccess, "", ipHash, uaHash, now)
	if err != nil {
		return err
	}
	if err := g.attempts.Append(ctx, attempt); err != nil {
		return err
	}
	// 成功即重置窗口；计数器不可用时由 LastSuccessAt 兜底，不影响登录结果。
	if g.counter != nil {
		_ = g.counter.Reset(ctx, loginFailureKey(account.Channel, accountRef))
	}
	if account.Status != domain.AccountStatusLocked {
		return nil
	}
	expectedVersion := account.Version
	unlocked, err := account.UnlockIfExpired(now)
	if err != nil {
		return err
	}
	if !unlocked {
		return nil
	}
	return g.accounts.Save(ctx, account, expectedVersion)
}

// loginFailureKey 构造失败计数键：channel + 账号摘要，跨副本共享。
func loginFailureKey(channel domain.Channel, accountRef string) string {
	return string(channel) + ":login_fail:" + accountRef
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
