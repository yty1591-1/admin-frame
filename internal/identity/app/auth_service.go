package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// DefaultSessionIdleTimeout 是默认空闲会话上限（docs/identity-authn.md §10）。
const DefaultSessionIdleTimeout = 72 * time.Hour

// DefaultSessionAbsoluteTTL 是设备默认绝对生命上限（30 天，IDN-P1-07）。
const DefaultSessionAbsoluteTTL = 720 * time.Hour

// DefaultMaxSessionsPerAccount 是单账号默认活跃设备上限。
const DefaultMaxSessionsPerAccount = 6

// 认证输入长度上限（SEC-03）：在昂贵计算或外部调用前拒绝异常输入。
const (
	// maxLoginSubjectBytes 与 identity_account.subject_index 的来源列宽一致。
	maxLoginSubjectBytes = 255
	// maxLoginPasswordBytes 远大于密码策略上限，仅用于阻断超大输入。
	maxLoginPasswordBytes = 4096
	// maxRefreshTokenBytes 刷新令牌长度上限。
	maxRefreshTokenBytes = 512
	// maxOAuthCodeBytes OAuth 授权码长度上限。
	maxOAuthCodeBytes = 2048
	// maxOAuthStateBytes OAuth state 长度上限。
	maxOAuthStateBytes = 1024
)

// AuthServiceConfig 是认证用例的装配参数。
//
// Channel 在装配期固定，禁止由 HTTP 请求指定，避免端别被请求参数劫持。
type AuthServiceConfig struct {
	Accounts    domain.AccountRepository
	Credentials domain.CredentialRepository
	Sessions    domain.SessionRepository
	Guard       *LoginGuard
	Password    *PasswordService
	Tokens      *TokenService
	Revocation  RevocationStore
	// RevocationStateTTL 账号撤销快照最大陈旧度；≤0 使用 DefaultAccountStateTTL。
	RevocationStateTTL time.Duration
	// RevocationObserver 接收撤销快照写入失败；可为 nil。
	RevocationObserver RevocationObserver
	// Metrics 是核心指标端口；可为 nil（全部上报降级为 no-op）。
	Metrics Metrics
	// Hasher 是 PII / 验证码摘要器；nil 时回退历史无盐算法（仅限本地/tests）。
	Hasher *crypto.Hasher
	// Logger 仅用于「业务已提交、缓存失效失败」的告警日志；可为 nil。
	Logger  *slog.Logger
	Audit   audit.Emitter
	IDs     IDGenerator
	Channel domain.Channel
	// UoW 是事务边界；nil 退化为 NoopUnitOfWork（内存模式与单测）。
	UoW UnitOfWork
	// MFA 是 TOTP 绑定仓储；admin_required=true 时必填。
	MFA domain.MFAEnrollmentRepository
	// MFACipher 是 TOTP 密钥的 AES-256-GCM 加解密器；未配置时 MFA 端点失败关闭。
	MFACipher MFASecretCipher
	// MFATickets 是两步登录票据的一次性消费存储。
	MFATickets MFATicketStore
	// MFAIssuer 写入 otpauth URI；空值使用 DefaultMFAIssuer。
	MFAIssuer string
	// MFAAdminRequired 表示 A 端强制绑定 MFA。
	MFAAdminRequired bool
	// IdleTimeout 会话空闲上限；≤0 时使用 72h。
	IdleTimeout time.Duration
	// SessionAbsoluteTTL 设备绝对生命上限；≤0 时使用 DefaultSessionAbsoluteTTL。
	// 刷新只能在该上限内滑动续期（IDN-P1-07）。
	SessionAbsoluteTTL time.Duration
	// MaxPerAccount 单账号活跃设备上限；≤0 使用 DefaultMaxSessionsPerAccount。
	MaxPerAccount int
	// RefreshGrace 并发刷新宽限窗口；≤0 表示禁用（REL-06）。
	RefreshGrace time.Duration
	// Service 写入审计事件的 service 字段。
	Service string
	// ActorType 写入审计事件的 actor_type；空值时按端别推导。
	ActorType string
	Clock     Clock
}

// LoginInput 是密码登录入参。
type LoginInput struct {
	// Subject 登录主体（A/B 端为账号 / 邮箱）。
	Subject string
	// MerchantID B 端商户 ID；B 端必填，A/C 端忽略。
	MerchantID string
	// Password 明文密码；只在本层短暂存在，禁止写日志。
	Password string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生，仅用于会话展示。
	Device domain.DeviceInfo
	// TraceID 请求关联 ID。
	TraceID string
}

// LoginResult 是登录成功后的令牌与会话信息。
type LoginResult struct {
	TokenPair
	// SessionID 本次登录创建的会话。
	SessionID string
	// AccountID 账号主键。
	AccountID string
	// MFARequired 表示密码已通过、但需要完成 MFA 才能获得令牌。
	MFARequired bool
	// MFATicket 是两步登录票据；仅在 MFARequired 时返回。
	MFATicket string
	// MFABindingRequired 表示密码已通过、但账号尚未绑定 MFA 且 A 端强制绑定（§7.2.1）。
	MFABindingRequired bool
	// MFASetupTicket 是强制绑定票据；仅在 MFABindingRequired 时返回。
	MFASetupTicket string
	// PasswordChangeRequired 表示管理员重置密码后必须先完成改密。
	PasswordChangeRequired bool
	// PasswordChangeTicket 是强制改密票据；仅在 PasswordChangeRequired 时返回。
	PasswordChangeTicket string
	// activatedSession 是成功创建会话时的撤销快照，仅供提交后写缓存，不对外暴露。
	activatedSession *SessionState
}

// RefreshInput 是刷新令牌入参。
type RefreshInput struct {
	RefreshToken string
	TraceID      string
	// Device 当次刷新请求派生出的画像；仅用于补齐历史会话缺失的设备信息。
	Device domain.DeviceInfo
}

// SessionActor 是需要认证上下文的用例入参。
type SessionActor struct {
	AccountID      string
	SessionID      string
	TokenID        string
	TokenExpiresAt time.Time
	MerchantID     string
	TraceID        string
}

// SessionView 是会话列表的对外视图；不包含任何令牌信息。
type SessionView struct {
	ID         string
	Current    bool
	Status     string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	// AbsoluteExpiresAt 设备绝对到期时间；刷新不得延长。
	AbsoluteExpiresAt time.Time
	IdleExpiresAt     time.Time
	RevokedAt         *time.Time
	// DeviceLabel / DeviceType / OS / Browser / IPMasked 设备画像；供前端展示设备。
	DeviceLabel string
	DeviceType  string
	OS          string
	Browser     string
	IPMasked    string
}

// MeView 是当前登录主体资料的对外视图。
//
// 只承载身份域字段；角色、权限与数据范围仍由 IAM 的 /iam/me/permissions 提供。
type MeView struct {
	// SubjectID 账号主键，与令牌 sub 一致。
	SubjectID string
	// Subject 规范化登录主体（trim + 小写）。
	Subject string
	// DisplayName 展示昵称；空串表示未设置，前端回退 Subject。
	DisplayName string
	// Channel 账号所属端别；必须与装配端别一致。
	Channel domain.Channel
	// MerchantID B 端商户 ID；A / C 端为空串。
	MerchantID string
	// Status 账号生命周期状态。
	Status domain.AccountStatus
	// MFAEnabled 表示 MFA 绑定是否已确认。
	MFAEnabled bool
	// HasPassword 表示账号已设置密码凭据。
	HasPassword bool
	// PasswordUpdatedAt 最近一次改密时间；nil 表示从未设置密码或从未改密。
	PasswordUpdatedAt *time.Time
	// MustChangePassword 表示管理员重置密码后尚未完成改密。
	MustChangePassword bool
	// PhoneBound 表示账号已绑定手机号凭据；号码本身不可回显。
	PhoneBound bool
	// TokenVersion 账号级撤销版本，只读。
	TokenVersion int64
}

// AuthService 编排登录、刷新、登出与会话撤销（docs/identity-authn.md §5）。
type AuthService struct {
	accounts     domain.AccountRepository
	credentials  domain.CredentialRepository
	sessions     domain.SessionRepository
	guard        *LoginGuard
	password     *PasswordService
	tokens       *TokenService
	revocation   RevocationStore
	invalidation *RevocationInvalidator
	// sessionStateTTL 会话撤销快照最大陈旧度；与账号快照共享 ≤60s 上限。
	sessionStateTTL time.Duration
	observer        RevocationObserver
	metrics         Metrics
	hasher          *crypto.Hasher
	logger          *slog.Logger
	audit           audit.Emitter
	ids             IDGenerator
	channel         domain.Channel
	uow             UnitOfWork
	idleTimeout     time.Duration
	absoluteTTL     time.Duration
	maxPerAccount   int
	refreshGrace    time.Duration
	service         string
	actorType       string
	clock           Clock
	mfa             domain.MFAEnrollmentRepository
	mfaCipher       MFASecretCipher
	mfaTickets      MFATicketStore
	mfaIssuer       string
	mfaRequired     bool
}

