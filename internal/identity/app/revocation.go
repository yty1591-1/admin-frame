package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// refetchWorkTimeout 是 singleflight 回源工作的独立上限（REL-03）。
//
// 回源使用与调用方解耦的 context：首个请求取消不应让同 key 的等待者一起失败；
// 同时给回源本身一个硬上限，避免无界占用连接。
const refetchWorkTimeout = 5 * time.Second

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

// SessionState 是撤销检查所需的会话最小快照（IDN-P1-02）。
//
// Status / RevokedAt / ExpiresAt 必须来自同一次读取；FetchedAt 用于陈旧度判定，
// 与账号快照共享 state_ttl 上限，保证会话撤销的最长可见延迟 ≤60s。
type SessionState struct {
	// Status 会话状态；revoked / expired 拒绝，active / rotated 放行。
	Status domain.SessionStatus
	// RevokedAt 撤销时间；零值表示未撤销。
	RevokedAt time.Time
	// ExpiresAt 会话绝对过期时间（UTC）；零值表示未知，由令牌自身 exp 兜底。
	ExpiresAt time.Time
	// FetchedAt 该快照回源数据库的时间；零值表示旧格式缓存，必须回源刷新。
	FetchedAt time.Time
}

// Revoked 报告会话是否已不可用：撤销、过期或超过绝对到期时间都拒绝。
//
// rotated 会话放行：旧 Access Token 在自身 exp 前仍应可用，避免刷新成功后
// 客户端仍在途的旧令牌被立刻打成 401。
func (s SessionState) Revoked(now time.Time) bool {
	if s.Status == domain.SessionStatusRevoked || s.Status == domain.SessionStatusExpired {
		return true
	}
	return !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt)
}

// ClientState 是撤销检查所需的 M2M 客户端最小快照（§21.2.3）。
//
// 与 AccountState 分离：client 令牌跳过账号 / 会话 / jti 检查，
// 只比较客户端 token_version 与状态，键为 authn:client_ver:<client_id>。
type ClientState struct {
	// Version 客户端当前 TokenVersion。
	Version int64
	// Status 客户端生命周期状态；非 active 一律拒绝。
	Status domain.M2MClientStatus
	// FetchedAt 该快照回源数据库的时间；零值视为陈旧，必须回源刷新。
	FetchedAt time.Time
}

// ClientStateStore 是撤销存储的可选扩展（M2M 客户端快照）。
//
// 独立于 RevocationStore：未实现时检查器直接回源数据库（DB 是唯一事实源），
// 行为仍正确，只是少了加速层；这样既有实现与测试替身无需改动。
type ClientStateStore interface {
	// LoadClientState 读取客户端撤销快照；未命中返回 found=false。
	LoadClientState(ctx context.Context, clientID string) (ClientState, bool, error)
	// SaveClientState 写入客户端撤销快照，ttl 必须 > 0。
	SaveClientState(ctx context.Context, clientID string, state ClientState, ttl time.Duration) error
	// DeleteClientState 删除客户端撤销快照，使下一次校验必然回源数据库。
	DeleteClientState(ctx context.Context, clientID string) error
}

// RevocationStore 是撤销状态的持久化端口（Redis / 内存）。//
// 约定：
//   - LoadAccountState / LoadSessionState 未命中返回 found=false，不得用零值代替「未知」；
//   - 读写错误必须原样返回，由 RevocationChecker 按 failure_mode 决定放行或拒绝；
//   - 账号 / 会话快照 TTL 与 state_ttl 一致（≤60s），jti 黑名单 TTL 为令牌剩余寿命。
type RevocationStore interface {
	// LoadAccountState 读取账号撤销快照。
	LoadAccountState(ctx context.Context, accountID string) (AccountState, bool, error)
	// SaveAccountState 写入账号撤销快照，ttl 必须 > 0。
	SaveAccountState(ctx context.Context, accountID string, state AccountState, ttl time.Duration) error
	// DeleteAccountState 删除账号撤销快照，使下一次校验必然回源数据库。
	DeleteAccountState(ctx context.Context, accountID string) error
	// LoadSessionState 读取会话撤销快照。
	LoadSessionState(ctx context.Context, sessionID string) (SessionState, bool, error)
	// SaveSessionState 写入会话撤销快照，ttl 必须 > 0。
	SaveSessionState(ctx context.Context, sessionID string, state SessionState, ttl time.Duration) error
	// DeleteSessionState 删除会话撤销快照，使下一次校验必然回源数据库。
	DeleteSessionState(ctx context.Context, sessionID string) error
	// RevokeToken 把 jti 写入黑名单，ttl 为令牌剩余寿命。
	RevokeToken(ctx context.Context, jti string, ttl time.Duration) error
	// IsTokenRevoked 报告 jti 是否在黑名单中。
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
}

