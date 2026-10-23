package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

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

// minAccountStateTTL 是账号撤销版本缓存的下限，避免极短 access_ttl 造成回源风暴。
const minAccountStateTTL = time.Hour

// AccountState 是撤销检查所需的账号最小快照。
//
// Status 与 Version 必须来自同一次读取：分两次读取会得到「旧状态 + 新版本」的
// 不一致组合，导致停用账号在版本回填前被误放行。
type AccountState struct {
	// Version 账号当前 TokenVersion。
	Version int64
	// Status 账号生命周期状态。
	Status domain.AccountStatus
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
	// RevokeToken 把 jti 写入黑名单，ttl 为令牌剩余寿命。
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	// IsTokenRevoked 报告 jti 是否在黑名单中。
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
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

// RevocationChecker 实现 internal/security.RevocationChecker（docs/identity-authn.md §6.2）。
//
// 校验顺序：账号状态 → token_version → jti 黑名单。
// 任一环节依赖不可用时按 failure_mode 返回 ErrUnavailable（fail_closed）或放行（fail_open）。
type RevocationChecker struct {
	accounts    domain.AccountRepository
	store       RevocationStore
	failureMode string
	stateTTL    time.Duration
	clock       Clock
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
	if stateTTL < minAccountStateTTL {
		stateTTL = minAccountStateTTL
	}
	checker := &RevocationChecker{
		accounts:    accounts,
		store:       store,
		failureMode: failureMode,
		stateTTL:    stateTTL,
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

// loadState 优先读缓存，未命中回源数据库并回填。
func (c *RevocationChecker) loadState(ctx context.Context, accountID string) (AccountState, bool, error) {
	state, hit, err := c.store.LoadAccountState(ctx, accountID)
	if err == nil && hit {
		return state, true, nil
	}
	if err != nil {
		return AccountState{}, false, err
	}
	account, err := c.accounts.FindByID(ctx, accountID)
	if err != nil {
		return AccountState{}, false, err
	}
	state = AccountState{Version: account.TokenVersion, Status: account.Status}
	if err := c.store.SaveAccountState(ctx, accountID, state, c.stateTTL); err != nil {
		return AccountState{}, false, err
	}
	return state, true, nil
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

// enforceRevocationMode 校验故障策略；空值按 fail_closed 处理。
func enforceRevocationMode(mode string) (string, error) {
	if mode == "" {
		return RevocationFailClosed, nil
	}
	if mode != RevocationFailClosed && mode != RevocationFailOpen {
		return "", fmt.Errorf("unsupported revocation failure mode: %s", mode)
	}
	return mode, nil
}