// NewAuthService 构造认证用例服务；核心依赖缺失时返回错误。
func NewAuthService(cfg AuthServiceConfig) (*AuthService, error) {
	if cfg.Accounts == nil || cfg.Credentials == nil || cfg.Sessions == nil {
		return nil, apperr.Internal("auth service repositories are required", nil)
	}
	if cfg.Guard == nil || cfg.Password == nil || cfg.Tokens == nil || cfg.IDs == nil {
		return nil, apperr.Internal("auth service dependencies are required", nil)
	}
	if err := cfg.Channel.Validate(); err != nil {
		return nil, err
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = DefaultSessionIdleTimeout
	}
	absoluteTTL := cfg.SessionAbsoluteTTL
	if absoluteTTL <= 0 {
		absoluteTTL = DefaultSessionAbsoluteTTL
	}
	maxPerAccount := cfg.MaxPerAccount
	if maxPerAccount <= 0 {
		maxPerAccount = DefaultMaxSessionsPerAccount
	}
	actorType := cfg.ActorType
	if actorType == "" {
		switch cfg.Channel {
		case domain.ChannelBiz:
			actorType = "merchant_staff"
		case domain.ChannelC:
			actorType = "consumer"
		default:
			actorType = "staff"
		}
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	if cfg.Channel == domain.ChannelAdmin && cfg.MFAAdminRequired {
		if cfg.MFA == nil || cfg.MFACipher == nil || cfg.MFATickets == nil {
			return nil, apperr.Internal("mfa dependencies are required when admin_required=true", nil)
		}
	}
	mfaIssuer := strings.TrimSpace(cfg.MFAIssuer)
	if mfaIssuer == "" {
		mfaIssuer = DefaultMFAIssuer
	}
	uow := cfg.UoW
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	var invalidation *RevocationInvalidator
	if cfg.Revocation != nil {
		var err error
		invalidation, err = NewRevocationInvalidator(
			cfg.Revocation,
			cfg.RevocationStateTTL,
			cfg.RevocationObserver,
			WithInvalidatorClock(clock),
		)
		if err != nil {
			return nil, err
		}
	}
	svc := &AuthService{
		accounts:        cfg.Accounts,
		credentials:     cfg.Credentials,
		sessions:        cfg.Sessions,
		guard:           cfg.Guard,
		password:        cfg.Password,
		tokens:          cfg.Tokens,
		revocation:      cfg.Revocation,
		invalidation:    invalidation,
		sessionStateTTL: clampAccountStateTTL(cfg.RevocationStateTTL),
		observer:        cfg.RevocationObserver,
		metrics:         cfg.Metrics,
		hasher:          cfg.Hasher,
		logger:          cfg.Logger,
		audit:           cfg.Audit,
		ids:             cfg.IDs,
		channel:         cfg.Channel,
		uow:             uow,
		idleTimeout:     idle,
		absoluteTTL:     absoluteTTL,
		maxPerAccount:   maxPerAccount,
		refreshGrace:    cfg.RefreshGrace,
		service:         cfg.Service,
		actorType:       actorType,
		clock:           clock,
		mfa:             cfg.MFA,
		mfaCipher:       cfg.MFACipher,
		mfaTickets:      cfg.MFATickets,
		mfaIssuer:       mfaIssuer,
		mfaRequired:     cfg.MFAAdminRequired,
	}
	return svc, nil
}

// withinTx 在事务中执行 fn；未配置 UnitOfWork 时直接执行（内存仓储无法回滚）。
func (s *AuthService) withinTx(ctx context.Context, fn func(context.Context) error) error {
	if s.uow == nil {
		return fn(ctx)
	}
	return s.uow.WithinTx(ctx, fn)
}

// runTx 统一事务语义：基础设施错误回滚并返回；401 业务拒绝提交后返回原错误。
//
// 失败路径（登录失败尝试、重放撤销、MFA 失败）必须先落库再返回 401，
// 因此拒绝错误在事务内被吞掉以触发提交，事务外再原样返回。
func (s *AuthService) runTx(ctx context.Context, fn func(context.Context) error) error {
	var rejection error
	err := s.withinTx(ctx, func(txCtx context.Context) error {
		if fnErr := fn(txCtx); fnErr != nil {
			if isRejected(fnErr) {
				rejection = fnErr
				return nil
			}
			return fnErr
		}
		return nil
	})
	if err != nil {
		return err
	}
	return rejection
}

// isRejected 报告错误是否为需要落库的业务拒绝（统一为 40100）。
func isRejected(err error) bool {
	return err != nil && errors.Is(err, apperr.ErrUnauthorized)
}

// Login 校验密码并签发令牌；失败时不创建会话。
//
// 两阶段设计（IDN-GAP-P1-01）：
//  1. probeLogin 在事务外完成限流、账号/凭据读取与 Argon2 校验，避免重 CPU
//     计算占用数据库事务连接；
//  2. 事务内重新加载账号与凭据并复核哈希/状态，再写登录尝试、审计、会话。
//
// 账号不存在、密码错误、停用与锁定统一返回 40100 文案，真实原因只写审计。
func (s *AuthService) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	started := s.clock.Now()
	probe, err := s.probeLogin(ctx, in)
	if err != nil {
		s.observeLogin(ctx, LoginResult{}, err, started)
		return LoginResult{}, err
	}
	var result LoginResult
	txStarted := s.clock.Now()
	err = s.runTx(ctx, func(txCtx context.Context) error {
		if probe.failure != nil {
			return s.recordLoginFailureInTx(txCtx, probe, in)
		}
		res, completeErr := s.completeProbeInTx(txCtx, probe, in)
		if completeErr != nil {
			return completeErr
		}
		result = res
		return nil
	})
	if s.metrics != nil {
		s.metrics.ObserveLoginTxDuration(ctx, s.channel, s.clock.Now().Sub(txStarted))
	}
	s.observeLogin(ctx, result, err, started)
	if err != nil {
		return LoginResult{}, err
	}
	// 会话快照必须在事务提交后写入：回滚的事务不得留下 active 缓存（IDN-GAP-PERF-02）。
	s.markActivatedSession(ctx, result.SessionID, result.activatedSession)
	return result, nil
}

// observeLogin 上报登录结果；失败不影响主流程。
func (s *AuthService) observeLogin(ctx context.Context, result LoginResult, err error, started time.Time) {
	if s.metrics == nil {
		return
	}
	duration := s.clock.Now().Sub(started)
	status, reason := MetricResultSuccess, MetricReasonOK
	switch {
	case err != nil:
		status, reason = MetricResultFailure, loginFailureReason(err)
	case result.PasswordChangeRequired:
		status, reason = MetricResultChallenge, MetricReasonPasswordChangeRequired
	case result.MFABindingRequired:
		status, reason = MetricResultChallenge, MetricReasonMFABindingRequired
	case result.MFARequired:
		status, reason = MetricResultChallenge, MetricReasonMFARequired
	}
	s.metrics.ObserveLogin(ctx, s.channel, status, reason, duration)
}

// loginFailureReason 把错误码映射为低基数原因标签；不泄露具体账号状态给客户端。
func loginFailureReason(err error) string {
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case apperr.CodeTooManyRequests:
			return MetricReasonRateLimited
		case apperr.CodeServiceUnavailable:
			return MetricReasonUnavailable
		case apperr.CodeUnauthorized:
			return MetricReasonBadCredential
		}
	}
	return MetricReasonInternal
}

// loginProbe 是事务外密码校验的结果，供事务内复核与收口。
type loginProbe struct {
	// account 事务外加载到的账号；账号不存在时为 nil。
	account *domain.Account
	// accountRef 账号主体摘要，用于审计关联。
	accountRef string
	// accountFound 报告账号是否存在。
	accountFound bool
	// credentialHash 事务外读取到的密码哈希，用于事务内比对是否被并发修改。
	credentialHash string
	// rehashEncoded / rehashAlgorithm 事务外计算的新哈希，事务内直接落库。
	rehashEncoded   string
	rehashAlgorithm string
	// failure 需要落库后返回的业务失败（401/500）；nil 表示密码校验通过。
	failure error
	// reason 审计原因；success 时为空。
	reason string
	// countFailure 报告本次失败是否计入锁定阈值。
	countFailure bool
}