// RevocationCacheObserver 接收 L1 / Redis / DB 回源的观测事件（IDN-GAP-P1-02）。
//
// 与失败观测分离，避免要求所有既有实现同时实现缓存指标；未注入时为 no-op。
type RevocationCacheObserver interface {
	// OnRevocationCacheHit 记录一次本地快照命中；kind=account|session。
	OnRevocationCacheHit(ctx context.Context, kind string)
	// OnRevocationRedisCall 记录一次 Redis 调用；op 为 account_load / session_load / token_load 等。
	OnRevocationRedisCall(ctx context.Context, op string)
	// OnRevocationDBRefetch 记录一次数据库回源；kind=account|session。
	OnRevocationDBRefetch(ctx context.Context, kind string)
}

// RevocationObserver 接收撤销快照写入 / 删除失败的观测事件。
//
// 失败不影响已提交的业务结果：快照超过 TTL 后必然回源数据库。
type RevocationObserver interface {
	// OnAccountStateWriteFailure 记录一次快照写入失败；op 为 set | delete。
	OnAccountStateWriteFailure(ctx context.Context, op string, err error)
	// OnRevocationStoreFailure 记录一次撤销存储读 / 回源失败；op 为 account_load | session_load | token_load。
	OnRevocationStoreFailure(ctx context.Context, op string, err error)
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

// WithRevocationCacheObserver 注入 L1 / Redis / DB 回源观测。
func WithRevocationCacheObserver(observer RevocationCacheObserver) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if observer != nil {
			c.cacheObserver = observer
		}
	}
}

// WithRevocationSessions 注入会话仓储，启用 Access Token 的 sid 会话状态校验（IDN-P1-02）。
//
// 未注入时跳过会话校验，仅保留账号级撤销；生产装配必须注入。
func WithRevocationSessions(sessions domain.SessionRepository) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if sessions != nil {
			c.sessions = sessions
		}
	}
}

// WithRevocationClients 注入 M2M 客户端仓储，启用 token_use=client 的撤销校验（§21.2.3）。
//
// 未注入时 client 令牌一律拒绝（失败关闭），避免「装配漏配 → 机器令牌不可撤销」。
func WithRevocationClients(clients domain.M2MClientRepository) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if clients != nil {
			c.clients = clients
		}
	}
}

// WithRevocationTokenGrace 设置 jti 本地宽限（8.4 R-8.4-01）；≤0 表示禁用。//
// 宽限只在 Redis 读失败或熔断冷却期内生效；正常路径仍逐请求校验黑名单。
func WithRevocationTokenGrace(grace time.Duration) RevocationCheckerOption {
	return func(c *RevocationChecker) {
		if grace > MaxRevocationTokenGrace {
			grace = MaxRevocationTokenGrace
		}
		c.tokenGrace = grace
	}
}

// 撤销存储熔断参数：连续失败后短时跳过 Redis，直接回源数据库。
//
// Redis 只是加速层，数据库是唯一事实源；熔断用于避免 Redis 故障时每个请求
// 都等待 3s 超时，把一次依赖故障放大为请求雪崩。
const (
	revocationStoreBreakerThreshold = 3
	revocationStoreBreakerCooldown  = 10 * time.Second
)

