package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// conflictOnceIdentityRepo 第一次 Save 返回冲突，随后回查真实映射。
type conflictOnceIdentityRepo struct {
	inner      domain.OAuthIdentityRepository
	conflicted bool
}

func (r *conflictOnceIdentityRepo) FindByProviderSubject(ctx context.Context, provider, subject string) (*domain.OAuthIdentity, error) {
	if !r.conflicted {
		return nil, apperr.NotFound("oauth identity not found")
	}
	return r.inner.FindByProviderSubject(ctx, provider, subject)
}

func (r *conflictOnceIdentityRepo) Save(ctx context.Context, identity *domain.OAuthIdentity) error {
	if !r.conflicted {
		r.conflicted = true
		return apperr.Conflict("oauth identity already exists")
	}
	return r.inner.Save(ctx, identity)
}

// ListByAccount 委托内层仓储。
func (r *conflictOnceIdentityRepo) ListByAccount(ctx context.Context, accountID string) ([]*domain.OAuthIdentity, error) {
	return r.inner.ListByAccount(ctx, accountID)
}

// Delete 委托内层仓储。
func (r *conflictOnceIdentityRepo) Delete(ctx context.Context, accountID, provider string) error {
	return r.inner.Delete(ctx, accountID, provider)
}

// TestOAuthProvisionConflictReusesExistingAccount 验证映射冲突时复用既有账号且不产生孤儿（REL-07）。
func TestOAuthProvisionConflictReusesExistingAccount(t *testing.T) {
	ctx := context.Background()
	base := newAuthFixture(t, domain.ChannelC)
	provider := &fakeProvider{name: "test", subject: "sub-1"}

	existing, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-existing", Channel: domain.ChannelC, Subject: "oauth-existing@example.com", Now: base.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new existing account: %v", err)
	}
	if err := base.accounts.Save(ctx, existing, 0); err != nil {
		t.Fatalf("save existing account: %v", err)
	}
	inner := persistence.NewMemoryOAuthIdentityRepository()
	mapping, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID: "oauth-existing", AccountID: existing.ID, Provider: "test", ProviderSubject: "sub-1",
	})
	if err != nil {
		t.Fatalf("new mapping: %v", err)
	}
	if err := inner.Save(ctx, mapping); err != nil {
		t.Fatalf("save mapping: %v", err)
	}
	identities := &conflictOnceIdentityRepo{inner: inner}

	states := cache.NewMemoryOAuthStateStore(base.clock)
	svc, err := app.NewOAuthService(app.OAuthServiceConfig{
		Providers:     map[string]app.OAuthProvider{"test": provider},
		States:        states,
		Identities:    identities,
		Accounts:      base.accounts,
		IDs:           base.ids,
		Auth:          base.svc,
		AutoProvision: true,
		StateTTL:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new oauth service: %v", err)
	}

	authorize, err := svc.Authorize(ctx, "test")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	result, err := svc.Callback(ctx, app.OAuthCallbackInput{
		Provider: "test", Code: "code-1", State: authorize.State,
	})
	if err != nil {
		t.Fatalf("callback must reuse existing mapping, got %v", err)
	}
	if result.AccountID != existing.ID {
		t.Fatalf("callback account = %s, want %s", result.AccountID, existing.ID)
	}

	// 本次新建的账号必须被软删，不能留下可登录的孤儿。
	accounts, total, err := base.accounts.List(ctx, domain.AccountListFilter{Channel: domain.ChannelC, Limit: 50})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if total != 2 {
		t.Fatalf("accounts = %d, want 2 (existing + soft-deleted orphan)", total)
	}
	for _, account := range accounts {
		if account.ID == existing.ID {
			if account.DeletedAt != nil {
				t.Fatalf("existing account must stay active: %+v", account)
			}
			continue
		}
		if account.DeletedAt == nil {
			t.Fatalf("conflicting provisioned account must be soft deleted: %+v", account)
		}
	}
}