// probeLogin 在事务外完成限流、账号/凭据读取与 Argon2 校验。
//
// 直接返回的错误（限流、依赖不可用、内部读取失败）不产生登录尝试写入；
// 业务失败封装在 probe.failure 中，由事务内统一写 attempt/审计后返回。
func (s *AuthService) probeLogin(ctx context.Context, in LoginInput) (*loginProbe, error) {
	subject := domain.NormalizeSubject(in.Subject)
	if subject == "" || strings.TrimSpace(in.Password) == "" {
		return nil, invalidCredentials()
	}
	// SEC-03：在 Argon2 之前拒绝异常长度的主体 / 口令，避免大输入放大 CPU 与内存。
	if len(subject) > maxLoginSubjectBytes || len(in.Password) > maxLoginPasswordBytes {
		return nil, invalidCredentials()
	}
	merchantID := strings.TrimSpace(in.MerchantID)
	if s.channel == domain.ChannelBiz && merchantID == "" {
		return nil, invalidCredentials()
	}
	if s.channel != domain.ChannelBiz {
		if merchantID != "" {
			return nil, apperr.BadRequest("merchant_id is not allowed on this channel")
		}
		merchantID = ""
	}
	accountRef := AccountRef(s.hasher, s.channel, subject)
	rateKeys := []string{loginRateKey(s.channel, accountRef, "")}
	if in.IPHash != "" {
		rateKeys = append(rateKeys, loginRateKey(s.channel, "", in.IPHash))
	}
	if err := s.guard.CheckRateLimit(rateKeys...); err != nil {
		// 限流拒绝是安全事件；审计失败不把 429 变成 503。
		_ = s.emitAudit(ctx, audit.ActionAuthRateLimited, accountRef, merchantID, "account", accountRef,
			audit.OutcomeDenied, MetricReasonRateLimited, in.TraceID, nil)
		return nil, err
	}

	account, err := s.accounts.FindBySubject(ctx, s.channel, merchantID, subject)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if dummyErr := s.password.VerifyDummyContext(ctx); dummyErr != nil {
				return nil, dummyErr
			}
			return &loginProbe{
				accountRef:   accountRef,
				accountFound: false,
				failure:      invalidCredentials(),
				reason:       domain.LoginReasonBadCredential,
			}, nil
		}
		return nil, apperr.Internal("load account failed", err)
	}

	if err := s.guard.Authorize(account, s.clock.Now().UTC()); err != nil {
		if dummyErr := s.password.VerifyDummyContext(ctx); dummyErr != nil {
			return nil, dummyErr
		}
		return &loginProbe{
			account:      account,
			accountRef:   accountRef,
			accountFound: true,
			failure:      err,
			reason:       reasonFromAuthError(err),
			countFailure: true,
		}, nil
	}

	credential, err := s.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if dummyErr := s.password.VerifyDummyContext(ctx); dummyErr != nil {
				return nil, dummyErr
			}
			return &loginProbe{
				account:      account,
				accountRef:   accountRef,
				accountFound: true,
				failure:      invalidCredentials(),
				reason:       domain.LoginReasonBadCredential,
				countFailure: true,
			}, nil
		}
		return nil, apperr.Internal("load credential failed", err)
	}

	needsRehash, err := s.password.VerifyContext(ctx, credential.Hash, in.Password)
	if err != nil {
		if errors.Is(err, apperr.ErrUnavailable) {
			return nil, err
		}
		return &loginProbe{
			account:        account,
			accountRef:     accountRef,
			accountFound:   true,
			credentialHash: credential.Hash,
			failure:        err,
			reason:         domain.LoginReasonBadCredential,
			countFailure:   true,
		}, nil
	}

	probe := &loginProbe{
		account:        account,
		accountRef:     accountRef,
		accountFound:   true,
		credentialHash: credential.Hash,
	}
	if needsRehash {
		encoded, algorithm, hashErr := s.password.HashContext(ctx, in.Password)
		if hashErr != nil {
			return nil, hashErr
		}
		probe.rehashEncoded = encoded
		probe.rehashAlgorithm = algorithm
	}
	return probe, nil
}

// recordLoginFailureInTx 在事务内写入失败的登录尝试与审计。
//
// 密码哈希已在 probeLogin 完成，本方法不再执行 Argon2。
func (s *AuthService) recordLoginFailureInTx(ctx context.Context, probe *loginProbe, in LoginInput) error {
	now := s.clock.Now().UTC()
	if !probe.accountFound || probe.account == nil {
		if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, probe.accountRef, "", "account", probe.accountRef,
			audit.OutcomeFailure, probe.reason, in.TraceID, nil); auditErr != nil {
			return auditErr
		}
		return probe.failure
	}

	account, err := s.accounts.FindByID(ctx, probe.account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, probe.account.ID, probe.account.MerchantID,
				"account", probe.account.ID, audit.OutcomeFailure, probe.reason, in.TraceID, nil); auditErr != nil {
				return auditErr
			}
			return probe.failure
		}
		return apperr.Internal("load account failed", err)
	}

	var lockedNow bool
	if probe.countFailure {
		lockedNow, err = s.guard.RecordFailure(ctx, account, normalizeReason(probe.reason), in.IPHash, in.UAHash, now)
		if err != nil {
			return apperr.Internal("record login failure failed", err)
		}
	}
	if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeFailure, normalizeReason(probe.reason), in.TraceID, nil); auditErr != nil {
		return auditErr
	}
	if lockedNow {
		if lockedErr := s.emitAccountLocked(ctx, account, in.TraceID); lockedErr != nil {
			return lockedErr
		}
	}
	return probe.failure
}

// completeProbeInTx 在事务内复核账号/凭据状态并完成登录收口。
//
// 复核点：账号仍存在且可登录、密码哈希未被并发修改；复核通过后才允许
// 重哈希、激活 pending、下发改密/MFA 票据或创建会话。
func (s *AuthService) completeProbeInTx(ctx context.Context, probe *loginProbe, in LoginInput) (LoginResult, error) {
	now := s.clock.Now().UTC()
	account, err := s.accounts.FindByID(ctx, probe.account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, s.auditLoginStateChanged(ctx, probe.account, in.TraceID, domain.LoginReasonBadCredential, invalidCredentials())
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		reason := reasonFromAuthError(err)
		if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
			audit.OutcomeFailure, reason, in.TraceID, nil); auditErr != nil {
			return LoginResult{}, auditErr
		}
		return LoginResult{}, err
	}

	credential, err := s.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, s.auditLoginStateChanged(ctx, account, in.TraceID, domain.LoginReasonBadCredential, invalidCredentials())
		}
		return LoginResult{}, apperr.Internal("load credential failed", err)
	}
	if credential.Hash != probe.credentialHash {
		// probe 与实际写入之间密码被修改：拒绝本次登录，避免签发旧凭据会话。
		return LoginResult{}, s.auditLoginStateChanged(ctx, account, in.TraceID, domain.LoginReasonBadCredential, invalidCredentials())
	}

	if probe.rehashEncoded != "" {
		if replaceErr := credential.ReplaceHash(probe.rehashEncoded, probe.rehashAlgorithm, now); replaceErr != nil {
			return LoginResult{}, replaceErr
		}
		if saveErr := s.credentials.Save(ctx, credential); saveErr != nil {
			return LoginResult{}, apperr.Internal("upgrade credential hash failed", saveErr)
		}
	}
	if account.Status == domain.AccountStatusPending {
		expected := account.Version
		if activateErr := account.Activate(now); activateErr != nil {
			return LoginResult{}, activateErr
		}
		if saveErr := s.accounts.Save(ctx, account, expected); saveErr != nil {
			return LoginResult{}, apperr.Internal("activate account failed", saveErr)
		}
	}
	if account.MustChangePassword {
		ticket, issueErr := s.tokens.IssuePasswordChangeTicket(account.ID)
		if issueErr != nil {
			return LoginResult{}, issueErr
		}
		return LoginResult{
			AccountID:              account.ID,
			PasswordChangeRequired: true,
			PasswordChangeTicket:   ticket.Ticket,
		}, nil
	}
	return s.challengeOrComplete(ctx, account, now, in.IPHash, in.UAHash, in.Device, in.TraceID)
}

// auditLoginStateChanged 写登录失败审计并返回给定错误。
func (s *AuthService) auditLoginStateChanged(ctx context.Context, account *domain.Account, traceID, reason string, failure error) error {
	if account == nil {
		return failure
	}
	if err := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeFailure, reason, traceID, nil); err != nil {
		return err
	}
	return failure
}

