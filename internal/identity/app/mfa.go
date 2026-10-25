package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/totp"
)

// DefaultMFAIssuer 是 otpauth URI 的默认发行方标识。
const DefaultMFAIssuer = "AdminFrame"

// recoveryCodeCount 是绑定 MFA 时生成的一次性恢复码数量。
const recoveryCodeCount = 10

// MFASetupResult 是绑定初始化结果；密钥与恢复码只返回一次。
type MFASetupResult struct {
	// Secret Base32 编码的 TOTP 密钥，供客户端录入。
	Secret string
	// OTPAuthURI otpauth:// 扫描录入地址。
	OTPAuthURI string
	// RecoveryCodes 一次性恢复码；仅本次响应可见。
	RecoveryCodes []string
}

// MFASetupInput 是 mfa/setup 的两种模式入参（docs/identity-authn.md §7.2.1）。
//
// SetupTicket 非空时按「登录前强制绑定」处理（不需要 Actor）；否则使用 Actor 完成已登录绑定。
type MFASetupInput struct {
	// SetupTicket 登录返回的强制绑定票据原文。
	SetupTicket string
	// Actor 已登录会话；SetupTicket 为空时使用。
	Actor SessionActor
	// IPHash 客户端摘要；由 transport 计算，用于绑定限流。
	IPHash string
}

// MFAVerifyInput 是 mfa/verify 的三种模式入参。
//
// Ticket 非空时执行两步登录；SetupTicket 非空时执行强制绑定并直接签发令牌；
// 两者都为空时使用 Actor 确认已登录账号的首次绑定。两种票据互斥。
type MFAVerifyInput struct {
	// Ticket 两步登录票据原文。
	Ticket string
	// SetupTicket 强制绑定票据原文。
	SetupTicket string
	// Code 6 位动态码或一次性恢复码。
	Code string
	// Actor 绑定确认时的已认证上下文。
	Actor SessionActor
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// TraceID 请求关联 ID。
	TraceID string
}

// MFAVerifyResult 是 mfa/verify 的结果。
//
// BindingConfirmed 为 true 表示完成绑定确认（无令牌）；否则返回正式令牌。
type MFAVerifyResult struct {
	TokenPair
	SessionID        string
	AccountID        string
	BindingConfirmed bool
}

// SetupMFA 生成 TOTP 密钥与恢复码，保存未确认绑定并返回一次性录入信息。
//
// 登录前强制绑定凭 SetupTicket，已登录绑定凭 Actor；已确认绑定再次调用返回 40900。
func (s *AuthService) SetupMFA(ctx context.Context, in MFASetupInput) (MFASetupResult, error) {
	if s.channel != domain.ChannelAdmin {
		return MFASetupResult{}, apperr.BadRequest("mfa is only supported on admin channel")
	}
	if s.mfa == nil || s.mfaCipher == nil {
		return MFASetupResult{}, apperr.Unavailable("mfa is not configured")
	}
	now := s.clock.Now().UTC()
	account, err := s.mfaSetupAccount(ctx, in, now)
	if err != nil {
		return MFASetupResult{}, err
	}
	existing, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return MFASetupResult{}, apperr.Internal("load mfa enrollment failed", err)
	}
	if existing != nil && existing.IsConfirmed() {
		return MFASetupResult{}, apperr.Conflict("mfa is already bound")
	}

	secret, err := totp.GenerateSecret()
	if err != nil {
		return MFASetupResult{}, apperr.Internal("generate mfa secret failed", err)
	}
	ciphertext, err := s.mfaCipher.Seal(secret, mfaAAD(account.ID))
	if err != nil {
		return MFASetupResult{}, apperr.Internal("encrypt mfa secret failed", err)
	}
	enrollment, err := domain.NewMFAEnrollment(account.ID, ciphertext, now)
	if err != nil {
		return MFASetupResult{}, err
	}
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return MFASetupResult{}, err
	}
	if err := enrollment.SetRecoveryCodes(hashes, now); err != nil {
		return MFASetupResult{}, err
	}
	if err := s.mfa.Save(ctx, enrollment); err != nil {
		return MFASetupResult{}, apperr.Internal("save mfa enrollment failed", err)
	}

	encoded := totp.EncodeSecret(secret)
	return MFASetupResult{
		Secret:        encoded,
		OTPAuthURI:    totp.ProvisioningURI(s.mfaIssuer, account.Subject, encoded),
		RecoveryCodes: codes,
	}, nil
}

