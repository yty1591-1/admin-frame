package app

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// 临时口令参数（IDN-P0-02）。
const (
	// temporaryPasswordLength 临时口令长度；需包含四类字符中的至少三类。
	temporaryPasswordLength = 20
	// reasonPasswordResetCompletion 强制改密完成时的审计原因。
	reasonPasswordResetCompletion = "password_reset_completion"
)

// temporaryPasswordClasses 临时口令字符集；剔除易混淆字符（0/O、1/l/I）。
var temporaryPasswordClasses = []string{
	"abcdefghijkmnpqrstuvwxyz",
	"ABCDEFGHJKLMNPQRSTUVWXYZ",
	"23456789",
	"!@#$%^&*-_=+",
}

// temporaryPasswordAlphabet 是全部可用字符的并集。
var temporaryPasswordAlphabet = strings.Join(temporaryPasswordClasses, "")

// CompletePasswordChangeInput 是凭强制改密票据完成改密的入参。
type CompletePasswordChangeInput struct {
	// Ticket 登录返回的 password_change_ticket。
	Ticket string
	// NewPassword 新密码；须满足密码策略且不得与临时口令相同。
	NewPassword string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// TraceID 请求关联 ID。
	TraceID string
}

// CompletePasswordChange 使用一次性票据完成管理员重置后的强制改密。
//
// 校验票据（scope=password_change、5 分钟、一次性）后更新密码、清除标记、
// 撤销全部会话，并复用 MFA 闸门决定直接签发令牌还是继续两步校验。
func (s *AuthService) CompletePasswordChange(ctx context.Context, in CompletePasswordChangeInput) (LoginResult, error) {
	if s.channel != domain.ChannelAdmin {
		return LoginResult{}, apperr.BadRequest("password change ticket is only supported on admin channel")
	}
	if s.mfaTickets == nil {
		return LoginResult{}, apperr.Unavailable("password change ticket store is not configured")
	}
	var result LoginResult
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.completePasswordChangeInTx(txCtx, in)
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

// completePasswordChangeInTx 是 CompletePasswordChange 的事务内实现。
func (s *AuthService) completePasswordChangeInTx(ctx context.Context, in CompletePasswordChangeInput) (LoginResult, error) {
	now := s.clock.Now().UTC()
	claims, err := s.tokens.ParsePasswordChangeTicket(in.Ticket)
	if err != nil {
		return LoginResult{}, err
	}
	if claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(now) {
		return LoginResult{}, invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, claims.Subject)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, invalidToken()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}
	if account.Channel != s.channel {
		return LoginResult{}, invalidToken()
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return LoginResult{}, err
	}
	if err := s.guard.CheckRateLimit(passwordChangeRateKey(s.channel, claims.ID, in.IPHash)); err != nil {
		return LoginResult{}, err
	}

	ttl := claims.ExpiresAt.Sub(now)
	consumed, err := s.mfaTickets.Consume(ctx, passwordTicketStoreKey(claims.ID), ttl)
	if err != nil {
		return LoginResult{}, apperr.New(apperr.CodeServiceUnavailable,
			"password change ticket store unavailable", errors.Join(apperr.ErrUnavailable, err))
	}
	if !consumed {
		// 票据重放不累计失败次数，避免恶意锁定。
		return LoginResult{}, invalidToken()
	}
	if !account.MustChangePassword {
		return LoginResult{}, invalidToken()
	}

	credential, err := s.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePassword)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("load credential failed", err)
	}
	if _, verifyErr := s.password.Verify(credential.Hash, in.NewPassword); verifyErr == nil {
		return LoginResult{}, apperr.BadRequest("new password must differ from the temporary password")
	}
	encoded, algorithm, err := s.password.Hash(in.NewPassword)
	if err != nil {
		return LoginResult{}, err
	}
	if err := credential.ReplaceHash(encoded, algorithm, now); err != nil {
		return LoginResult{}, err
	}
	if err := s.credentials.Save(ctx, credential); err != nil {
		return LoginResult{}, apperr.Internal("save credential failed", err)
	}

	expected := account.Version
	if account.Status == domain.AccountStatusLocked {
		unlocked, unlockErr := account.UnlockIfExpired(now)
		if unlockErr != nil {
			return LoginResult{}, unlockErr
		}
		if !unlocked {
			return LoginResult{}, authFailure(domain.ErrAccountLocked)
		}
	}
	if err := account.ChangePassword(now); err != nil {
		return LoginResult{}, err
	}
	if err := s.accounts.Save(ctx, account, expected); err != nil {
		return LoginResult{}, apperr.Internal("save account failed", err)
	}
	if _, err := s.sessions.RevokeByAccount(ctx, account.ID, domain.RevokeReasonPasswordChanged, now); err != nil {
		return LoginResult{}, apperr.Internal("revoke account sessions failed", err)
	}
	if err := s.emitAudit(ctx, audit.ActionAuthPasswordChanged, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeSuccess, reasonPasswordResetCompletion, in.TraceID, nil); err != nil {
		return LoginResult{}, err
	}
	return s.challengeOrComplete(ctx, account, now, in.IPHash, in.UAHash, in.TraceID)
}

// generateTemporaryPassword 生成满足复杂度要求的临时口令。
func generateTemporaryPassword() (string, error) {
	runes := make([]byte, 0, temporaryPasswordLength)
	for _, class := range temporaryPasswordClasses {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(class))))
		if err != nil {
			return "", apperr.Internal("generate temporary password failed", err)
		}
		runes = append(runes, class[idx.Int64()])
	}
	for len(runes) < temporaryPasswordLength {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(temporaryPasswordAlphabet))))
		if err != nil {
			return "", apperr.Internal("generate temporary password failed", err)
		}
		runes = append(runes, temporaryPasswordAlphabet[idx.Int64()])
	}
	// Fisher-Yates 洗牌，避免固定位置泄露字符类别。
	for i := len(runes) - 1; i > 0; i-- {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", apperr.Internal("shuffle temporary password failed", err)
		}
		j := idx.Int64()
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

// passwordChangeRateKey 构造强制改密限流键：channel + 票据 jti + IP 摘要。
func passwordChangeRateKey(channel domain.Channel, jti, ipHash string) string {
	if ipHash == "" {
		return string(channel) + ":password_change:" + jti
	}
	return string(channel) + ":password_change:" + jti + ":" + ipHash
}

// passwordTicketStoreKey 把强制改密票据的 jti 放入独立命名空间。
func passwordTicketStoreKey(jti string) string {
	return "pwd:" + jti
}
