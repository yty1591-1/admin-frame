package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
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
	MFACipher *crypto.AEAD
	// MFATickets 是两步登录票据的一次性消费存储。
	MFATickets MFATicketStore
	// MFAIssuer 写入 otpauth URI；空值使用 DefaultMFAIssuer。
	MFAIssuer string
	// MFAAdminRequired 表示 A 端强制绑定 MFA。
	MFAAdminRequired bool
	// IdleTimeout 会话空闲上限；≤0 时使用 72h。
	IdleTimeout time.Duration
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
	// Password 明文密码；只在本层短暂存在，禁止写日志。
	Password string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
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
}

// RefreshInput 是刷新令牌入参。
type RefreshInput struct {
	RefreshToken string
	TraceID      string
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
	ID            string
	Current       bool
	Status        string
	CreatedAt     time.Time
	LastUsedAt    time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	RevokedAt     *time.Time
}

// MeView 是当前登录主体资料的对外视图。
//
// 只承载身份域字段；角色、权限与数据范围仍由 IAM 的 /iam/me/permissions 提供。
type MeView struct {
	// SubjectID 账号主键，与令牌 sub 一致。
	SubjectID string
	// Subject 规范化登录主体（trim + 小写）。
	Subject string
	// Channel 账号所属端别；必须与装配端别一致。
	Channel domain.Channel
	// MerchantID B 端商户 ID；A / C 端为空串。
	MerchantID string
	// Status 账号生命周期状态。
	Status domain.AccountStatus
	// MFAEnabled 表示 MFA 绑定是否已确认。
	MFAEnabled bool
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
	logger       *slog.Logger
	audit        audit.Emitter
	ids          IDGenerator
	channel      domain.Channel
	uow          UnitOfWork
	idleTimeout  time.Duration
	service      string
	actorType    string
	clock        Clock
	mfa          domain.MFAEnrollmentRepository
	mfaCipher    *crypto.AEAD
	mfaTickets   MFATicketStore
	mfaIssuer    string
	mfaRequired  bool
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
		accounts:     cfg.Accounts,
		credentials:  cfg.Credentials,
		sessions:     cfg.Sessions,
		guard:        cfg.Guard,
		password:     cfg.Password,
		tokens:       cfg.Tokens,
		revocation:   cfg.Revocation,
		invalidation: invalidation,
		logger:       cfg.Logger,
		audit:        cfg.Audit,
		ids:          cfg.IDs,
		channel:      cfg.Channel,
		uow:          uow,
		idleTimeout:  idle,
		service:      cfg.Service,
		actorType:    actorType,
		clock:        clock,
		mfa:          cfg.MFA,
		mfaCipher:    cfg.MFACipher,
		mfaTickets:   cfg.MFATickets,
		mfaIssuer:    mfaIssuer,
		mfaRequired:  cfg.MFAAdminRequired,
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
// 账号不存在、密码错误、停用与锁定统一返回 40100 文案，真实原因只写审计。
func (s *AuthService) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	var result LoginResult
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.loginInTx(txCtx, in)
		if err != nil {
			return err
		}
		result = res
		return nil
	}); err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

