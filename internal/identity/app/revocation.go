package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// 撤销存储故障策略（identity.revocation.failure_mode）。
const (
	// RevocationFailClosed Redis / DB 不可用时拒绝请求（生产强制）。
	RevocationFailClosed = "fail_closed"
	// RevocationFailOpen 依赖不可用时放行；仅允许非生产环境。
	RevocationFailOpen = "fail_open"
)

// 账号撤销快照的陈旧度边界（docs/identity-authn.md §6.1）。
//
// 数据库是唯一事实源，Redis 只是加速层：快照的 FetchedAt 超过 state_ttl 即强制回源，
// 因此账号禁用 / 改密 / 登出全部的最长可见延迟不超过 MaxAccountStateTTL。
const (
	// MinAccountStateTTL 是快照最小陈旧度，避免 0 值造成每次请求都回源。
	MinAccountStateTTL = time.Second
	// DefaultAccountStateTTL 是未配置时的快照陈旧度。
	DefaultAccountStateTTL = 60 * time.Second
	// MaxAccountStateTTL 是硬上限，对应「≤60s 生效」的安全承诺。
	MaxAccountStateTTL = 60 * time.Second
)

// AccountState 是撤销检查所需的账号最小快照。
//
// Status 与 Version 必须来自同一次读取：分两次读取会得到「旧状态 + 新版本」的
// 不一致组合，导致停用账号在版本回填前被误放行。
type AccountState struct {
	// Version 账号当前 TokenVersion。
	Version int64
	// Status 账号生命周期状态。
	Status domain.AccountStatus
	// FetchedAt 该快照回源数据库的时间；零值表示旧格式缓存，必须回源刷新。
	FetchedAt time.Time
}

// RevocationStore 是撤销状态的持久化端口（Redis / 内存）。
//
// 约定：
//   - LoadAccountState 未命中返回 found=false，不得用零值代替「未知」；
//   - 读写错误必须原样返回，由 RevocationChecker 按 failure_mode 决定放行或拒绝；
//   - jti 黑名单 TTL 为令牌剩余寿命，过期即自然清除。
type RevocationStore interface {
	// LoadAccountState 读取账号撤销快照。
	LoadAccountState(ctx context.Context, accountID string) (AccountState, bool, error)
	// SaveAccountState 写入账号撤销快照，ttl 必须 > 0。
	SaveAccountState(ctx context.Context, accountID string, state AccountState, ttl time.Duration) error
	// DeleteAccountState 删除账号撤销快照，使下一次校验必然回源数据库。
	DeleteAccountState(ctx context.Context, accountID string) error
	// RevokeToken 把 jti 写入黑名单，ttl 为令牌剩余寿命。
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	// IsTokenRevoked 报告 jti 是否在黑名单中。
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
}

// RevocationObserver 接收撤销快照写入 / 删除失败的观测事件。
//
// 失败不影响已提交的业务结果：快照超过 TTL 后必然回源数据库。
type RevocationObserver interface {
	// OnAccountStateWriteFailure 记录一次快照写入失败；op 为 set | delete。
	OnAccountStateWriteFailure(ctx context.Context, op string, err error)
}

// RevocationCheckerOption 配置 RevocationChecker。
type RevocationCheckerOption func(*RevocationChecker)

// WithRevocationClock 注入时钟（测试用）。
func WithRevocationClock(clock Clock) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// WithRevocationObserver 注入快照写入失败观测。
func WithRevocationObserver(observer RevocationObserver) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if observer != nil {
			c.observer = observer
		}
	}
}

// RevocationChecker 实现 internal/security.RevocationChecker（docs/identity-authn.md §6.2）。
//
// 校验顺序：账号状态 → token_version → jti 黑名单。
// 任一环节依赖不可用时按 failure_mode 返回 ErrUnavailable（fail_closed）或放行（fail_open）。
type RevocationChecker struct {
	accounts    domain.AccountRepository
	store       RevocationStore
	failureMode string
	stateTTL    time.Duration
	observer    RevocationObserver
	clock       Clock
	refetch     singleflight.Group
}