// challengeOrComplete 在密码 / 改密票据通过后执行 MFA 闸门或直接完成登录。
func (s *AuthService) challengeOrComplete(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	ipHash, uaHash string,
	device domain.DeviceInfo,
	traceID string,
) (LoginResult, error) {
	gate, err := s.mfaChallenge(ctx, account, traceID)
	if err != nil {
		return LoginResult{}, err
	}
	switch gate {
	case mfaGateBind:
		ticket, issueErr := s.tokens.IssueMFASetupTicket(account.ID)
		if issueErr != nil {
			return LoginResult{}, issueErr
		}
		return LoginResult{
			AccountID:          account.ID,
			MFABindingRequired: true,
			MFASetupTicket:     ticket.Ticket,
		}, nil
	case mfaGateVerify:
		ticket, issueErr := s.tokens.IssueMFATicket(account.ID)
		if issueErr != nil {
			return LoginResult{}, issueErr
		}
		return LoginResult{
			AccountID:   account.ID,
			MFARequired: true,
			MFATicket:   ticket.Ticket,
		}, nil
	default:
		return s.completeLogin(ctx, account, now, ipHash, uaHash, device, traceID)
	}
}

// IssuePasswordChangeTicket 签发强制改密票据（IDN-P0-02）。
func (s *TokenService) IssuePasswordChangeTicket(accountID string) (MFATicket, error) {
	return s.issueScopedTicket(accountID, passwordChangeTicketScope, PasswordChangeTicketTTL)
}

// ParsePasswordChangeTicket 校验并解析强制改密票据。
func (s *TokenService) ParsePasswordChangeTicket(raw string) (jwt.Claims, error) {
	return s.parseScopedTicket(raw, passwordChangeTicketScope)
}

// VerifiedLoginInput 是外部验证（短信验证码 / OAuth）通过后的登录入参。
type VerifiedLoginInput struct {
	// AccountID 已通过外部验证的账号主键。
	AccountID string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生，仅用于会话展示。
	Device domain.DeviceInfo
	// TraceID 请求关联 ID。
	TraceID string
}

// CompleteVerifiedLogin 在短信 / OAuth 外部验证通过后完成登录。
//
// 只接受本端别账号；锁定 / 停用与密码登录一致地返回 40100 并记录失败。
// C 端不触发 MFA。
func (s *AuthService) CompleteVerifiedLogin(ctx context.Context, in VerifiedLoginInput) (LoginResult, error) {
	var result LoginResult
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.completeVerifiedLoginInTx(txCtx, in)
		if err != nil {
			return err
		}
		result = res
		return nil
	}); err != nil {
		return LoginResult{}, err
	}
	s.markActivatedSession(ctx, result.SessionID, result.activatedSession)
	return result, nil
}

// completeVerifiedLoginInTx 是 CompleteVerifiedLogin 的事务内实现。
func (s *AuthService) completeVerifiedLoginInTx(ctx context.Context, in VerifiedLoginInput) (LoginResult, error) {
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		return LoginResult{}, invalidCredentials()
	}
	now := s.clock.Now().UTC()
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}
	if account.Channel != s.channel {
		// 外部验证结果只能登录其所属端别的账号，避免跨端提权。
		return LoginResult{}, invalidCredentials()
	}
	if err := s.guard.Authorize(account, now); err != nil {
		reason := reasonFromAuthError(err)
		lockedNow, recordErr := s.guard.RecordFailure(ctx, account, reason, in.IPHash, in.UAHash, now)
		if recordErr != nil {
			return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
		}
		if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
			audit.OutcomeFailure, reason, in.TraceID, nil); auditErr != nil {
			return LoginResult{}, auditErr
		}
		if lockedNow {
			if lockedErr := s.emitAccountLocked(ctx, account, in.TraceID); lockedErr != nil {
				return LoginResult{}, lockedErr
			}
		}
		return LoginResult{}, err
	}
	return s.completeLogin(ctx, account, now, in.IPHash, in.UAHash, in.Device, in.TraceID)
}

// completeLogin 完成已通过主体验证的登录收口：激活 pending、记录成功、创建会话与审计。
//
// 调用方必须已完成 guard.Authorize；MFA 挑战由密码登录在调用前处理。
func (s *AuthService) completeLogin(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	ipHash, uaHash string,
	device domain.DeviceInfo,
	traceID string,
) (LoginResult, error) {
	if account.Status == domain.AccountStatusPending {
		expected := account.Version
		if activateErr := account.Activate(now); activateErr != nil {
			return LoginResult{}, activateErr
		}
		if saveErr := s.accounts.Save(ctx, account, expected); saveErr != nil {
			return LoginResult{}, apperr.Internal("activate account failed", saveErr)
		}
	}
	if err := s.guard.RecordSuccess(ctx, account, ipHash, uaHash, now); err != nil {
		return LoginResult{}, apperr.Internal("record login success failed", err)
	}
	result, err := s.createSession(ctx, account, now, ipHash, uaHash, device, traceID)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthLogin, account.ID, account.MerchantID, "session", result.SessionID,
		audit.OutcomeSuccess, "", traceID, nil); err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

// refreshOutcome 汇总一次刷新的事务内结果，供提交后写会话撤销快照。
type refreshOutcome struct {
	// pair 成功刷新时的新令牌对。
	pair TokenPair
	// sessionID 成功刷新时的新会话；事务回滚或重放时为空。
	sessionID string
	// revocations 重放等场景下被整链撤销的会话，提交后写 `authn:sid`。
	revocations []sessionStateWrite
	// activatedSession 成功刷新后新会话的撤销快照，提交后写 `authn:sid`。
	activatedSession *SessionState
	// replay 表示本次刷新命中了已轮换令牌（重放），供提交后上报指标。
	replay bool
}

// Refresh 执行一次性刷新：旧会话轮换，同 Family 下创建新会话。
//
// 已 rotated 的令牌再次出现视为重放：撤销整条家庭链并记 auth.token.reused。
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (TokenPair, error) {
	// SEC-03：刷新令牌形态固定，超长输入在哈希与查库前直接拒绝。
	if len(in.RefreshToken) > maxRefreshTokenBytes {
		return TokenPair{}, invalidToken()
	}
	var outcome refreshOutcome
	err := s.runTx(ctx, func(txCtx context.Context) error {
		res, fnErr := s.refreshInTx(txCtx, in)
		outcome = res
		return fnErr
	})
	if err == nil || isRejected(err) {
		// 事务已提交（重放拒绝同样提交）：补齐整链会话撤销快照，使 Access Token ≤60s 失效。
		s.markSessionsRevoked(ctx, outcome.revocations)
		// 新会话快照同样在提交后写入，避免 Redis 写超时占用数据库事务（IDN-GAP-PERF-02）。
		s.markActivatedSession(ctx, outcome.sessionID, outcome.activatedSession)
	}
	if s.metrics != nil {
		if outcome.replay {
			s.metrics.ObserveTokenReuse(ctx)
		}
		result := MetricResultSuccess
		if err != nil {
			result = MetricResultFailure
		}
		s.metrics.ObserveRefresh(ctx, result)
	}
	if err != nil {
		return TokenPair{}, err
	}
	return outcome.pair, nil
}