// loginInTx 是 Login 的事务内实现：失败尝试、审计与成功写入同一事务。
func (s *AuthService) loginInTx(ctx context.Context, in LoginInput) (LoginResult, error) {
	now := s.clock.Now().UTC()
	subject := domain.NormalizeSubject(in.Subject)
	if subject == "" || strings.TrimSpace(in.Password) == "" {
		return LoginResult{}, invalidCredentials()
	}
	accountRef := HashAccountRef(s.channel, subject)
	if err := s.guard.CheckRateLimit(loginRateKey(s.channel, accountRef, in.IPHash)); err != nil {
		return LoginResult{}, err
	}

	account, err := s.accounts.FindBySubject(ctx, s.channel, subject)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// 账号不存在与密码错误必须返回同一文案，不写登录尝试（无账号主键）。
			if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, accountRef, "", "account", accountRef,
				audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil); auditErr != nil {
				return LoginResult{}, auditErr
			}
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
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

	credential, err := s.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			lockedNow, recordErr := s.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, in.IPHash, in.UAHash, now)
			if recordErr != nil {
				return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
			}
			if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
				audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil); auditErr != nil {
				return LoginResult{}, auditErr
			}
			if lockedNow {
				if lockedErr := s.emitAccountLocked(ctx, account, in.TraceID); lockedErr != nil {
					return LoginResult{}, lockedErr
				}
			}
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load credential failed", err)
	}

	needsRehash, err := s.password.Verify(credential.Hash, in.Password)
	if err != nil {
		lockedNow, recordErr := s.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, in.IPHash, in.UAHash, now)
		if recordErr != nil {
			return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
		}
		if auditErr := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
			audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil); auditErr != nil {
			return LoginResult{}, auditErr
		}
		if lockedNow {
			if lockedErr := s.emitAccountLocked(ctx, account, in.TraceID); lockedErr != nil {
				return LoginResult{}, lockedErr
			}
		}
		return LoginResult{}, err
	}

	if needsRehash {
		encoded, algorithm, hashErr := s.password.Hash(in.Password)
		if hashErr != nil {
			return LoginResult{}, hashErr
		}
		if replaceErr := credential.ReplaceHash(encoded, algorithm, now); replaceErr != nil {
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
	// 管理员重置密码后必须先完成改密：只下发一次性票据，不签发业务令牌。
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
	return s.challengeOrComplete(ctx, account, now, in.IPHash, in.UAHash, in.TraceID)
}

// challengeOrComplete 在密码 / 改密票据通过后执行 MFA 闸门或直接完成登录。
func (s *AuthService) challengeOrComplete(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	ipHash, uaHash, traceID string,
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
		return s.completeLogin(ctx, account, now, ipHash, uaHash, traceID)
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
	return s.completeLogin(ctx, account, now, in.IPHash, in.UAHash, in.TraceID)
}

// completeLogin 完成已通过主体验证的登录收口：激活 pending、记录成功、创建会话与审计。
//
// 调用方必须已完成 guard.Authorize；MFA 挑战由密码登录在调用前处理。
func (s *AuthService) completeLogin(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	ipHash, uaHash, traceID string,
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
	result, err := s.createSession(ctx, account, now)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthLogin, account.ID, account.MerchantID, "session", result.SessionID,
		audit.OutcomeSuccess, "", traceID, nil); err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

// Refresh 执行一次性刷新：旧会话轮换，同 Family 下创建新会话。
//
// 已 rotated 的令牌再次出现视为重放：撤销整条家庭链并记 auth.token.reused。
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (TokenPair, error) {
	var pair TokenPair
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.refreshInTx(txCtx, in)
		if err != nil {
			return err
		}
		pair = res
		return nil
	}); err != nil {
		return TokenPair{}, err
	}
	return pair, nil
}