// NewRevocationChecker 构造撤销检查器；accounts / store 必填。
func NewRevocationChecker(
	accounts domain.AccountRepository,
	store RevocationStore,
	failureMode string,
	stateTTL time.Duration,
	opts ...RevocationCheckerOption,
) (*RevocationChecker, error) {
	if accounts == nil || store == nil {
		return nil, apperr.Internal("revocation checker dependencies are required", nil)
	}
	if failureMode == "" {
		failureMode = RevocationFailClosed
	}
	if failureMode != RevocationFailClosed && failureMode != RevocationFailOpen {
		return nil, apperr.BadRequest("unsupported revocation failure mode: " + failureMode)
	}
	checker := &RevocationChecker{
		accounts:    accounts,
		store:       store,
		failureMode: failureMode,
		stateTTL:    clampAccountStateTTL(stateTTL),
		clock:       realClock{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(checker)
		}
	}
	return checker, nil
}

// Check 在签名 / 时间 / iss / aud / channel 校验通过后执行撤销判定。
//
// 返回 apperr.ErrUnauthorized 时中间件输出 40100；返回 apperr.ErrUnavailable 时输出 50300。
func (c *RevocationChecker) Check(ctx context.Context, claims jwt.Claims) error {
	accountID := claims.Subject
	if accountID == "" {
		return invalidToken()
	}
	state, _, err := c.loadState(ctx, accountID)
	if err != nil {
		return c.failure(err)
	}
	if state.Status == domain.AccountStatusDisabled {
		return invalidToken()
	}
	version, ok := claimVersion(claims)
	if !ok || version < state.Version {
		return invalidToken()
	}
	if claims.ID != "" {
		revoked, err := c.store.IsTokenRevoked(ctx, claims.ID)
		if err != nil {
			return c.failure(err)
		}
		if revoked {
			return invalidToken()
		}
	}
	return nil
}

// loadState 读取账号快照：命中且未超过陈旧度直接复用，否则回源数据库并回填。
func (c *RevocationChecker) loadState(ctx context.Context, accountID string) (AccountState, bool, error) {
	state, hit, err := c.store.LoadAccountState(ctx, accountID)
	if err != nil {
		return AccountState{}, false, err
	}
	if hit && c.isFresh(state) {
		return state, true, nil
	}
	return c.refetchState(ctx, accountID)
}

// isFresh 报告快照是否仍在允许陈旧度内；零值 FetchedAt 视为陈旧（旧格式缓存）。
func (c *RevocationChecker) isFresh(state AccountState) bool {
	if state.FetchedAt.IsZero() {
		return false
	}
	elapsed := c.clock.Now().Sub(state.FetchedAt)
	// 时钟回拨时 elapsed 为负：按新鲜处理，避免把所有请求同时打到数据库。
	if elapsed < 0 {
		return true
	}
	return elapsed <= c.stateTTL
}

// refetchState 回源数据库并回填快照；同一账号的并发回源合并为一次。
//
// 回填失败不改变本次判定：刚读到的数据库快照就是事实源，只是下次校验会再次回源。
func (c *RevocationChecker) refetchState(ctx context.Context, accountID string) (AccountState, bool, error) {
	value, err, _ := c.refetch.Do(accountID, func() (any, error) {
		// 获得执行权后二次检查，避免慢回源期间的其它请求刚写入新快照仍被覆盖。
		if loaded, hit, loadErr := c.store.LoadAccountState(ctx, accountID); loadErr != nil {
			return nil, loadErr
		} else if hit && c.isFresh(loaded) {
			return loaded, nil
		}
		account, dbErr := c.accounts.FindByID(ctx, accountID)
		if dbErr != nil {
			return nil, dbErr
		}
		state := AccountState{
			Version:   account.TokenVersion,
			Status:    account.Status,
			FetchedAt: c.clock.Now(),
		}
		if saveErr := c.store.SaveAccountState(ctx, accountID, state, c.stateTTL); saveErr != nil {
			c.notifyWriteFailure(ctx, "set", saveErr)
		}
		return state, nil
	})
	if err != nil {
		return AccountState{}, false, err
	}
	state, ok := value.(AccountState)
	if !ok {
		return AccountState{}, false, apperr.Internal("revocation checker: unexpected state type", nil)
	}
	return state, true, nil
}