// errRevocationStoreCircuitOpen 表示撤销存储处于熔断冷却期，jti 校验不再触网。
//
// jti 黑名单没有数据库事实源，无法回源；冷却期内直接按 failure_mode 返回，
// 避免 Redis 故障时每个请求都等待读超时（REL-01）。
var errRevocationStoreCircuitOpen = errors.New("revocation store circuit open")

// jti 本地宽限参数（8.4 R-8.4-01）。
//
// jti 黑名单没有数据库事实源：Redis 故障时若一律 fail_closed，会让全部已认证请求
// 在故障期间变成 503。折中方案是记录「最近一次验证为未撤销」的 jti，
// 故障期间允许其在 min(宽限, 令牌剩余寿命) 内继续使用；超出宽限仍失败关闭。
// 代价是撤销（登出 / 改密）在该副本上的可见延迟最多为一个宽限窗口，默认 30s。
const (
	// DefaultRevocationTokenGrace 默认 jti 本地宽限。
	DefaultRevocationTokenGrace = 30 * time.Second
	// MaxRevocationTokenGrace 宽限硬上限，避免放大撤销延迟。
	MaxRevocationTokenGrace = 60 * time.Second
	// revocationTokenCacheSweep 每写入多少条 jti 触发一次过期清理。
	revocationTokenCacheSweep = 4096
)

// RevocationChecker 实现 internal/security.RevocationChecker（docs/identity-authn.md §6.2）。
//
// 校验顺序：账号状态 → token_version → 会话状态（sid）→ jti 黑名单。
// 账号 / 会话快照读取失败时回退数据库；jti 黑名单无数据库事实源，仍按
// failure_mode 失败关闭。任一环节最终不可用时返回 50300（fail_closed）或放行（fail_open）。
type RevocationChecker struct {
	accounts      domain.AccountRepository
	sessions      domain.SessionRepository
	clients       domain.M2MClientRepository
	store         RevocationStore
	clientStore   ClientStateStore
	failureMode   string
	stateTTL      time.Duration
	observer      RevocationObserver
	cacheObserver RevocationCacheObserver
	clock         Clock
	refetch       singleflight.Group

	breakerMu      sync.Mutex
	storeFailures  int
	storeOpenUntil time.Time

	// tokenGrace 是 jti 本地宽限；≤0 表示禁用（故障时一律失败关闭）。
	tokenGrace time.Duration
	tokens     sync.Map
	tokenSeen  atomic.Int64
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
	// 客户端快照是可选的加速层；存储未实现时检查器直接回源数据库。
	if clientStore, ok := store.(ClientStateStore); ok {
		checker.clientStore = clientStore
	}
	return checker, nil
}

// Check 在签名 / 时间 / iss / aud / channel 校验通过后执行撤销判定。
//
// 返回 apperr.ErrUnauthorized 时中间件输出 40100；返回 apperr.ErrUnavailable 时输出 50300。
func (c *RevocationChecker) Check(ctx context.Context, claims jwt.Claims) error {
	// M2M 令牌走独立分支：不查账号 / 会话 / jti，只比较客户端版本与状态（§21.2.3）。
	if claims.GetString("token_use") == TokenUseClient {
		return c.checkClient(ctx, claims)
	}
	accountID := claims.Subject
	if accountID == "" {
		return invalidToken()
	}
	state, _, err := c.loadState(ctx, accountID)
	if err != nil {
		// loadState 已在缓存读取失败时上报指标并回退 DB；此处错误只代表 DB 回源失败。
		return c.failure(err)
	}
	if state.Status == domain.AccountStatusDisabled {
		return invalidToken()
	}
	version, ok := claimVersion(claims)
	if !ok || version < state.Version {
		return invalidToken()
	}
	if sessionID, ok := claimSessionID(claims); ok && c.sessions != nil {
		if err := c.checkSession(ctx, sessionID); err != nil {
			return err
		}
	}
	if claims.ID != "" {
		// 熔断冷却期内 jti 无法校验且没有 DB 事实源：命中本地宽限则放行，
		// 否则快速失败，禁止继续等待 Redis 超时。
		if c.storeSkipped() {
			if c.tokenRecentlyVerified(claims.ID, claims) {
				return nil
			}
			c.notifyStoreFailure(ctx, "token_load", errRevocationStoreCircuitOpen)
			return c.failure(errRevocationStoreCircuitOpen)
		}
		revoked, err := c.store.IsTokenRevoked(ctx, claims.ID)
		if err != nil {
			c.recordStoreFailure()
			c.notifyStoreFailure(ctx, "token_load", err)
			// Redis 读失败时允许最近一次已验证未撤销的令牌在宽限内继续使用，
			// 避免一次依赖故障放大为全量 503（8.4 R-8.4-01）。
			if c.tokenRecentlyVerified(claims.ID, claims) {
				return nil
			}
			return c.failure(err)
		}
		c.recordStoreSuccess()
		if revoked {
			c.forgetToken(claims.ID)
			return invalidToken()
		}
		c.rememberToken(claims.ID, claims)
	}
	return nil
}

