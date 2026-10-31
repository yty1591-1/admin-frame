package app

import (
	"context"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// SMSRebindInput 是 C 端手机号换绑入参。
type SMSRebindInput struct {
	// Actor 当前登录的消费者。
	Actor SessionActor
	// Phone 新手机号；必须为 E.164。
	Phone string
	// Code 新手机号的验证码。
	Code string
	// OldCode 当前手机号的验证码；换绑属于账号接管高风险操作，必须同时证明持有旧号。
	OldCode string
	// IPHash 客户端 IP 摘要；用于限流。
	IPHash string
}

// Rebind 校验新旧手机号验证码后完成换绑。
//
// 新号验证码证明对新号的控制，旧号验证码证明对当前账号的控制；
// 仅凭 Bearer 无法换绑，避免令牌泄露后直接被接管。
func (s *SMSLoginService) Rebind(ctx context.Context, in SMSRebindInput) error {
	phoneHash, err := s.verifyInput(SMSLoginInput{
		Phone:   in.Phone,
		Code:    in.Code,
		IPHash:  in.IPHash,
		TraceID: in.Actor.TraceID,
	})
	if err != nil {
		return err
	}
	if err := s.consumeCode(ctx, phoneHash, strings.TrimSpace(in.Code), in.Actor.TraceID); err != nil {
		return err
	}
	if err := s.verifyCurrentPhone(ctx, in.Actor, in.OldCode); err != nil {
		return err
	}
	return s.auth.RebindConsumerPhone(ctx, in.Actor, phoneHash)
}

// DeleteAccount 在验证当前手机号验证码后注销消费者账号。
//
// 已有手机号绑定的账号必须提供当前手机号验证码；OAuth-only 账号无手机号时
// 仅能凭 Bearer 注销（后续可扩展 OAuth 再认证）。
func (s *SMSLoginService) DeleteAccount(ctx context.Context, actor SessionActor, code string) error {
	if err := s.verifyCurrentPhone(ctx, actor, code); err != nil {
		return err
	}
	return s.auth.DeleteConsumerAccount(ctx, actor)
}

// verifyCurrentPhone 校验当前账号手机号的验证码。
//
// 旧手机号摘要来自账号主体（phone:<hmac>），不要求客户端回传旧号明文。
func (s *SMSLoginService) verifyCurrentPhone(ctx context.Context, actor SessionActor, code string) error {
	if strings.TrimSpace(actor.AccountID) == "" {
		return invalidToken()
	}
	account, err := s.accounts.FindByID(ctx, actor.AccountID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return invalidToken()
		}
		return apperr.Internal("load account failed", err)
	}
	if account.Channel != domain.ChannelC {
		return invalidToken()
	}
	subject := strings.TrimSpace(account.Subject)
	if !strings.HasPrefix(subject, "phone:") {
		// OAuth-only 账号没有手机号可校验；由调用方决定是否允许。
		return apperr.Conflict("phone is not bound")
	}
	phoneHash := strings.TrimPrefix(subject, "phone:")
	if strings.TrimSpace(phoneHash) == "" {
		return invalidToken()
	}
	normalized := strings.TrimSpace(code)
	if !smsCodePattern.MatchString(normalized) {
		return apperr.BadRequest("phone or code format is invalid")
	}
	if err := s.guard.CheckRateLimit(loginRateKey(domain.ChannelC, phoneHash, "")); err != nil {
		return err
	}
	return s.consumeCode(ctx, phoneHash, normalized, actor.TraceID)
}

// RebindConsumerPhone 把 C 端账号的登录主体与手机号凭据切换到新手机号。
//
// 换绑属于账号接管高风险操作：成功后递增 token_version 并撤销全部会话。
func (s *AuthService) RebindConsumerPhone(ctx context.Context, actor SessionActor, phoneHash string) error {
	if s.channel != domain.ChannelC {
		return apperr.BadRequest("phone rebind is only supported on channel=c")
	}
	if strings.TrimSpace(actor.AccountID) == "" || strings.TrimSpace(phoneHash) == "" {
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
		if loaded.Channel != domain.ChannelC || loaded.DeletedAt != nil ||
			loaded.Status == domain.AccountStatusDisabled {
			return invalidToken()
		}
		credential, err := s.credentials.FindByAccount(txCtx, loaded.ID, domain.CredentialTypePhone)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				// OAuth-only 账号尚无手机号绑定：先绑定再换绑，不在本端点隐式建号。
				return apperr.Conflict("phone is not bound")
			}
			return apperr.Internal("load credential failed", err)
		}
		if credential.Hash == phoneHash {
			return apperr.BadRequest("new phone must differ from the current phone")
		}
		expected := loaded.Version
		if err := loaded.RebindSubject(phoneSubject(phoneHash), now); err != nil {
			return err
		}
		if err := loaded.BumpTokenVersion(now); err != nil {
			return err
		}
		if err := s.accounts.RebindSubject(txCtx, loaded, expected); err != nil {
			if errors.Is(err, apperr.ErrConflict) {
				return apperr.Conflict("phone is already bound")
			}
			return apperr.Internal("rebind account subject failed", err)
		}
		if err := credential.ReplaceHash(phoneHash, "sha256", now); err != nil {
			return err
		}
		if err := s.credentials.Save(txCtx, credential); err != nil {
			return apperr.Internal("save phone credential failed", err)
		}
		if _, err := s.sessions.RevokeByAccount(txCtx, loaded.ID, domain.RevokeReasonPhoneChanged, now); err != nil {
			return apperr.Internal("revoke account sessions failed", err)
		}
		if err := s.emitAudit(txCtx, audit.ActionAuthPhoneRebound, loaded.ID, "", "account", loaded.ID,
			audit.OutcomeSuccess, string(domain.RevokeReasonPhoneChanged), actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return nil
}

// DeleteConsumerAccount 用户自助注销账号（软删）：撤销全部会话并记录审计。
func (s *AuthService) DeleteConsumerAccount(ctx context.Context, actor SessionActor) error {
	if s.channel != domain.ChannelC {
		return apperr.BadRequest("account deletion is only supported on channel=c")
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
		if loaded.Channel != domain.ChannelC {
			return invalidToken()
		}
		if loaded.DeletedAt != nil {
			account = loaded
			return nil
		}
		expected := loaded.Version
		if err := loaded.Delete(now); err != nil {
			return err
		}
		if err := s.accounts.Save(txCtx, loaded, expected); err != nil {
			return apperr.Internal("save account failed", err)
		}
		if _, err := s.sessions.RevokeByAccount(txCtx, loaded.ID, domain.RevokeReasonAccountDisabled, now); err != nil {
			return apperr.Internal("revoke account sessions failed", err)
		}
		if err := s.emitAudit(txCtx, audit.ActionAuthAccountDeleted, loaded.ID, "", "account", loaded.ID,
			audit.OutcomeSuccess, "consumer_deleted", actor.TraceID, nil); err != nil {
			return err
		}
		account = loaded
		return nil
	}); err != nil {
		return err
	}
	s.invalidateAccountState(ctx, account.ID, accountStateOf(account))
	return s.revokeCurrentToken(ctx, actor, now)
}