// notifyWriteFailure 上报快照写入失败；观测器缺失时静默，由 TTL 兜底。
func (c *RevocationChecker) notifyWriteFailure(ctx context.Context, op string, err error) {
	if c.observer == nil {
		return
	}
	c.observer.OnAccountStateWriteFailure(ctx, op, err)
}

// failure 按故障策略返回：fail_closed 输出 50300，fail_open 放行。
func (c *RevocationChecker) failure(err error) error {
	if errors.Is(err, apperr.ErrNotFound) {
		// 读不到账号不等于依赖故障：账号已不存在必须拒绝，禁止 fail_open 放行。
		return invalidToken()
	}
	if c.failureMode == RevocationFailOpen {
		return nil
	}
	return apperr.New(apperr.CodeServiceUnavailable,
		"revocation store unavailable",
		errors.Join(apperr.ErrUnavailable, err))
}

// RevocationInvalidator 统一刷新账号快照：SET 新值，失败回退 DEL。
//
// 所有 token_version 变更（禁用、改密、MFA 解绑、logout-all）提交后都必须经过本端口，
// 禁止用例直接调用 SaveAccountState 绕过回退逻辑。
type RevocationInvalidator struct {
	store    RevocationStore
	ttl      time.Duration
	observer RevocationObserver
	clock    Clock
}

// RevocationInvalidatorOption 配置 RevocationInvalidator。
type RevocationInvalidatorOption func(*RevocationInvalidator)

// WithInvalidatorClock 注入时钟（测试用）。
func WithInvalidatorClock(clock Clock) RevocationInvalidatorOption {
	return func(i *RevocationInvalidator) {
		if clock != nil {
			i.clock = clock
		}
	}
}

// NewRevocationInvalidator 构造快照失效器；store 必填。
func NewRevocationInvalidator(
	store RevocationStore,
	ttl time.Duration,
	observer RevocationObserver,
	opts ...RevocationInvalidatorOption,
) (*RevocationInvalidator, error) {
	if store == nil {
		return nil, apperr.Internal("revocation invalidator requires store", nil)
	}
	invalidator := &RevocationInvalidator{
		store:    store,
		ttl:      clampAccountStateTTL(ttl),
		observer: observer,
		clock:    realClock{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(invalidator)
		}
	}
	return invalidator, nil
}

// Invalidate 写入新快照；SET 失败时回退 DEL，保证下一次校验必然回源数据库。
//
// 仅当 SET 与 DEL 都失败时返回错误；调用方不得据此改变已提交的业务结果。
func (i *RevocationInvalidator) Invalidate(ctx context.Context, accountID string, state AccountState) error {
	if accountID == "" {
		return apperr.BadRequest("account id is required")
	}
	state.FetchedAt = i.clock.Now()
	if err := i.store.SaveAccountState(ctx, accountID, state, i.ttl); err != nil {
		i.notify(ctx, "set", err)
		if delErr := i.store.DeleteAccountState(ctx, accountID); delErr != nil {
			i.notify(ctx, "delete", delErr)
			return errors.Join(err, delErr)
		}
	}
	return nil
}

// notify 上报失败；观测器缺失时静默，由 ≤TTL 的过期兜底。
func (i *RevocationInvalidator) notify(ctx context.Context, op string, err error) {
	if i.observer == nil {
		return
	}
	i.observer.OnAccountStateWriteFailure(ctx, op, err)
}

// clampAccountStateTTL 把配置值收敛到 [MinAccountStateTTL, MaxAccountStateTTL]。
func clampAccountStateTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultAccountStateTTL
	}
	if ttl < MinAccountStateTTL {
		return MinAccountStateTTL
	}
	if ttl > MaxAccountStateTTL {
		return MaxAccountStateTTL
	}
	return ttl
}

// claimVersion 从 ver 声明读取账号撤销版本，兼容 JSON 数字反序列化类型。
func claimVersion(claims jwt.Claims) (int64, bool) {
	if claims.Extra == nil {
		return 0, false
	}
	raw, ok := claims.Extra["ver"]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// invalidToken 构造统一的 40100 错误（不暴露具体撤销原因）。
func invalidToken() error {
	return apperr.Unauthorized("invalid token")
}