// refreshInTx 是 Refresh 的事务内实现：重放撤销与新会话签发同一事务。
func (s *AuthService) refreshInTx(ctx context.Context, in RefreshInput) (refreshOutcome, error) {
	if !RefreshPrefixMatches(s.channel, in.RefreshToken) {
		return refreshOutcome{}, invalidToken()
	}
	hash, err := HashRefreshToken(in.RefreshToken)
	if err != nil {
		return refreshOutcome{}, invalidToken()
	}
	now := s.clock.Now().UTC()
	session, err := s.sessions.FindByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return refreshOutcome{}, invalidToken()
		}
		return refreshOutcome{}, apperr.Internal("load session failed", err)
	}
	if session.Status == domain.SessionStatusRotated {
		// REL-06：短窗口内的并发重试不撤销整链，避免网络重试 / 双击刷新误伤用户。
		if s.refreshGrace > 0 && !session.UpdatedAt.IsZero() && now.Sub(session.UpdatedAt) <= s.refreshGrace {
			return refreshOutcome{}, invalidToken()
		}
		outcome, revokeErr := s.revokeSessionFamily(ctx, session, now)
		if revokeErr != nil {
			return refreshOutcome{}, revokeErr
		}
		if auditErr := s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
			audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil); auditErr != nil {
			return refreshOutcome{}, auditErr
		}
		outcome.replay = true
		return outcome, invalidToken()
	}
	// 设备达到绝对生命上限：终止整条链，刷新令牌不得再续期（IDN-P1-07）。
	family, err := s.sessions.FindFamily(ctx, session.FamilyID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return refreshOutcome{}, invalidToken()
		}
		return refreshOutcome{}, apperr.Internal("load session family failed", err)
	}
	if !family.AbsoluteExpiresAt.IsZero() && !now.Before(family.AbsoluteExpiresAt) {
		writes, revokeErr := s.revokeFamilyChain(ctx, family.ID, domain.RevokeReasonExpired, now)
		if revokeErr != nil {
			s.logError("identity revoke expired family failed",
				"family_ref", shortHash(family.ID), "error", revokeErr)
			s.observeSessionRevoke(ctx, domain.RevokeReasonExpired, MetricResultFailure)
			return refreshOutcome{}, apperr.Unavailable("revoke expired session failed")
		}
		if auditErr := s.emitAudit(ctx, audit.ActionAuthTokenRevoked, family.AccountID, "", "session", family.ID,
			audit.OutcomeSuccess, string(domain.RevokeReasonExpired), in.TraceID, nil); auditErr != nil {
			return refreshOutcome{}, auditErr
		}
		return refreshOutcome{revocations: writes}, invalidToken()
	}
	if !session.IsActive(now) {
		return refreshOutcome{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, session.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			outcome, revokeErr := s.revokeSessionFamily(ctx, session, now)
			if revokeErr != nil {
				return refreshOutcome{}, revokeErr
			}
			return outcome, invalidToken()
		}
		return refreshOutcome{}, apperr.Internal("load account failed", err)
	}
	if account.IsDeleted() || account.Status == domain.AccountStatusDisabled ||
		account.TokenVersion != session.TokenVersion {
		outcome, revokeErr := s.revokeSessionFamily(ctx, session, now)
		if revokeErr != nil {
			return refreshOutcome{}, revokeErr
		}
		return outcome, invalidToken()
	}

	expected := session.Version
	if rotateErr := session.Rotate(now); rotateErr != nil {
		outcome, revokeErr := s.revokeSessionFamily(ctx, session, now)
		if revokeErr != nil {
			return refreshOutcome{}, revokeErr
		}
		return outcome, invalidToken()
	}
	if saveErr := s.sessions.Save(ctx, session, expected); saveErr != nil {
		if errors.Is(saveErr, apperr.ErrConflict) {
			// REL-06：并发刷新冲突发生在同一次轮换窗口内，按短宽限处理，不撤销整链。
			if s.refreshGrace > 0 {
				return refreshOutcome{}, invalidToken()
			}
			// 并发刷新只有一个请求能完成 active → rotated，其余按重放处理。
			outcome, revokeErr := s.revokeSessionFamily(ctx, session, now)
			if revokeErr != nil {
				return refreshOutcome{}, revokeErr
			}
			if auditErr := s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
				audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil); auditErr != nil {
				return refreshOutcome{}, auditErr
			}
			outcome.replay = true
			return outcome, invalidToken()
		}
		return refreshOutcome{}, apperr.Internal("rotate session failed", saveErr)
	}

	pair, sessionID, activated, err := s.issueNewSession(ctx, account, session.FamilyID, now, in.Device)
	if err != nil {
		return refreshOutcome{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthTokenRefresh, account.ID, account.MerchantID, "session", sessionID,
		audit.OutcomeSuccess, "", in.TraceID, map[string]string{"family_id": shortHash(session.FamilyID)}); err != nil {
		return refreshOutcome{}, err
	}
	return refreshOutcome{pair: pair, sessionID: sessionID, activatedSession: activated}, nil
}

// Logout 撤销当前会话并把当前 jti 加入黑名单；重复登出幂等。
//
// 会话撤销与审计在同一事务内；jti 黑名单不可回滚，放在提交后执行。
func (s *AuthService) Logout(ctx context.Context, actor SessionActor) error {
	if actor.AccountID == "" || actor.SessionID == "" {
		return invalidToken()
	}
	now := s.clock.Now().UTC()
	revokeToken := false
	var writes []sessionStateWrite
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		session, err := s.sessions.FindByID(txCtx, actor.SessionID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				// 会话已不存在：仍需拉黑当前访问令牌，保持幂等。
				revokeToken = true
				return nil
			}
			return apperr.Internal("load session failed", err)
		}
		if session.AccountID != actor.AccountID {
			return invalidToken()
		}
		chain, revokeErr := s.revokeFamilyChain(txCtx, session.FamilyID, domain.RevokeReasonLogout, now)
		if revokeErr != nil {
			return apperr.Internal("revoke session family failed", revokeErr)
		}
		writes = chain
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthLogout, actor.AccountID, actor.MerchantID, "session", session.FamilyID,
			audit.OutcomeSuccess, "", actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		revokeToken = true
		return nil
	}); err != nil {
		return err
	}
	// 业务已提交：先写会话撤销快照（整台设备的 Access Token 都会被拒绝），再拉黑当前 jti。
	s.markSessionsRevoked(ctx, writes)
	if !revokeToken {
		return nil
	}
	return s.revokeCurrentToken(ctx, actor, now)
}

// LogoutAll 递增 token_version 并撤销账号全部会话。
//
// 账号版本、会话撤销与审计在同一事务内；撤销快照与 jti 黑名单在提交后写入。
func (s *AuthService) LogoutAll(ctx context.Context, actor SessionActor) error {
	if actor.AccountID == "" {
		return invalidToken()
	}
	now := s.clock.Now().UTC()
	var state *AccountState
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		account, err := s.accounts.FindByID(txCtx, actor.AccountID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return invalidToken()
			}
			return apperr.Internal("load account failed", err)
		}
		if account.IsDeleted() || account.Status == domain.AccountStatusDisabled {
			return invalidToken()
		}
		expected := account.Version
		if bumpErr := account.BumpTokenVersion(now); bumpErr != nil {
			return apperr.Internal("bump token version failed", bumpErr)
		}
		if saveErr := s.accounts.Save(txCtx, account, expected); saveErr != nil {
			return apperr.Internal("save account failed", saveErr)
		}
		if _, revokeErr := s.sessions.RevokeByAccount(txCtx, account.ID, domain.RevokeReasonLogoutAll, now); revokeErr != nil {
			return apperr.Internal("revoke account sessions failed", revokeErr)
		}
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthTokenRevoked, actor.AccountID, actor.MerchantID, "account", actor.AccountID,
			audit.OutcomeSuccess, string(domain.RevokeReasonLogoutAll), actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		state = &AccountState{Version: account.TokenVersion, Status: account.Status}
		return nil
	}); err != nil {
		return err
	}
	if state != nil {
		// 业务已提交：快照失效失败只告警，不把成功结果改成失败（下次校验会回源 DB）。
		s.invalidateAccountState(ctx, actor.AccountID, *state)
	}
	return s.revokeCurrentToken(ctx, actor, now)
}

// 会话列表分页边界。
const (
	// DefaultSessionPageSize 默认每页设备数。
	DefaultSessionPageSize = 20
	// MaxSessionPageSize 单页设备数硬上限，避免响应体无界增长。
	MaxSessionPageSize = 100
)

// SessionListInput 是设备会话列表入参。
type SessionListInput struct {
	SessionActor
	// Cursor 上一页返回的 next_cursor；空表示第一页。
	Cursor string
	// Limit 每页设备数；≤0 使用 DefaultSessionPageSize，超过 MaxSessionPageSize 会被收敛。
	Limit int
}

// SessionPage 是设备会话分页结果。
type SessionPage struct {
	// Items 当前页设备会话。
	Items []SessionView
	// NextCursor 下一页游标；空表示没有更多。
	NextCursor string
}

// ListSessions 返回本账号设备会话（family），供 A 端会话管理使用。
func (s *AuthService) ListSessions(ctx context.Context, in SessionListInput) (SessionPage, error) {
	if strings.TrimSpace(in.AccountID) == "" {
		return SessionPage{}, invalidToken()
	}
	currentFamily := ""
	if in.SessionID != "" {
		if session, err := s.sessions.FindByID(ctx, in.SessionID); err == nil {
			currentFamily = session.FamilyID
		}
	}
	return s.listFamilies(ctx, in.AccountID, currentFamily, in.Cursor, in.Limit)
}

