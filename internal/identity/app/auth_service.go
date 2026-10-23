package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// 默认空闲会话上限（docs/identity-authn.md §10）。
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
	Audit       audit.Emitter
	IDs         IDGenerator
	Channel     domain.Channel
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

// AuthService 编排登录、刷新、登出与会话撤销（docs/identity-authn.md §5）。
type AuthService struct {
	accounts    domain.AccountRepository
	credentials domain.CredentialRepository
	sessions    domain.SessionRepository
	guard       *LoginGuard
	password    *PasswordService
	tokens      *TokenService
	revocation  RevocationStore
	audit       audit.Emitter
	ids         IDGenerator
	channel     domain.Channel
	idleTimeout time.Duration
	service     string
	actorType   string
	clock       Clock
	mfa         domain.MFAEnrollmentRepository
	mfaCipher   *crypto.AEAD
	mfaTickets  MFATicketStore
	mfaIssuer   string
	mfaRequired bool
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
		if cfg.Channel == domain.ChannelBiz {
			actorType = "merchant_staff"
		} else {
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
	svc := &AuthService{
		accounts:    cfg.Accounts,
		credentials: cfg.Credentials,
		sessions:    cfg.Sessions,
		guard:       cfg.Guard,
		password:    cfg.Password,
		tokens:      cfg.Tokens,
		revocation:  cfg.Revocation,
		audit:       cfg.Audit,
		ids:         cfg.IDs,
		channel:     cfg.Channel,
		idleTimeout: idle,
		service:     cfg.Service,
		actorType:   actorType,
		clock:       clock,
		mfa:         cfg.MFA,
		mfaCipher:   cfg.MFACipher,
		mfaTickets:  cfg.MFATickets,
		mfaIssuer:   mfaIssuer,
		mfaRequired: cfg.MFAAdminRequired,
	}
	return svc, nil
}

// Login 校验密码并签发令牌；失败时不创建会话。
//
// 账号不存在、密码错误、停用与锁定统一返回 40100 文案，真实原因只写审计。
func (s *AuthService) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
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
			_ = s.emitAudit(ctx, audit.ActionAuthLoginFailed, accountRef, "", "account", accountRef,
				audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil)
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}

	if err := s.guard.Authorize(account, now); err != nil {
		reason := reasonFromAuthError(err)
		if recordErr := s.guard.RecordFailure(ctx, account, reason, in.IPHash, in.UAHash, now); recordErr != nil {
			return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
		}
		_ = s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
			audit.OutcomeFailure, reason, in.TraceID, nil)
		return LoginResult{}, err
	}

	credential, err := s.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if recordErr := s.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, in.IPHash, in.UAHash, now); recordErr != nil {
				return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
			}
			_ = s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
				audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil)
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load credential failed", err)
	}

	needsRehash, err := s.password.Verify(credential.Hash, in.Password)
	if err != nil {
		if recordErr := s.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, in.IPHash, in.UAHash, now); recordErr != nil {
			return LoginResult{}, apperr.Internal("record login failure failed", recordErr)
		}
		_ = s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
			audit.OutcomeFailure, domain.LoginReasonBadCredential, in.TraceID, nil)
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
	required, err := s.mfaChallenge(ctx, account, now, in.TraceID)
	if err != nil {
		return LoginResult{}, err
	}
	if required {
		ticket, issueErr := s.tokens.IssueMFATicket(account.ID)
		if issueErr != nil {
			return LoginResult{}, issueErr
		}
		return LoginResult{
			AccountID:   account.ID,
			MFARequired: true,
			MFATicket:   ticket.Ticket,
		}, nil
	}
	if err := s.guard.RecordSuccess(ctx, account, in.IPHash, in.UAHash, now); err != nil {
		return LoginResult{}, apperr.Internal("record login success failed", err)
	}

	result, err := s.createSession(ctx, account, now)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthLogin, account.ID, account.MerchantID, "session", result.SessionID,
		audit.OutcomeSuccess, "", in.TraceID, nil); err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

