// Package app 的外部身份解析：把 OAuth / SAML 主体映射到本地账号。
//
// 两种协议共用同一张 identity_oauth_identity 映射表：
// provider 写入协议前缀（如 "saml:keycloak"），provider_subject 写入断言主体。
package app

import (
	"context"
	"errors"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// externalAccountResolver 把外部身份解析为本地账号；未映射时按 auto_provision 决定是否建号。
type externalAccountResolver struct {
	identities    domain.OAuthIdentityRepository
	accounts      domain.AccountRepository
	ids           IDGenerator
	auth          *AuthService
	hasher        *crypto.Hasher
	autoProvision bool
}

// resolve 返回外部身份对应账号；未映射且未开启自动开通时失败关闭。
func (r externalAccountResolver) resolve(ctx context.Context, provider, subject, traceID string) (*domain.Account, error) {
	identity, err := r.identities.FindByProviderSubject(ctx, provider, subject)
	if err == nil {
		account, loadErr := r.accounts.FindByID(ctx, identity.AccountID)
		if loadErr != nil {
			if errors.Is(loadErr, apperr.ErrNotFound) {
				return nil, oauthInvalid()
			}
			return nil, apperr.Internal("load account failed", loadErr)
		}
		return account, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, apperr.Unavailable("oauth identity store unavailable")
	}
	if !r.autoProvision {
		// D4：失败关闭，不自动建号。
		return nil, oauthInvalid()
	}
	return r.provision(ctx, provider, subject, traceID)
}

// provision 在同一事务内自动建号、写外部身份映射与 account.created 审计。
//
// 审计与账号写入必须原子：避免「账号已建、审计丢失」（IDN-P2-02）。
func (r externalAccountResolver) provision(
	ctx context.Context,
	provider, subject, traceID string,
) (*domain.Account, error) {
	var account *domain.Account
	if err := r.auth.runTx(ctx, func(txCtx context.Context) error {
		created := true
		accountID, err := r.ids.NextString()
		if err != nil {
			return apperr.Internal("generate account id failed", err)
		}
		provisioned, err := domain.NewAccount(domain.NewAccountParams{
			ID:      accountID,
			Channel: domain.ChannelC,
			Subject: OAuthSubjectRef(r.hasher, provider, subject),
		})
		if err != nil {
			return apperr.Internal("build account failed", err)
		}
		if err := r.accounts.Save(txCtx, provisioned, 0); err != nil {
			if errors.Is(err, apperr.ErrConflict) {
				// 并发建号：以已存在的账号为准，避免同一外部身份产生两个账号。
				existing, loadErr := r.accounts.FindBySubject(txCtx, domain.ChannelC, "", provisioned.Subject)
				if loadErr != nil {
					return apperr.Internal("load account failed", loadErr)
				}
				created = false
				account = existing
			} else {
				return apperr.Internal("save account failed", err)
			}
		} else {
			account = provisioned
		}

		identityID, err := r.ids.NextString()
		if err != nil {
			return apperr.Internal("generate oauth identity id failed", err)
		}
		identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
			ID:              identityID,
			AccountID:       account.ID,
			Provider:        provider,
			ProviderSubject: subject,
		})
		if err != nil {
			return apperr.Internal("build oauth identity failed", err)
		}
		if err := r.identities.Save(txCtx, identity); err != nil {
			if !errors.Is(err, apperr.ErrConflict) {
				return apperr.Unavailable("oauth identity store unavailable")
			}
			// REL-07：并发回调可能已写入映射。回查并复用已存在账号，避免 503 与孤儿账号。
			existing, findErr := r.identities.FindByProviderSubject(txCtx, provider, subject)
			if findErr != nil {
				return apperr.Unavailable("oauth identity store unavailable")
			}
			existingAccount, loadErr := r.accounts.FindByID(txCtx, existing.AccountID)
			if loadErr != nil {
				return apperr.Internal("load mapped account failed", loadErr)
			}
			if created && account != nil && account.ID != existingAccount.ID {
				// 本次新建但未建立映射：软删，避免留下可登录的孤儿账号。
				expectedVersion := account.Version
				if deleteErr := account.Delete(r.auth.clock.Now().UTC()); deleteErr != nil {
					return deleteErr
				}
				if saveErr := r.accounts.Save(txCtx, account, expectedVersion); saveErr != nil {
					return apperr.Internal("remove orphan account failed", saveErr)
				}
			}
			account = existingAccount
			return nil
		}
		if created {
			// 只记录 provider 名，不写外部主体原文。
			if err := r.auth.emitAuditAs(txCtx, "system", audit.ActionAuthAccountCreated,
				account.ID, account.MerchantID, "account", account.ID, audit.OutcomeSuccess, "",
				traceID, map[string]string{"provider": provider}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return account, nil
}