// checkClient 校验 M2M 令牌：客户端存在、状态 active、令牌版本不落后。
//
// 与用户令牌共享 failure_mode 语义：回源数据库失败时 fail_closed 返回 50300，
// fail_open 仅允许非生产环境。会话、jti 黑名单不参与（机器令牌无 sid）。
func (c *RevocationChecker) checkClient(ctx context.Context, claims jwt.Claims) error {
	if c.clients == nil {
		// 装配缺失时失败关闭：宁拒绝机器令牌，也不放行不可撤销的长期凭据。
		return invalidToken()
	}
	clientID := strings.TrimSpace(claims.GetString("client_id"))
	if clientID == "" {
		fromSubject, ok := ClientIDFromSubject(claims.Subject)
		if !ok {
			return invalidToken()
		}
		clientID = fromSubject
	}
	state, err := c.loadClientState(ctx, clientID)
	if err != nil {
		return c.failure(err)
	}
	if state.Status != domain.M2MClientActive {
		return invalidToken()
	}
	version, ok := claimVersion(claims)
	if !ok || version < state.Version {
		return invalidToken()
	}
	return nil
}

// loadClientState 读取客户端快照：命中且未超过陈旧度直接复用，否则回源数据库。
func (c *RevocationChecker) loadClientState(ctx context.Context, clientID string) (ClientState, error) {
	if c.clientStore == nil || c.storeSkipped() {
		return c.refetchClientState(ctx, clientID)
	}
	state, hit, err := c.clientStore.LoadClientState(ctx, clientID)
	if err != nil {
		c.recordStoreFailure()
		c.notifyStoreFailure(ctx, "client_load", err)
		return c.refetchClientState(ctx, clientID)
	}
	c.recordStoreSuccess()
	if hit && c.freshAt(state.FetchedAt) {
		return state, nil
	}
	return c.refetchClientState(ctx, clientID)
}

// refetchClientState 回源数据库并回填客户端快照；同一 client_id 的并发回源合并。
func (c *RevocationChecker) refetchClientState(ctx context.Context, clientID string) (ClientState, error) {
	value, err, _ := c.refetch.Do("client:"+clientID, func() (any, error) {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refetchWorkTimeout)
		defer cancel()
		client, loadErr := c.clients.FindByClientID(workCtx, clientID)
		if loadErr != nil {
			if errors.Is(loadErr, apperr.ErrNotFound) {
				// 不存在视为确定性拒绝，不是依赖故障。
				return ClientState{}, apperr.ErrUnauthorized
			}
			return ClientState{}, loadErr
		}
		state := ClientState{
			Version:   client.TokenVersion,
			Status:    client.Status,
			FetchedAt: c.clock.Now().UTC(),
		}
		if c.clientStore != nil && !c.storeSkipped() {
			if saveErr := c.clientStore.SaveClientState(workCtx, clientID, state, c.stateTTL); saveErr != nil {
				c.notifyStoreFailure(ctx, "client_save", saveErr)
			}
		}
		return state, nil
	})
	if err != nil {
		if errors.Is(err, apperr.ErrUnauthorized) {
			return ClientState{}, invalidToken()
		}
		return ClientState{}, err
	}
	state, ok := value.(ClientState)
	if !ok {
		return ClientState{}, invalidToken()
	}
	return state, nil
}