// refreshInTx 是 Refresh 的事务内实现：重放撤销与新会话签发同一事务。
func (s *AuthService) refreshInTx(ctx context.Context, in RefreshInput) (TokenPair, error) {
	if !RefreshPrefixMatches(s.channel, in.RefreshToken) {
		return TokenPair{}, invalidToken()
	}
	hash, err := HashRefreshToken(in.RefreshToken)
	if err != nil {
		return TokenPair{}, invalidToken()
	}
	now := s.clock.Now().UTC()
	session, err := s.sessions.FindByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return TokenPair{}, invalidToken()
		}
		return TokenPair{}, apperr.Internal("load session failed", err)
	}
	if session.Status == domain.SessionStatusRotated {
		s.revokeFamily(ctx, session, now)
		if auditErr := s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
			audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil); auditErr != nil {
			return TokenPair{}, auditErr
		}
		return TokenPair{}, invalidToken()
	}
	if !session.IsActive(now) {
		return TokenPair{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, session.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			s.revokeFamily(ctx, session, now)
			return TokenPair{}, invalidToken()
		}
		return TokenPair{}, apperr.Internal("load account failed", err)
	}
	if account.IsDeleted() || account.Status == domain.AccountStatusDisabled ||
		account.TokenVersion != session.TokenVersion {
		s.revokeFamily(ctx, session, now)
		return TokenPair{}, invalidToken()
	}

	expected := session.Version
	if rotateErr := session.Rotate(now); rotateErr != nil {
		s.revokeFamily(ctx, session, now)
		return TokenPair{}, invalidToken()
	}
	if saveErr := s.sessions.Save(ctx, session, expected); saveErr != nil {
		if errors.Is(saveErr, apperr.ErrConflict) {
			// 并发刷新只有一个请求能完成 active → rotated，其余按重放处理。
			s.revokeFamily(ctx, session, now)
			if auditErr := s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
				audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil); auditErr != nil {
				return TokenPair{}, auditErr
			}
			return TokenPair{}, invalidToken()
		}
		return TokenPair{}, apperr.Internal("rotate session failed", saveErr)
	}

	pair, sessionID, err := s.issueNewSession(ctx, account, session.FamilyID, now)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthTokenRefresh, account.ID, account.MerchantID, "session", sessionID,
		audit.OutcomeSuccess, "", in.TraceID, map[string]string{"family_id": shortHash(session.FamilyID)}); err != nil {
		return TokenPair{}, err
	}
	return pair, nil
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
		expected := session.Version
		if revokeErr := session.Revoke(domain.RevokeReasonLogout, now); revokeErr != nil {
			return apperr.Internal("revoke session failed", revokeErr)
		}
		if saveErr := s.sessions.Save(txCtx, session, expected); saveErr != nil && !errors.Is(saveErr, apperr.ErrConflict) {
			return apperr.Internal("save session failed", saveErr)
		}
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthLogout, actor.AccountID, actor.MerchantID, "session", actor.SessionID,
			audit.OutcomeSuccess, "", actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		revokeToken = true
		return nil
	}); err != nil {
		return err
	}
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

// ListSessions 返回账号全部会话，供 A 端会话管理使用。
func (s *AuthService) ListSessions(ctx context.Context, actor SessionActor) ([]SessionView, error) {
	if actor.AccountID == "" {
		return nil, invalidToken()
	}
	sessions, err := s.sessions.ListByAccount(ctx, actor.AccountID)
	if err != nil {
		return nil, apperr.Internal("list sessions failed", err)
	}
	out := make([]SessionView, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, SessionView{
			ID:            session.ID,
			Current:       session.ID == actor.SessionID,
			Status:        session.Status.String(),
			CreatedAt:     session.CreatedAt,
			LastUsedAt:    session.LastUsedAt,
			ExpiresAt:     session.ExpiresAt,
			IdleExpiresAt: session.IdleExpiresAt,
			RevokedAt:     session.RevokedAt,
		})
	}
	return out, nil
}

// Me 返回当前登录主体资料。
//
// 只读：不写审计、不碰会话；不存在、软删、端别不一致与停用统一返回 40100，
// 避免通过该端点枚举账号状态。
func (s *AuthService) Me(ctx context.Context, actor SessionActor) (MeView, error) {
	if strings.TrimSpace(actor.AccountID) == "" {
		return MeView{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MeView{}, invalidToken()
		}
		return MeView{}, apperr.Internal("load account failed", err)
	}
	if account == nil || account.DeletedAt != nil || account.Channel != s.channel ||
		account.Status == domain.AccountStatusDisabled {
		return MeView{}, invalidToken()
	}
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
	return MeView{
		SubjectID:    account.ID,
		Subject:      account.Subject,
		Channel:      account.Channel,
		MerchantID:   account.MerchantID,
		Status:       account.Status,
		MFAEnabled:   mfaEnabled,
		TokenVersion: account.TokenVersion,
	}, nil
}

