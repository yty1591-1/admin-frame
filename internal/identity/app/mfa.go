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

// MFAVerifyInput 是 mfa/verify 的两种模式入参。
//
// Ticket 非空时执行两步登录（不需要 Actor）；否则使用 Actor 完成绑定确认。
type MFAVerifyInput struct {
	// Ticket 两步登录票据原文。
	Ticket string
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
func (s *AuthService) SetupMFA(ctx context.Context, actor SessionActor) (MFASetupResult, error) {
	if s.channel != domain.ChannelAdmin {
		return MFASetupResult{}, apperr.BadRequest("mfa is only supported on admin channel")
	}
	if s.mfa == nil || s.mfaCipher == nil {
		return MFASetupResult{}, apperr.Unavailable("mfa is not configured")
	}
	if strings.TrimSpace(actor.AccountID) == "" {
		return MFASetupResult{}, invalidToken()
	}
	now := s.clock.Now().UTC()
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return MFASetupResult{}, invalidToken()
		}
		return MFASetupResult{}, apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
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

// VerifyMFA 完成两步登录或绑定确认。
//
// 票据优先：只要 Ticket 非空就按两步登录处理，忽略 Actor，避免请求同时携带
// 两种凭据时出现语义歧义。
func (s *AuthService) VerifyMFA(ctx context.Context, in MFAVerifyInput) (MFAVerifyResult, error) {
	if s.channel != domain.ChannelAdmin {
		return MFAVerifyResult{}, apperr.BadRequest("mfa is only supported on admin channel")
	}
	if s.mfa == nil || s.mfaCipher == nil || s.mfaTickets == nil {
		return MFAVerifyResult{}, apperr.Unavailable("mfa is not configured")
	}
	if strings.TrimSpace(in.Ticket) != "" {
		return s.completeMFALogin(ctx, in)
	}
	if strings.TrimSpace(in.Actor.AccountID) != "" {
		return s.confirmMFABinding(ctx, in)
	}
	return MFAVerifyResult{}, invalidToken()
}

// DisableMFA 校验动态码后解绑，并强制撤销账号全部会话。
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
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return invalidToken()
		}
		return apperr.Internal("load account failed", err)
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return err
	}
	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return invalidToken()
		}
		return apperr.Internal("load mfa enrollment failed", err)
	}
	if !enrollment.IsConfirmed() {
		return invalidToken()
	}
	ok, err := s.verifyEnrollmentCode(ctx, enrollment, code, now, true)
	if err != nil {
		return err
	}
	if !ok {
		return s.mfaFailure(ctx, account, "", "", actor.TraceID)
	}
	if err := s.mfa.Delete(ctx, account.ID); err != nil {
		return apperr.Internal("delete mfa enrollment failed", err)
	}
	if err := s.revokeAllSessions(ctx, account, now); err != nil {
		return err
	}
	return s.emitAudit(ctx, audit.ActionAuthMFADisabled, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeSuccess, "", actor.TraceID, nil)
}

// mfaChallenge 判断登录是否需要 MFA，并在强制绑定时返回稳定错误。
func (s *AuthService) mfaChallenge(
	ctx context.Context,
	account *domain.Account,
	now time.Time,
	traceID string,
) (bool, error) {
	if s.channel != domain.ChannelAdmin {
		return false, nil
	}
	if s.mfa == nil {
		if s.mfaRequired {
			return false, s.mfaBindingRequired(ctx, account, traceID)
		}
		return false, nil
	}
	enrollment, err := s.mfa.FindByAccount(ctx, account.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			if s.mfaRequired {
				return false, s.mfaBindingRequired(ctx, account, traceID)
			}
			return false, nil
		}
		return false, apperr.Internal("load mfa enrollment failed", err)
	}
	if !enrollment.IsConfirmed() {
		if s.mfaRequired {
			return false, s.mfaBindingRequired(ctx, account, traceID)
		}
		return false, nil
	}
	if s.mfaCipher == nil || s.mfaTickets == nil {
		return false, apperr.Unavailable("mfa is not configured")
	}
	return true, nil
}

// mfaBindingRequired 记录「需要绑定 MFA」的失败审计并返回稳定 40100。
func (s *AuthService) mfaBindingRequired(ctx context.Context, account *domain.Account, traceID string) error {
	if err := s.emitAudit(ctx, audit.ActionAuthLoginFailed, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeDenied, domain.LoginReasonMFAPending, traceID, nil); err != nil {
		return err
	}
	return apperr.New(apperr.CodeUnauthorized, "mfa binding required",
		errors.Join(domain.ErrMFARequired, apperr.ErrUnauthorized))
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
	return invalidToken()
}

// revokeAllSessions 递增 token_version、撤销全部会话并刷新撤销缓存。
func (s *AuthService) revokeAllSessions(ctx context.Context, account *domain.Account, now time.Time) error {
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
	if s.revocation != nil {
		state := AccountState{Version: account.TokenVersion, Status: account.Status}
		if err := s.revocation.SaveAccountState(ctx, account.ID, state, s.accountStateTTL()); err != nil {
			return apperr.Internal("refresh revocation state failed", err)
		}
	}
	return nil
}

// generateRecoveryCodes 生成一次性恢复码与其 SHA-256 哈希。
func generateRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([]string, 0, recoveryCodeCount)
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