// mfaSetupAccount 解析绑定目标账号：票据优先，其次已认证会话。
//
// 票据路径每次重新读取账号状态，禁用 / 锁定立即失效，不依赖票据内的快照。
func (s *AuthService) mfaSetupAccount(ctx context.Context, in MFASetupInput, now time.Time) (*domain.Account, error) {
	if strings.TrimSpace(in.SetupTicket) != "" {
		if s.mfaTickets == nil {
			return nil, apperr.Unavailable("mfa is not configured")
		}
		claims, err := s.tokens.ParseMFASetupTicket(in.SetupTicket)
		if err != nil {
			return nil, err
		}
		if claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(now) {
			return nil, invalidToken()
		}
		if err := s.guard.CheckRateLimit(mfaSetupRateKey(s.channel, claims.ID, in.IPHash)); err != nil {
			return nil, err
		}
		return s.loadMFAAccount(ctx, claims.Subject, now)
	}
	if strings.TrimSpace(in.Actor.AccountID) != "" {
		return s.loadMFAAccount(ctx, in.Actor.AccountID, now)
	}
	return nil, invalidToken()
}

// loadMFAAccount 加载并校验账号可执行 MFA 操作。
func (s *AuthService) loadMFAAccount(ctx context.Context, accountID string, now time.Time) (*domain.Account, error) {
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, invalidToken()
		}
		return nil, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return nil, err
	}
	return account, nil
}

// VerifyMFA 完成两步登录、强制绑定或已登录绑定确认。
//
// 票据优先且互斥：Ticket 非空按两步登录；SetupTicket 非空按强制绑定；
// 两者同时出现返回 40000，避免请求语义歧义。
func (s *AuthService) VerifyMFA(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	var result MFAVerifyResult
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.verifyMFAInTx(txCtx, in)
		if err != nil {
			return err
		}
		result = res
		return nil
	}); err != nil {
		return MFAVerifyResult{}, err
	}
	return result, nil
}

// verifyMFAInTx 是 VerifyMFA 的事务内实现。
func (s *AuthService) verifyMFAInTx(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	if s.channel != domain.ChannelAdmin {
		return MFAVerifyResult{}, apperr.BadRequest("mfa is only supported on admin channel")
	}
	if s.mfa == nil || s.mfaCipher == nil || s.mfaTickets == nil {
		return MFAVerifyResult{}, apperr.Unavailable("mfa is not configured")
	}
	hasTicket := strings.TrimSpace(in.Ticket) != ""
	hasSetupTicket := strings.TrimSpace(in.SetupTicket) != ""
	if hasTicket && hasSetupTicket {
		return MFAVerifyResult{}, apperr.BadRequest("mfa_ticket and mfa_setup_ticket are mutually exclusive")
	}
	if hasTicket {
		return s.completeMFALogin(ctx, in)
	}
	if hasSetupTicket {
		return s.completeMFASetup(ctx, in)
	}
	if strings.TrimSpace(in.Actor.AccountID) != "" {
		return s.confirmMFABinding(ctx, in)
	}
	return MFAVerifyResult{}, invalidToken()
}

// DisableMFA 校验动态码后解绑，并强制撤销账号全部会话。
//
// 解绑、会话撤销与审计在同一事务内；撤销快照在提交后写入。
func (s *AuthService) DisableMFA(ctx context.Context, actor SessionActor, code string) error {
	if s.channel != domain.ChannelAdmin {
		return apperr.BadRequest("mfa is only supported on admin channel")
	}
	if s.mfa == nil || s.mfaCipher == nil {
		return apperr.Unavailable("mfa is not configured")
	}
	if strings.TrimSpace(actor.AccountID) == "" {
		return invalidToken()
	}
	now := s.clock.Now().UTC()
	var account *domain.Account
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		loaded, err := s.accounts.FindByID(txCtx, actor.AccountID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return invalidToken()
			}
			return apperr.Internal("load account failed", err)
		}
		if authErr := s.guard.Authorize(loaded, now); authErr != nil {
			return authErr
		}
		enrollment, err := s.mfa.FindByAccount(txCtx, loaded.ID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return invalidToken()
			}
			return apperr.Internal("load mfa enrollment failed", err)
		}
		if !enrollment.IsConfirmed() {
			return invalidToken()
		}
		ok, err := s.verifyEnrollmentCode(txCtx, enrollment, code, now, true)
		if err != nil {
			return err
		}
		if !ok {
			return s.mfaFailure(txCtx, loaded, "", "", actor.TraceID)
		}
		if err := s.mfa.Delete(txCtx, loaded.ID); err != nil {
			return apperr.Internal("delete mfa enrollment failed", err)
		}
		if err := s.revokeAllSessionsInTx(txCtx, loaded, now); err != nil {
			return err
		}
		if auditErr := s.emitAudit(txCtx, audit.ActionAuthMFADisabled, loaded.ID, loaded.MerchantID, "account", loaded.ID,
			audit.OutcomeSuccess, "", actor.TraceID, nil); auditErr != nil {
			return auditErr
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	return s.saveRevocationState(ctx, account)
}