// loadState 读取账号快照：命中且未超过陈旧度直接复用，否则回源数据库并回填。
//
// Redis 读失败时直接回源 DB（DB 是事实源），并触发熔断计数；连续失败后
// 在冷却窗口内跳过 Redis，避免每个请求重复等待超时。
func (c *RevocationChecker) loadState(ctx context.Context, accountID string) (AccountState, bool, error) {
	if c.storeSkipped() {
		return c.refetchState(ctx, accountID)
	}
	state, hit, err := c.store.LoadAccountState(ctx, accountID)
	if err != nil {
		c.recordStoreFailure()
		c.notifyStoreFailure(ctx, "account_load", err)
		return c.refetchState(ctx, accountID)
	}
	c.recordStoreSuccess()
	if hit && c.isFresh(state) {
		return state, true, nil
	}
	return c.refetchState(ctx, accountID)
}

// isFresh 报告快照是否仍在允许陈旧度内；零值 FetchedAt 视为陈旧（旧格式缓存）。
func (c *RevocationChecker) isFresh(state AccountState) bool {
	return c.freshAt(state.FetchedAt)
}

// freshAt 报告回源时间是否仍在允许陈旧度内；零值视为陈旧（旧格式缓存）。
func (c *RevocationChecker) freshAt(fetchedAt time.Time) bool {
	if fetchedAt.IsZero() {
		return false
	}
	elapsed := c.clock.Now().Sub(fetchedAt)
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
		// 回源与调用方 context 解耦：首个请求取消 / 超时不得让同 key 的等待者一起失败。
		// 工作 context 只继承值并附加独立超时；调用方在拿到结果后自行判断是否已取消。
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refetchWorkTimeout)
		defer cancel()
		// 获得执行权后二次检查，避免慢回源期间的其它请求刚写入新快照仍被覆盖。
		// 熔断期内跳过 Redis 读写：读取失败时直接回源 DB（DB 才是事实源），
		// 回填也跳过，避免 Redis 故障时每个请求仍等待写超时（REL-01）。
		skipStore := c.storeSkipped()
		if !skipStore {
			if loaded, hit, loadErr := c.store.LoadAccountState(workCtx, accountID); loadErr == nil && hit && c.isFresh(loaded) {
				c.recordStoreSuccess()
				return loaded, nil
			}
		}
		account, dbErr := c.accounts.FindByID(workCtx, accountID)
		if dbErr != nil {
			return nil, dbErr
		}
		c.notifyDBRefetch(ctx, "account")
		state := AccountState{
			Version:   account.TokenVersion,
			Status:    account.Status,
			FetchedAt: c.clock.Now(),
		}
		if !skipStore {
			if saveErr := c.store.SaveAccountState(workCtx, accountID, state, c.stateTTL); saveErr != nil {
				c.notifyWriteFailure(ctx, "set", saveErr)
			}
		}
		return state, nil
	})
	if err != nil {
		return AccountState{}, false, err
	}
	// 调用方自身已取消：不再继续使用共享结果，由上层按请求生命周期处理。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return AccountState{}, false, ctxErr
	}
	state, ok := value.(AccountState)
	if !ok {
		return AccountState{}, false, apperr.Internal("revocation checker: unexpected state type", nil)
	}
	return state, true, nil
}