// RevokeSession 撤销账号下的指定会话；已被撤销时幂等返回。
//
// 会话撤销与审计在同一事务内；撤销当前会话时 jti 黑名单在提交后写入。
func (s *AuthService) RevokeSession(ctx context.Context, actor SessionActor, sessionID string) error {
	if actor.AccountID == "" || strings.TrimSpace(sessionID) == "" {
		return apperr.BadRequest("session id is required")
	}
	now := s.clock.Now().UTC()
	revokeToken := false
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		session, err := s.sessions.FindByID(txCtx, sessionID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.NotFound("session not found")
			}
			return apperr.Internal("load session failed", err)
		}
		if session.AccountID != actor.AccountID {
			// 不区分「不存在」与「不属于本账号」，避免横向枚举他人会话。
			return apperr.NotFound("session not found")
		}
		expected := session.Version
		if revokeErr := session.Revoke(domain.RevokeReasonLogout, now); revokeErr != nil {
			if errors.Is(revokeErr, domain.ErrSessionExpired) {
				return nil
			}
			return apperr.Internal("revoke session failed", revokeErr)
		}
		if saveErr := s.sessions.Save(txCtx, session, expected); saveErr != nil && !errors.Is(saveErr, apperr.ErrConflict) {
			return apperr.Internal("save session failed", saveErr)
		}
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthTokenRevoked, actor.AccountID, actor.MerchantID, "session", sessionID,
			audit.OutcomeSuccess, string(domain.RevokeReasonLogout), actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		revokeToken = sessionID == actor.SessionID
		return nil
	}); err != nil {
		return err
	}
	if !revokeToken {
		return nil
	}
	return s.revokeCurrentToken(ctx, actor, now)
}

// createSession 创建新会话并签发令牌。
func (s *AuthService) createSession(ctx context.Context, account *domain.Account, now time.Time) (LoginResult, error) {
	sessionID, err := s.ids.NextString()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate session id failed", err)
	}
	familyID, err := s.ids.NextString()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate family id failed", err)
	}
	pair, err := s.tokens.Issue(account, sessionID)
	if err != nil {
		return LoginResult{}, err
	}
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:            sessionID,
		AccountID:     account.ID,
		FamilyID:      familyID,
		RefreshHash:   pair.RefreshHash,
		TokenVersion:  account.TokenVersion,
		ExpiresAt:     now.Add(s.tokens.RefreshTTL()),
		IdleExpiresAt: s.idleExpiry(now),
		Now:           now,
	})
	if err != nil {
		return LoginResult{}, apperr.Internal("build session failed", err)
	}
	if err := s.sessions.Save(ctx, session, 0); err != nil {
		return LoginResult{}, apperr.Internal("save session failed", err)
	}
	return LoginResult{TokenPair: pair, SessionID: sessionID, AccountID: account.ID}, nil
}

// issueNewSession 在刷新时创建同 Family 的下一代会话。
func (s *AuthService) issueNewSession(
	ctx context.Context,
	account *domain.Account,
	familyID string,
	now time.Time,
) (TokenPair, string, error) {
	sessionID, err := s.ids.NextString()
	if err != nil {
		return TokenPair{}, "", apperr.Internal("generate session id failed", err)
	}
	pair, err := s.tokens.Issue(account, sessionID)
	if err != nil {
		return TokenPair{}, "", err
	}
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:            sessionID,
		AccountID:     account.ID,
		FamilyID:      familyID,
		RefreshHash:   pair.RefreshHash,
		TokenVersion:  account.TokenVersion,
		ExpiresAt:     now.Add(s.tokens.RefreshTTL()),
		IdleExpiresAt: s.idleExpiry(now),
		Now:           now,
	})
	if err != nil {
		return TokenPair{}, "", apperr.Internal("build session failed", err)
	}
	if err := s.sessions.Save(ctx, session, 0); err != nil {
		return TokenPair{}, "", apperr.Internal("save session failed", err)
	}
	return pair, sessionID, nil
}

// idleExpiry 计算空闲上限；超过刷新绝对有效期时由领域层裁剪为绝对有效期。
func (s *AuthService) idleExpiry(now time.Time) time.Time {
	if s.idleTimeout <= 0 {
		return time.Time{}
	}
	idle := now.Add(s.idleTimeout)
	absolute := now.Add(s.tokens.RefreshTTL())
	if idle.After(absolute) {
		return absolute
	}
	return idle
}

// revokeFamily 撤销整条会话链；失败不覆盖原始 401 语义，避免把重放暴露成 500。
func (s *AuthService) revokeFamily(ctx context.Context, session *domain.Session, now time.Time) {
	if session == nil || session.FamilyID == "" {
		return
	}
	_, _ = s.sessions.RevokeFamily(ctx, session.FamilyID, domain.RevokeReasonReplay, now)
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

// emitAudit 写入审计事件；未配置 emitter 时跳过。
func (s *AuthService) emitAudit(
	ctx context.Context,
	action, actorID, merchantID, resourceType, resourceID string,
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
		ActorType:    s.actorType,
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