// mfaGate 是密码校验通过后的 MFA 闸门结论。
type mfaGate int

const (
	// mfaGateNotRequired 无需 MFA，可直接签发令牌。
	mfaGateNotRequired mfaGate = iota
	// mfaGateVerify 已绑定，需要完成两步校验。
	mfaGateVerify
	// mfaGateBind 强制绑定但尚未完成，需要先绑定。
	mfaGateBind
)

// mfaChallenge 判断登录后的 MFA 闸门，并在强制绑定时写失败审计。
func (s *AuthService) mfaChallenge(
	ctx context.Context,
	account *domain.Account,
	traceID string,
) (mfaGate, error) {
	if s.channel != domain.ChannelAdmin {
		return mfaGateNotRequired, nil
	}
	if s.mfa == nil {
		if s.mfaRequired {
			// 强制绑定但 MFA 依赖未装配：失败关闭，避免把用户引入必然失败的重置流程。
			return mfaGateNotRequired, apperr.Unavailable("mfa is not configured")
		}
		return mfaGateNotRequired, nil
	}
	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if s.mfaRequired {
				return mfaGateBind, s.recordMFABindingPending(ctx, account, traceID)
			}
			return mfaGateNotRequired, nil
		}
		return mfaGateNotRequired, apperr.Internal("load mfa enrollment failed", err)
	}
	if !enrollment.IsConfirmed() {
		if s.mfaRequired {
			return mfaGateBind, s.recordMFABindingPending(ctx, account, traceID)
		}
		return mfaGateNotRequired, nil
	}
	if s.mfaCipher == nil || s.mfaTickets == nil {
		return mfaGateNotRequired, apperr.Unavailable("mfa is not configured")
	}
	return mfaGateVerify, nil
}

// recordMFABindingPending 记录「密码已通过但需要绑定 MFA」的审计。
//
// 这是认证未完成而非失败：对外返回 200 + mfa_setup_ticket（§7.2.1），
// 因此不返回错误，仅保证审计写入失败时整体失败关闭。
func (s *AuthService) recordMFABindingPending(ctx context.Context, account *domain.Account, traceID string) error {
	return s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeDenied, domain.LoginReasonMFAPending, traceID, nil)
}

// completeMFALogin 校验票据与动态码，成功后创建正式会话并签发令牌。
func (s *AuthService) completeMFALogin(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	now := s.clock.Now().UTC()
	claims, err := s.tokens.ParseMFATicket(in.Ticket)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(now) {
		return MFAVerifyResult{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, claims.Subject)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, invalidToken()
		}
		return MFAVerifyResult{}, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return MFAVerifyResult{}, err
	}
	if err := s.guard.CheckRateLimit(mfaRateKey(s.channel, claims.ID, in.IPHash)); err != nil {
		return MFAVerifyResult{}, err
	}

	ttl := claims.ExpiresAt.Sub(now)
	consumed, err := s.mfaTickets.Consume(ctx, claims.ID, ttl)
	if err != nil {
		return MFAVerifyResult{}, apperr.New(apperr.CodeServiceUnavailable,
			"mfa ticket store unavailable", errors.Join(apperr.ErrUnavailable, err))
	}
	if !consumed {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}

	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
		}
		return MFAVerifyResult{}, apperr.Internal("load mfa enrollment failed", err)
	}
	if !enrollment.IsConfirmed() {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}
	ok, err := s.verifyEnrollmentCode(ctx, enrollment, in.Code, now, true)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if !ok {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}

	if err := s.guard.RecordSuccess(ctx, account, in.IPHash, in.UAHash, now); err != nil {
		return MFAVerifyResult{}, apperr.Internal("record login success failed", err)
	}
	result, err := s.createSession(ctx, account, now)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if err := s.emitAudit(ctx, audit.ActionAuthLogin, account.ID, account.MerchantID, "session", result.SessionID,
		audit.OutcomeSuccess, "", in.TraceID, nil); err != nil {
		return MFAVerifyResult{}, err
	}
	return MFAVerifyResult{
		TokenPair: result.TokenPair,
		SessionID: result.SessionID,
		AccountID: account.ID,
	}, nil
}