// checkSession 校验 Access Token 的 sid 对应会话是否已被撤销（IDN-P1-02）。
func (c *RevocationChecker) checkSession(ctx context.Context, sessionID string) error {
	state, err := c.loadSessionState(ctx, sessionID)
	if err != nil {
		// 会话不存在视为确定性拒绝（已撤销且被清理），不是存储故障。
		if !errors.Is(err, apperr.ErrNotFound) {
			c.notifyStoreFailure(ctx, "session_load", err)
		}
		return c.failure(err)
	}
	if state.Revoked(c.clock.Now()) {
		return invalidToken()
	}
	return nil
}

// loadSessionState 读取会话快照：命中且未超过陈旧度直接复用，否则回源数据库并回填。
//
// 与账号快照一致：Redis 读失败回退 DB；连续失败后短时跳过 Redis。
func (c *RevocationChecker) loadSessionState(ctx context.Context, sessionID string) (SessionState, error) {
	if c.storeSkipped() {
		return c.refetchSessionState(ctx, sessionID)
	}
	state, hit, err := c.store.LoadSessionState(ctx, sessionID)
	if err != nil {
		c.recordStoreFailure()
		c.notifyStoreFailure(ctx, "session_load", err)
		return c.refetchSessionState(ctx, sessionID)
	}
	c.recordStoreSuccess()
	if hit && c.freshAt(state.FetchedAt) {
		return state, nil
	}
	return c.refetchSessionState(ctx, sessionID)
}

// refetchSessionState 回源数据库并回填会话快照；同一会话的并发回源合并为一次。
//
// 会话不存在视为确定性拒绝：撤销后即使被清理任务删除也不能放行。
func (c *RevocationChecker) refetchSessionState(ctx context.Context, sessionID string) (SessionState, error) {
	value, err, _ := c.refetch.Do("sid:"+sessionID, func() (any, error) {
		// 获得执行权后二次检查，避免慢回源期间的其它请求刚写入新快照仍被覆盖。
		// 熔断期内跳过 Redis 读写，避免 Redis 故障时请求仍被写超时拖住（REL-01）。
		skipStore := c.storeSkipped()
		if !skipStore {
			if loaded, hit, loadErr := c.store.LoadSessionState(ctx, sessionID); loadErr == nil && hit && c.freshAt(loaded.FetchedAt) {
				c.recordStoreSuccess()
				return loaded, nil
			}
		}
		session, dbErr := c.sessions.FindByID(ctx, sessionID)
		if dbErr != nil {
			return nil, dbErr
		}
		c.notifyDBRefetch(ctx, "session")
		state := SessionState{
			Status:    session.Status,
			RevokedAt: timeOrZero(session.RevokedAt),
			ExpiresAt: session.ExpiresAt,
			FetchedAt: c.clock.Now(),
		}
		if !skipStore {
			if saveErr := c.store.SaveSessionState(ctx, sessionID, state, c.stateTTL); saveErr != nil {
				c.notifyWriteFailure(ctx, "session_set", saveErr)
			}
		}
		return state, nil
	})
	if err != nil {
		return SessionState{}, err
	}
	state, ok := value.(SessionState)
	if !ok {
		return SessionState{}, apperr.Internal("revocation checker: unexpected session state type", nil)
	}
	return state, nil
}

// storeSkipped 报告撤销存储是否处于熔断冷却期。
func (c *RevocationChecker) storeSkipped() bool {
	c.breakerMu.Lock()
	defer c.breakerMu.Unlock()
	if c.storeOpenUntil.IsZero() {
		return false
	}
	if c.clock.Now().Before(c.storeOpenUntil) {
		return true
	}
	// 冷却结束：重新尝试 Redis，失败则再次进入冷却。
	c.storeOpenUntil = time.Time{}
	c.storeFailures = 0
	return false
}

// recordStoreFailure 记录一次撤销存储失败；连续失败达到阈值后打开冷却窗口。
func (c *RevocationChecker) recordStoreFailure() {
	c.breakerMu.Lock()
	defer c.breakerMu.Unlock()
	c.storeFailures++
	if c.storeFailures >= revocationStoreBreakerThreshold {
		c.storeOpenUntil = c.clock.Now().Add(revocationStoreBreakerCooldown)
	}
}

