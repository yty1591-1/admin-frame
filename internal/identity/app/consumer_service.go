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
	// IPHash 客户端 IP 摘要；用于限流。
	IPHash string
}

// Rebind 校验新手机号验证码后完成换绑。
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
	return s.auth.RebindConsumerPhone(ctx, in.Actor, phoneHash)
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