// completeMFASetup 校验强制绑定票据与首码：确认绑定并直接签发令牌（§7.2.1）。
//
// 票据一次性消费，首码错误同样作废，与两步登录保持一致；前端需回到密码步骤重新登录。
// 绑定成功即完成登录，不再要求第二次密码输入。
func (s *AuthService) completeMFASetup(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	now := s.clock.Now().UTC()
	claims, err := s.tokens.ParseMFASetupTicket(in.SetupTicket)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(now) {
		return MFAVerifyResult{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, claims.Subject)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, invalidToken()
		}
		return MFAVerifyResult{}, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return MFAVerifyResult{}, err
	}
	if err := s.guard.CheckRateLimit(mfaSetupRateKey(s.channel, claims.ID, in.IPHash)); err != nil {
		return MFAVerifyResult{}, err
	}

	ttl := claims.ExpiresAt.Sub(now)
	consumed, err := s.mfaTickets.Consume(ctx, mfaSetupTicketStoreKey(claims.ID), ttl)
	if err != nil {
		return MFAVerifyResult{}, apperr.New(apperr.CodeServiceUnavailable,
			"mfa ticket store unavailable", errors.Join(apperr.ErrUnavailable, err))
	}
	if !consumed {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}

	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
		}
		return MFAVerifyResult{}, apperr.Internal("load mfa enrollment failed", err)
	}
	if enrollment.IsConfirmed() {
		// 票据签发后绑定已被其他请求确认：不再重复确认，直接按失败关闭处理。
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}
	ok, err := s.verifyEnrollmentCode(ctx, enrollment, in.Code, now, false)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if !ok {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}
	if err := enrollment.Confirm(now); err != nil {
		return MFAVerifyResult{}, err
	}
	if err := s.mfa.Save(ctx, enrollment); err != nil {
		return MFAVerifyResult{}, apperr.Internal("save mfa enrollment failed", err)
	}
	if err := s.emitAudit(ctx, audit.ActionAuthMFABound, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeSuccess, "", in.TraceID, nil); err != nil {
		return MFAVerifyResult{}, err
	}

	login, err := s.completeLogin(ctx, account, now, in.IPHash, in.UAHash, in.TraceID)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	return MFAVerifyResult{
		TokenPair: login.TokenPair,
		SessionID: login.SessionID,
		AccountID: account.ID,
	}, nil
}

// confirmMFABinding 使用首个动态码确认未完成的绑定。
func (s *AuthService) confirmMFABinding(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	now := s.clock.Now().UTC()
	actor := in.Actor
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, invalidToken()
		}
		return MFAVerifyResult{}, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return MFAVerifyResult{}, err
	}
	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFAVerifyResult{}, invalidToken()
		}
		return MFAVerifyResult{}, apperr.Internal("load mfa enrollment failed", err)
	}
	if enrollment.IsConfirmed() {
		return MFAVerifyResult{}, apperr.Conflict("mfa is already confirmed")
	}
	ok, err := s.verifyEnrollmentCode(ctx, enrollment, in.Code, now, false)
	if err != nil {
		return MFAVerifyResult{}, err
	}
	if !ok {
		return MFAVerifyResult{}, s.mfaFailure(ctx, account, in.IPHash, in.UAHash, in.TraceID)
	}
	if err := enrollment.Confirm(now); err != nil {
		return MFAVerifyResult{}, err
	}
	if err := s.mfa.Save(ctx, enrollment); err != nil {
		return MFAVerifyResult{}, apperr.Internal("save mfa enrollment failed", err)
	}
	if err := s.emitAudit(ctx, audit.ActionAuthMFABound, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeSuccess, "", in.TraceID, nil); err != nil {
		return MFAVerifyResult{}, err
	}
	return MFAVerifyResult{AccountID: account.ID, BindingConfirmed: true}, nil
}