// recordStoreSuccess 重置撤销存储失败计数。
func (c *RevocationChecker) recordStoreSuccess() {
	c.breakerMu.Lock()
	defer c.breakerMu.Unlock()
	c.storeFailures = 0
	c.storeOpenUntil = time.Time{}
}

// rememberToken 记录一次「已验证未撤销」的 jti，有效期取 min(宽限, 令牌剩余寿命)。
func (c *RevocationChecker) rememberToken(jti string, claims jwt.Claims) {
	if c.tokenGrace <= 0 {
		return
	}
	now := c.clock.Now()
	until := now.Add(c.tokenGrace)
	if !claims.ExpiresAt.IsZero() && claims.ExpiresAt.Before(until) {
		until = claims.ExpiresAt
	}
	if !until.After(now) {
		return
	}
	c.tokens.Store(jti, until)
	if c.tokenSeen.Add(1) >= revocationTokenCacheSweep {
		c.tokenSeen.Store(0)
		c.sweepTokens(now)
	}
}

// tokenRecentlyVerified 报告 jti 是否在本地宽限内验证过未撤销且令牌未过期。
func (c *RevocationChecker) tokenRecentlyVerified(jti string, claims jwt.Claims) bool {
	if c.tokenGrace <= 0 {
		return false
	}
	now := c.clock.Now()
	if !claims.ExpiresAt.IsZero() && !now.Before(claims.ExpiresAt) {
		return false
	}
	value, ok := c.tokens.Load(jti)
	if !ok {
		return false
	}
	until, ok := value.(time.Time)
	if !ok {
		c.tokens.Delete(jti)
		return false
	}
	if now.Before(until) {
		return true
	}
	c.tokens.Delete(jti)
	return false
}

// forgetToken 在发现 jti 已撤销时清除本地记录，避免宽限期内被误放行。
func (c *RevocationChecker) forgetToken(jti string) { c.tokens.Delete(jti) }

// sweepTokens 清理已过期的本地记录；由写入计数触发，避免单独起后台协程。
func (c *RevocationChecker) sweepTokens(now time.Time) {
	c.tokens.Range(func(key, value any) bool {
		until, ok := value.(time.Time)
		if !ok || !now.Before(until) {
			c.tokens.Delete(key)
		}
		return true
	})
}

// notifyWriteFailure 上报快照写入失败；观测器缺失时静默，由 TTL 兜底。
func (c *RevocationChecker) notifyWriteFailure(ctx context.Context, op string, err error) {
	if c.observer == nil {
		return
	}
	c.observer.OnAccountStateWriteFailure(ctx, op, err)
}

// notifyStoreFailure 上报撤销存储读 / 回源失败；观测器缺失时静默。
func (c *RevocationChecker) notifyStoreFailure(ctx context.Context, op string, err error) {
	if c.observer == nil {
		return
	}
	c.observer.OnRevocationStoreFailure(ctx, op, err)
}

// notifyDBRefetch 上报一次数据库回源；仅在 Redis 未命中 / 陈旧时触发。
func (c *RevocationChecker) notifyDBRefetch(ctx context.Context, kind string) {
	if c.cacheObserver == nil {
		return
	}
	c.cacheObserver.OnRevocationDBRefetch(ctx, kind)
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

// claimSessionID 从 sid 声明读取会话主键；缺失或空串表示无法做会话校验。
func claimSessionID(claims jwt.Claims) (string, bool) {
	if claims.Extra == nil {
		return "", false
	}
	raw, ok := claims.Extra["sid"]
	if !ok || raw == nil {
		return "", false
	}
	sid, ok := raw.(string)
	if !ok {
		return "", false
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return "", false
	}
	return sid, true
}

// timeOrZero 解引用可空时间；nil 返回零值，避免把「未撤销」误判为已撤销。
func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

// invalidToken 构造统一的 40100 错误（不暴露具体撤销原因）。
func invalidToken() error {
	return apperr.Unauthorized("invalid token")
}