// listFamilies 按游标分页返回设备会话；currentFamilyID 用于标记当前设备。
func (s *AuthService) listFamilies(
	ctx context.Context,
	accountID, currentFamilyID, cursor string,
	limit int,
) (SessionPage, error) {
	afterLastUsed, afterID, err := parseSessionCursor(cursor)
	if err != nil {
		return SessionPage{}, err
	}
	limit = normalizeSessionPageSize(limit)
	families, err := s.sessions.ListFamiliesByAccount(ctx, accountID, afterLastUsed, afterID, limit)
	if err != nil {
		return SessionPage{}, apperr.Internal("list session families failed", err)
	}
	items := make([]SessionView, 0, len(families))
	for _, family := range families {
		device := family.Device.Sanitized()
		items = append(items, SessionView{
			ID:                family.ID,
			Current:           family.ID == currentFamilyID,
			Status:            family.Status.String(),
			CreatedAt:         family.CreatedAt,
			LastUsedAt:        family.LastUsedAt,
			ExpiresAt:         family.ExpiresAt,
			AbsoluteExpiresAt: family.AbsoluteExpiresAt,
			RevokedAt:         family.RevokedAt,
			DeviceLabel:       device.Label(),
			DeviceType:        device.Type,
			OS:                device.OS,
			Browser:           device.Browser,
			IPMasked:          device.IPMasked,
		})
	}
	page := SessionPage{Items: items}
	if len(families) == limit && len(families) > 0 {
		last := families[len(families)-1]
		page.NextCursor = encodeSessionCursor(last.LastUsedAt, last.ID)
	}
	return page, nil
}

// Me 返回当前登录主体资料。
//
// 只读：不写审计、不碰会话；不存在、软删、端别不一致与停用统一返回 40100，
// 避免通过该端点枚举账号状态。
func (s *AuthService) Me(ctx context.Context, actor SessionActor) (MeView, error) {
	account, err := s.loadActiveAccount(ctx, actor.AccountID)
	if err != nil {
		return MeView{}, err
	}
	return s.buildMeView(ctx, account)
}

// UpdateProfileInput 是更新当前登录主体资料的入参。
type UpdateProfileInput struct {
	SessionActor
	// DisplayName 展示昵称；空串表示清除，由前端回退登录主体。
	DisplayName string
}

// UpdateProfile 更新当前登录主体的展示昵称。
//
// 只改展示字段：不递增 TokenVersion、不撤销会话，改昵称不会把用户踢下线。
// 成功后直接返回完整的 MeView，省掉前端一次 GET /me 往返。
// 审计只记录被改动的字段名，不记录昵称原文——昵称可能包含真实姓名等个人信息。
func (s *AuthService) UpdateProfile(ctx context.Context, in UpdateProfileInput) (MeView, error) {
	if strings.TrimSpace(in.AccountID) == "" {
		return MeView{}, invalidToken()
	}
	now := s.clock.Now().UTC()
	var updated *domain.Account
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		account, err := s.loadActiveAccount(txCtx, in.AccountID)
		if err != nil {
			return err
		}
		expected := account.Version
		if renameErr := account.RenameDisplayName(in.DisplayName, now); renameErr != nil {
			return renameErr
		}
		if saveErr := s.accounts.Save(txCtx, account, expected); saveErr != nil {
			if errors.Is(saveErr, apperr.ErrConflict) {
				return apperr.Conflict("account was modified concurrently, please retry")
			}
			return apperr.Internal("save account failed", saveErr)
		}
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthProfileUpdated, account.ID, account.MerchantID,
			"account", account.ID, audit.OutcomeSuccess, "", in.TraceID,
			map[string]string{"changed_fields": "display_name"}); auditErr != nil {
			return auditErr
		}
		updated = account
		return nil
	}); err != nil {
		return MeView{}, err
	}
	return s.buildMeView(ctx, updated)
}

// loadActiveAccount 加载当前请求可用的账号。
//
// 不存在 / 软删 / 端别不一致 / 停用统一返回 40100：资料端点不能成为账号状态探测器。
// Me 与 UpdateProfile 共用同一组不变量，避免两处判断漂移。
func (s *AuthService) loadActiveAccount(ctx context.Context, accountID string) (*domain.Account, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, invalidToken()
		}
		return nil, apperr.Internal("load account failed", err)
	}
	if account == nil || account.DeletedAt != nil || account.Channel != s.channel ||
		account.Status == domain.AccountStatusDisabled {
		return nil, invalidToken()
	}
	return account, nil
}

// buildMeView 组装资料视图。
func (s *AuthService) buildMeView(ctx context.Context, account *domain.Account) (MeView, error) {
	mfaEnabled := false
	if s.mfa != nil {
		enrollment, mfaErr := s.mfa.FindByAccount(ctx, account.ID)
		switch {
		case mfaErr == nil && enrollment != nil:
			mfaEnabled = enrollment.IsConfirmed()
		case mfaErr == nil || errors.Is(mfaErr, apperr.ErrNotFound):
			mfaEnabled = false
		default:
			return MeView{}, apperr.Internal("load mfa enrollment failed", mfaErr)
		}
	}
	// 凭据查询失败必须失败关闭：把「查询失败」当成「未设置」会让安全设置页
	// 展示错误状态，比直接报错更难发现。
	hasPassword, phoneBound := false, false
	if s.credentials != nil {
		credentials, credErr := s.credentials.ListByAccount(ctx, account.ID)
		if credErr != nil {
			return MeView{}, apperr.Internal("list credentials failed", credErr)
		}
		for _, credential := range credentials {
			switch credential.Type {
			case domain.CredentialTypePassword:
				hasPassword = true
			case domain.CredentialTypePhone:
				phoneBound = true
			}
		}
	}
	var passwordUpdatedAt *time.Time
	if account.PasswordUpdatedAt != nil {
		at := *account.PasswordUpdatedAt
		passwordUpdatedAt = &at
	}
	return MeView{
		SubjectID:          account.ID,
		Subject:            account.Subject,
		DisplayName:        account.DisplayName,
		Channel:            account.Channel,
		MerchantID:         account.MerchantID,
		Status:             account.Status,
		MFAEnabled:         mfaEnabled,
		HasPassword:        hasPassword,
		PasswordUpdatedAt:  passwordUpdatedAt,
		MustChangePassword: account.MustChangePassword,
		PhoneBound:         phoneBound,
		TokenVersion:       account.TokenVersion,
	}, nil
}

// RevokeSession 撤销账号下的指定设备（family）；已被撤销时幂等返回。
//
// sessionID 参数为会话列表中返回的设备 ID（family_id）。撤销与审计在同一事务内，
// 设备全部令牌代的撤销快照与当前 jti 黑名单在提交后写入。
func (s *AuthService) RevokeSession(ctx context.Context, actor SessionActor, familyID string) error {
	if actor.AccountID == "" || strings.TrimSpace(familyID) == "" {
		return apperr.BadRequest("session id is required")
	}
	now := s.clock.Now().UTC()
	revokeToken := false
	var writes []sessionStateWrite
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		family, err := s.sessions.FindFamily(txCtx, familyID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.NotFound("session not found")
			}
			return apperr.Internal("load session family failed", err)
		}
		if family.AccountID != actor.AccountID {
			// 不区分「不存在」与「不属于本账号」，避免横向枚举他人设备。
			return apperr.NotFound("session not found")
		}
		chain, revokeErr := s.revokeFamilyChain(txCtx, familyID, domain.RevokeReasonLogout, now)
		if revokeErr != nil {
			return apperr.Internal("revoke session family failed", revokeErr)
		}
		writes = chain
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthTokenRevoked, actor.AccountID, actor.MerchantID, "session", familyID,
			audit.OutcomeSuccess, string(domain.RevokeReasonLogout), actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		if actor.SessionID != "" {
			current, currentErr := s.sessions.FindByID(txCtx, actor.SessionID)
			if currentErr == nil && current.FamilyID == familyID {
				revokeToken = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// 业务已提交：写会话撤销快照，使被撤销设备的 Access Token 在 ≤60s 内失效。
	s.markSessionsRevoked(ctx, writes)
	if !revokeToken {
		return nil
	}
	return s.revokeCurrentToken(ctx, actor, now)
}

// createSession 创建新设备会话与首个令牌代，并签发令牌。
//
// family_id 取首个会话 ID：同一登录链后续刷新只轮换 identity_session。
func (s *AuthService) createSession(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	ipHash, uaHash string,
	device domain.DeviceInfo,
	traceID string,
) (LoginResult, error) {
	if err := s.evictFamilies(ctx, account, now, traceID); err != nil {
		return LoginResult{}, err
	}
	sessionID, err := s.ids.NextString()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate session id failed", err)
	}
	expiresAt := now.Add(s.tokens.RefreshTTL())
	absoluteExpiresAt := s.absoluteExpiry(now, expiresAt)
	family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
		ID:                sessionID,
		AccountID:         account.ID,
		IPHash:            ipHash,
		UAHash:            uaHash,
		Device:            device,
		ExpiresAt:         expiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
		Now:               now,
	})
	if err != nil {
		return LoginResult{}, apperr.Internal("build session family failed", err)
	}
	if err := s.sessions.SaveFamily(ctx, family, 0); err != nil {
		return LoginResult{}, apperr.Internal("save session family failed", err)
	}
	pair, err := s.tokens.Issue(account, sessionID)
	if err != nil {
		return LoginResult{}, err
	}
	// 登录响应返回设备绝对到期时间，供前端在临近上限时提示重新登录。
	pair.AbsoluteExpiresAt = absoluteExpiresAt
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:            sessionID,
		AccountID:     account.ID,
		FamilyID:      sessionID,
		RefreshHash:   pair.RefreshHash,
		TokenVersion:  account.TokenVersion,
		ExpiresAt:     expiresAt,
		IdleExpiresAt: s.idleExpiry(now, expiresAt),
		Now:           now,
	})
	if err != nil {
		return LoginResult{}, apperr.Internal("build session failed", err)
	}
	if err := s.sessions.Save(ctx, session, 0); err != nil {
		return LoginResult{}, apperr.Internal("save session failed", err)
	}
	return LoginResult{
		TokenPair: pair,
		SessionID: sessionID,
		AccountID: account.ID,
		activatedSession: &SessionState{
			Status:    session.Status,
			ExpiresAt: session.ExpiresAt,
		},
	}, nil
}