// verifyEnrollmentCode 先校验 TOTP；allowRecovery 时再尝试一次性恢复码。
func (s *AuthService) verifyEnrollmentCode(
	ctx context.Context,
	enrollment *domain.MFAEnrollment,
	code string,
	now time.Time,
	allowRecovery bool,
) (bool, error) {
	if s.mfaCipher == nil {
		return false, apperr.Unavailable("mfa is not configured")
	}
	secret, err := s.mfaCipher.Open(enrollment.SecretCiphertext, mfaAAD(enrollment.AccountID))
	if err != nil {
		return false, apperr.Internal("decrypt mfa secret failed", err)
	}
	if totp.Validate(secret, code, now) {
		return true, nil
	}
	if !allowRecovery {
		return false, nil
	}
	if !enrollment.UseRecoveryCode(hashRecoveryCode(code), now) {
		return false, nil
	}
	if err := s.mfa.Save(ctx, enrollment); err != nil {
		return false, apperr.Internal("consume recovery code failed", err)
	}
	return true, nil
}

// mfaFailure 记录一次 MFA 失败尝试并写审计，返回统一 40100。
func (s *AuthService) mfaFailure(
	ctx context.Context,
	account *domain.Account,
	ipHash, uaHash, traceID string,
) error {
	now := s.clock.Now().UTC()
	if err := s.guard.RecordFailure(ctx, account, domain.LoginReasonMFAFailed, ipHash, uaHash, now); err != nil {
		return apperr.Internal("record mfa failure failed", err)
	}
	if err := s.emitAudit(ctx, audit.ActionAuthMFAFailed, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeFailure, domain.LoginReasonMFAFailed, traceID, nil); err != nil {
		return err
	}
	if err := s.emitAccountLockedIfNeeded(ctx, account, traceID); err != nil {
		return err
	}
	return invalidToken()
}

// revokeAllSessionsInTx 递增 token_version 并撤销全部会话（事务内）。
func (s *AuthService) revokeAllSessionsInTx(ctx context.Context, account *domain.Account, now time.Time) error {
	expected := account.Version
	if err := account.BumpTokenVersion(now); err != nil {
		return apperr.Internal("bump token version failed", err)
	}
	if err := s.accounts.Save(ctx, account, expected); err != nil {
		return apperr.Internal("save account failed", err)
	}
	if _, err := s.sessions.RevokeByAccount(ctx, account.ID, domain.RevokeReasonLogoutAll, now); err != nil {
		return apperr.Internal("revoke account sessions failed", err)
	}
	return nil
}

// saveRevocationState 在事务提交后刷新账号撤销快照；Redis 写入不可回滚。
func (s *AuthService) saveRevocationState(ctx context.Context, account *domain.Account) error {
	if s.revocation == nil || account == nil {
		return nil
	}
	state := AccountState{Version: account.TokenVersion, Status: account.Status}
	if err := s.revocation.SaveAccountState(ctx, account.ID, state, s.accountStateTTL()); err != nil {
		return apperr.Internal("refresh revocation state failed", err)
	}
	return nil
}

// generateRecoveryCodes 生成一次性恢复码与其 SHA-256 哈希。
func generateRecoveryCodes() (codes, hashes []string, err error) {
	codes = make([]string, 0, recoveryCodeCount)
	hashes = make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		buf := make([]byte, 5)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, apperr.Internal("generate recovery code failed", err)
		}
		code := totp.EncodeSecret(buf)
		codes = append(codes, code)
		hashes = append(hashes, hashRecoveryCode(code))
	}
	return codes, hashes, nil
}

// hashRecoveryCode 返回规范化恢复码的 SHA-256 十六进制摘要。
func hashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// mfaAAD 绑定账号 ID 作为附加认证数据，防止密文跨账号挪用。
func mfaAAD(accountID string) []byte {
	return []byte("identity:mfa:" + accountID)
}

// mfaRateKey 构造 MFA 校验限流键：channel + 票据 jti + IP 摘要。
func mfaRateKey(channel domain.Channel, jti, ipHash string) string {
	if ipHash == "" {
		return string(channel) + ":mfa:" + jti
	}
	return string(channel) + ":mfa:" + jti + ":" + ipHash
}

// mfaSetupRateKey 构造强制绑定流程的限流键，与两步登录分桶（§7.2.1）。
func mfaSetupRateKey(channel domain.Channel, jti, ipHash string) string {
	if ipHash == "" {
		return string(channel) + ":mfa_setup:" + jti
	}
	return string(channel) + ":mfa_setup:" + jti + ":" + ipHash
}

// mfaSetupTicketStoreKey 把绑定票据的 jti 放入独立命名空间。
//
// 一次性消费存储只接收 jti 字符串，前缀隔离避免两类票据共用同一键空间；
// 即使 jti 碰撞也不会互相消费（§7.2.1「键与 mfa_ticket 隔离」）。
func mfaSetupTicketStoreKey(jti string) string {
	return "setup:" + jti
}