// Refresh 执行一次性刷新：旧会话轮换，同 Family 下创建新会话。
//
// 已 rotated 的令牌再次出现视为重放：撤销整条家庭链并记 auth.token.reused。
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (TokenPair, error) {
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
		_ = s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
			audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil)
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
			_ = s.emitAudit(ctx, audit.ActionAuthTokenReused, session.AccountID, "", "session", session.ID,
				audit.OutcomeFailure, domain.LoginReasonReplay, in.TraceID, nil)
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
func (s *AuthService) Logout(ctx context.Context, actor SessionActor) error {
	now := s.clock.Now().UTC()
	if actor.AccountID == "" || actor.SessionID == "" {
		return invalidToken()
	}
	session, err := s.sessions.FindByID(ctx, actor.SessionID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return s.revokeCurrentToken(ctx, actor, now)
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
	if saveErr := s.sessions.Save(ctx, session, expected); saveErr != nil && !errors.Is(saveErr, apperr.ErrConflict) {
		return apperr.Internal("save session failed", saveErr)
	}
	if err := s.revokeCurrentToken(ctx, actor, now); err != nil {
		return err
	}
	return s.emitAudit(ctx, audit.ActionAuthLogout, actor.AccountID, actor.MerchantID, "session", actor.SessionID,
		audit.OutcomeSuccess, "", actor.TraceID, nil)
}

// LogoutAll 递增 token_version 并撤销账号全部会话。
func (s *AuthService) LogoutAll(ctx context.Context, actor SessionActor) error {
	now := s.clock.Now().UTC()
	if actor.AccountID == "" {
		return invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
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
	if saveErr := s.accounts.Save(ctx, account, expected); saveErr != nil {
		return apperr.Internal("save account failed", saveErr)
	}
	if _, err := s.sessions.RevokeByAccount(ctx, account.ID, domain.RevokeReasonLogoutAll, now); err != nil {
		return apperr.Internal("revoke account sessions failed", err)
	}
	if s.revocation != nil {
		state := AccountState{Version: account.TokenVersion, Status: account.Status}
		ttl := s.accountStateTTL()
		if err := s.revocation.SaveAccountState(ctx, account.ID, state, ttl); err != nil {
			return apperr.Internal("refresh revocation state failed", err)
		}
	}
	if err := s.revokeCurrentToken(ctx, actor, now); err != nil {
		return err
	}
	return s.emitAudit(ctx, audit.ActionAuthTokenRevoked, actor.AccountID, actor.MerchantID, "account", actor.AccountID,
		audit.OutcomeSuccess, string(domain.RevokeReasonLogoutAll), actor.TraceID, nil)
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

// RevokeSession 撤销账号下的指定会话；已被撤销时幂等返回。
func (s *AuthService) RevokeSession(ctx context.Context, actor SessionActor, sessionID string) error {
	if actor.AccountID == "" || strings.TrimSpace(sessionID) == "" {
		return apperr.BadRequest("session id is required")
	}
	now := s.clock.Now().UTC()
	session, err := s.sessions.FindByID(ctx, sessionID)
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
	if saveErr := s.sessions.Save(ctx, session, expected); saveErr != nil && !errors.Is(saveErr, apperr.ErrConflict) {
		return apperr.Internal("save session failed", saveErr)
	}
	if sessionID == actor.SessionID {
		if err := s.revokeCurrentToken(ctx, actor, now); err != nil {
			return err
		}
	}
	return s.emitAudit(ctx, audit.ActionAuthTokenRevoked, actor.AccountID, actor.MerchantID, "session", sessionID,
		audit.OutcomeSuccess, string(domain.RevokeReasonLogout), actor.TraceID, nil)
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

// accountStateTTL 返回账号撤销快照缓存 TTL：access_ttl + leeway，下限 1h。
func (s *AuthService) accountStateTTL() time.Duration {
	ttl := s.tokens.AccessTTL() + time.Minute
	if ttl < minAccountStateTTL {
		return minAccountStateTTL
	}
	return ttl
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