// evictFamilies 在创建新设备前淘汰最久未使用的设备，保证单账号活跃设备数受限。
//
// 采用「先淘汰再创建」而不是创建后回收：新设备永远不被自己的登录顶掉。
// 并发登录可能多淘汰一台（最终仍不超过上限），但不会让上限失效。
func (s *AuthService) evictFamilies(ctx context.Context, account *domain.Account, now time.Time, traceID string) error {
	if s.maxPerAccount <= 0 {
		return nil
	}
	families, err := s.sessions.ListFamiliesByAccount(ctx, account.ID, time.Time{}, "", 0)
	if err != nil {
		return apperr.Internal("list session families failed", err)
	}
	excess := len(families) - s.maxPerAccount + 1
	if excess <= 0 {
		return nil
	}
	// ListFamiliesByAccount 按最近使用倒序，从尾部开始淘汰。
	for i := len(families) - 1; i >= 0 && excess > 0; i-- {
		target := families[i]
		if !target.IsActive(now) {
			continue
		}
		if _, revokeErr := s.sessions.RevokeFamily(ctx, target.ID, domain.RevokeReasonEvicted, now); revokeErr != nil {
			return apperr.Internal("evict session family failed", revokeErr)
		}
		if auditErr := s.emitAudit(ctx, audit.ActionAuthSessionEvicted, target.AccountID, account.MerchantID, "session", target.ID,
			audit.OutcomeSuccess, string(domain.RevokeReasonEvicted), traceID, nil); auditErr != nil {
			return auditErr
		}
		excess--
	}
	return nil
}

// issueNewSession 在刷新时创建同 Family 的下一代会话，并推进设备最近使用时间。
func (s *AuthService) issueNewSession(
	ctx context.Context,
	account *domain.Account,
	familyID string,
	now time.Time,
	device domain.DeviceInfo,
) (TokenPair, string, *SessionState, error) {
	family, err := s.sessions.FindFamily(ctx, familyID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return TokenPair{}, "", nil, invalidToken()
		}
		return TokenPair{}, "", nil, apperr.Internal("load session family failed", err)
	}
	if !family.IsActive(now) {
		// 设备已过期 / 撤销 / 达到绝对上限：终止本条链，避免刷新绕过设备状态。
		reason := domain.RevokeReasonReplay
		if !family.AbsoluteExpiresAt.IsZero() && !now.Before(family.AbsoluteExpiresAt) {
			reason = domain.RevokeReasonExpired
		}
		if _, revokeErr := s.sessions.RevokeFamily(ctx, familyID, reason, now); revokeErr != nil {
			// 兜底撤销失败不覆盖原始 401，但必须留痕，避免「链路未终止」静默。
			s.logError("identity revoke inactive family failed",
				"family_ref", shortHash(familyID), "reason", reason.String(), "error", revokeErr)
			s.observeSessionRevoke(ctx, reason, MetricResultFailure)
		}
		return TokenPair{}, "", nil, invalidToken()
	}
	// 历史会话可能缺少设备画像：用当次请求逐字段补全，随本次 Touch 一起落库。
	family.Device.MergeMissing(device)
	// 当前令牌代过期时间只能在设备绝对上限内滑动。
	nextExpiry := now.Add(s.tokens.RefreshTTL())
	if !family.AbsoluteExpiresAt.IsZero() && nextExpiry.After(family.AbsoluteExpiresAt) {
		nextExpiry = family.AbsoluteExpiresAt
	}
	expectedFamily := family.Version
	if touchErr := family.Touch(now, nextExpiry); touchErr != nil {
		return TokenPair{}, "", nil, invalidToken()
	}
	if saveErr := s.sessions.SaveFamily(ctx, family, expectedFamily); saveErr != nil {
		if errors.Is(saveErr, apperr.ErrConflict) {
			// 并发刷新只允许一条链推进，其余按重放处理。
			return TokenPair{}, "", nil, invalidToken()
		}
		return TokenPair{}, "", nil, apperr.Internal("save session family failed", saveErr)
	}

	sessionID, err := s.ids.NextString()
	if err != nil {
		return TokenPair{}, "", nil, apperr.Internal("generate session id failed", err)
	}
	pair, err := s.tokens.Issue(account, sessionID)
	if err != nil {
		return TokenPair{}, "", nil, err
	}
	// 刷新响应同样返回设备绝对到期时间。
	pair.AbsoluteExpiresAt = family.AbsoluteExpiresAt
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:            sessionID,
		AccountID:     account.ID,
		FamilyID:      familyID,
		RefreshHash:   pair.RefreshHash,
		TokenVersion:  account.TokenVersion,
		ExpiresAt:     family.ExpiresAt,
		IdleExpiresAt: s.idleExpiry(now, family.ExpiresAt),
		Now:           now,
	})
	if err != nil {
		return TokenPair{}, "", nil, apperr.Internal("build session failed", err)
	}
	if err := s.sessions.Save(ctx, session, 0); err != nil {
		return TokenPair{}, "", nil, apperr.Internal("save session failed", err)
	}
	return pair, sessionID, &SessionState{
		Status:    session.Status,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

// idleExpiry 计算空闲上限；不得超过当前令牌代或设备绝对到期时间。
func (s *AuthService) idleExpiry(now, hardExpiry time.Time) time.Time {
	if s.idleTimeout <= 0 {
		return time.Time{}
	}
	idle := now.Add(s.idleTimeout)
	if !hardExpiry.IsZero() && idle.After(hardExpiry) {
		return hardExpiry
	}
	return idle
}

// absoluteExpiry 计算设备绝对到期时间；不早于首个令牌代过期时间。
func (s *AuthService) absoluteExpiry(now, expiresAt time.Time) time.Time {
	if s.absoluteTTL <= 0 {
		return expiresAt
	}
	absolute := now.Add(s.absoluteTTL)
	if !absolute.After(expiresAt) {
		return expiresAt
	}
	return absolute
}

// sessionStateWrite 是一次待写入的会话撤销快照（提交后执行）。
type sessionStateWrite struct {
	// SessionID 会话主键，对应 Access Token 的 sid。
	SessionID string
	// RevokedAt 撤销时间。
	RevokedAt time.Time
	// ExpiresAt 会话绝对过期时间，用于判断是否已自然过期。
	ExpiresAt time.Time
}

// markActivatedSession 在事务提交后写入 active 快照，预热鉴权缓存（失败只告警）。
func (s *AuthService) markActivatedSession(ctx context.Context, sessionID string, state *SessionState) {
	if state == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.markSessionState(ctx, sessionID, *state)
}

// markSessionsRevoked 逐条写入会话撤销快照；同一会话只写一次。
func (s *AuthService) markSessionsRevoked(ctx context.Context, writes []sessionStateWrite) {
	if len(writes) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(writes))
	for _, write := range writes {
		if _, dup := seen[write.SessionID]; dup {
			continue
		}
		seen[write.SessionID] = struct{}{}
		s.markSessionState(ctx, write.SessionID, SessionState{
			Status:    domain.SessionStatusRevoked,
			RevokedAt: write.RevokedAt,
			ExpiresAt: write.ExpiresAt,
		})
	}
}

// markSessionState 写入会话撤销快照；失败只告警，由 ≤state_ttl 的回源兜底。
//
// 必须在业务事务提交后调用：回滚的事务不得留下撤销快照。
func (s *AuthService) markSessionState(ctx context.Context, sessionID string, state SessionState) {
	if s.revocation == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	state.FetchedAt = s.clock.Now().UTC()
	if err := s.revocation.SaveSessionState(ctx, sessionID, state, s.sessionStateTTL); err != nil {
		if s.observer != nil {
			s.observer.OnAccountStateWriteFailure(ctx, "session_set", err)
		}
		s.logError("identity session revocation state invalidate failed",
			"session_ref", shortHash(sessionID), "error", err)
	}
}

// revokeFamily 撤销整条会话链并返回需写快照的会话。
//
// 撤销失败必须向上返回错误，由调用方回滚事务（IDN-P1-03）：禁止只提交重放审计
// 而没有真正撤销，否则攻击者仍持有可用刷新令牌。
func (s *AuthService) revokeFamily(ctx context.Context, session *domain.Session, now time.Time) ([]sessionStateWrite, error) {
	if session == nil || session.FamilyID == "" {
		return nil, nil
	}
	writes, err := s.revokeFamilyChain(ctx, session.FamilyID, domain.RevokeReasonReplay, now)
	if err != nil {
		s.logError("identity revoke family failed",
			"family_ref", shortHash(session.FamilyID), "error", err)
		return nil, apperr.Unavailable("revoke session family failed")
	}
	return writes, nil
}

// revokeSessionFamily 撤销整链并封装为刷新事务的结果；失败返回 50300 触发回滚。
func (s *AuthService) revokeSessionFamily(
	ctx context.Context,
	session *domain.Session,
	now time.Time,
) (refreshOutcome, error) {
	writes, err := s.revokeFamily(ctx, session, now)
	if err != nil {
		return refreshOutcome{}, err
	}
	return refreshOutcome{revocations: writes}, nil
}

// observeSessionRevoke 上报设备撤销结果；metrics 未注入时为 no-op。
func (s *AuthService) observeSessionRevoke(ctx context.Context, reason domain.RevokeReason, result string) {
	if s.metrics == nil {
		return
	}
	s.metrics.ObserveSessionRevoke(ctx, reason.String(), result)
}

// revokeFamilyChain 撤销设备下全部 active / rotated 会话并返回撤销快照写入项。
func (s *AuthService) revokeFamilyChain(
	ctx context.Context,
	familyID string,
	reason domain.RevokeReason,
	now time.Time,
) ([]sessionStateWrite, error) {
	// 只取 access_ttl 内仍可能持有有效 Access Token 的会话，避免随会话链增长无界加载。
	// 额外留 1 分钟时钟偏差余量；实现层仍有硬上限（IDN-GAP-PERF-03）。
	activeSince := now.Add(-s.tokens.AccessTTL() - time.Minute)
	sessions, listErr := s.sessions.ListActiveByFamily(ctx, familyID, activeSince)
	if _, err := s.sessions.RevokeFamily(ctx, familyID, reason, now); err != nil {
		s.logError("identity revoke session family failed",
			"family_ref", shortHash(familyID), "reason", reason.String(), "error", err)
		s.observeSessionRevoke(ctx, reason, MetricResultFailure)
		return nil, err
	}
	if listErr != nil {
		// 撤销已成功；缺少快照只会让旧 Access Token 最长多存活一个 state_ttl（≤60s）。
		// 不能因读取失败回滚已完成的安全撤销。
		s.logError("identity list active sessions failed",
			"family_ref", shortHash(familyID), "reason", reason.String(), "error", listErr)
	}
	s.observeSessionRevoke(ctx, reason, MetricResultSuccess)
	writes := make([]sessionStateWrite, 0, len(sessions))
	for _, item := range sessions {
		writes = append(writes, sessionStateWrite{
			SessionID: item.ID,
			RevokedAt: now,
			ExpiresAt: item.ExpiresAt,
		})
	}
	return writes, nil
}

// revokeCurrentToken 把当前访问令牌的 jti 加入黑名单。
func (s *AuthService) revokeCurrentToken(ctx context.Context, actor SessionActor, now time.Time) error {
	if s.revocation == nil || actor.TokenID == "" || actor.TokenExpiresAt.IsZero() {
		return nil
	}
	ttl := actor.TokenExpiresAt.Sub(now)
	if ttl <= 0 {
		return nil
	}
	if err := s.revocation.RevokeToken(ctx, actor.TokenID, ttl); err != nil {
		return apperr.Internal("revoke access token failed", err)
	}
	return nil
}

// invalidateAccountState 刷新账号撤销快照；失败只写告警，不改变已提交的业务结果。
func (s *AuthService) invalidateAccountState(ctx context.Context, accountID string, state AccountState) {
	if s.invalidation == nil {
		return
	}
	if err := s.invalidation.Invalidate(ctx, accountID, state); err != nil {
		s.logError("identity revocation state invalidate failed",
			"account_ref", shortHash(accountID), "error", err)
	}
}

// logError 输出告警日志；未注入 logger 时静默（指标仍会记录失败）。
func (s *AuthService) logError(msg string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Error(msg, args...)
}

// normalizeSessionPageSize 收敛分页大小到 [1, MaxSessionPageSize]。
func normalizeSessionPageSize(limit int) int {
	if limit <= 0 {
		return DefaultSessionPageSize
	}
	if limit > MaxSessionPageSize {
		return MaxSessionPageSize
	}
	return limit
}

// encodeSessionCursor 把设备最近使用时间与主键编码为不透明游标。
func encodeSessionCursor(lastUsed time.Time, familyID string) string {
	return strconv.FormatInt(lastUsed.UTC().UnixMilli(), 10) + "|" + familyID
}

// parseSessionCursor 解析游标；空游标表示第一页。
func parseSessionCursor(raw string) (time.Time, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, "", nil
	}
	parts := strings.SplitN(trimmed, "|", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return time.Time{}, "", apperr.BadRequest("invalid session cursor")
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, "", apperr.BadRequest("invalid session cursor")
	}
	return time.UnixMilli(ms).UTC(), parts[1], nil
}

// emitAudit 写入审计事件；未配置 emitter 时跳过。
func (s *AuthService) emitAudit(
	ctx context.Context,
	action, actorID, merchantID, resourceType, resourceID string,
	outcome audit.Outcome,
	reason, traceID string,
	attrs map[string]string,
) error {
	return s.emitAuditAs(ctx, s.actorType, action, actorID, merchantID, resourceType, resourceID,
		outcome, reason, traceID, attrs)
}

// emitAuditAs 以指定 actor_type 写审计事件；system 用于自动建号等非用户触发操作。
func (s *AuthService) emitAuditAs(
	ctx context.Context,
	actorType, action, actorID, merchantID, resourceType, resourceID string,
	outcome audit.Outcome,
	reason, traceID string,
	attrs map[string]string,
) error {
	if s.audit == nil {
		return nil
	}
	eventID, err := s.ids.NextString()
	if err != nil {
		return apperr.Internal("generate audit event id failed", err)
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actorID,
		ActorType:    actorType,
		Channel:      string(s.channel),
		MerchantID:   merchantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   s.clock.Now().UTC(),
		TraceID:      normalizeTraceID(traceID),
		Service:      s.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	if err := s.audit.Emit(ctx, event); err != nil {
		return apperr.Internal("emit audit event failed", err)
	}
	return nil
}

// emitAccountLocked 仅在本次失败真正触发锁定迁移时补发 auth.account.locked。
//
// 已锁定账号的后续失败由 RecordFailure 返回 lockedNow=false，避免重复审计。
func (s *AuthService) emitAccountLocked(ctx context.Context, account *domain.Account, traceID string) error {
	if account == nil {
		return nil
	}
	if s.metrics != nil {
		s.metrics.ObserveLockout(ctx, s.channel)
	}
	return s.emitAudit(ctx, audit.ActionAuthAccountLocked, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeFailure, domain.LoginReasonLocked, traceID, nil)
}

// HashClientRef 返回客户端 IP / UA 的不可逆摘要；空值返回空串。
func HashClientRef(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

// reasonFromAuthError 从守卫错误链中提取审计原因。
func reasonFromAuthError(err error) string {
	switch {
	case errors.Is(err, domain.ErrAccountLocked):
		return domain.LoginReasonLocked
	case errors.Is(err, domain.ErrAccountDisabled):
		return domain.LoginReasonDisabled
	default:
		return domain.LoginReasonBadCredential
	}
}

// loginRateKey 构造登录限流键：channel + 账号摘要 + IP 摘要。
func loginRateKey(channel domain.Channel, accountRef, ipHash string) string {
	if ipHash == "" {
		return string(channel) + ":login:" + accountRef
	}
	return string(channel) + ":login:" + accountRef + ":" + ipHash
}

// shortHash 截断摘要，避免审计 attrs 过长。
func shortHash(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:16]
}

// normalizeTraceID 为空 TraceID 提供稳定占位，保证审计事件必填字段成立。
func normalizeTraceID(traceID string) string {
	trimmed := strings.TrimSpace(traceID)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
