package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// activateAccount 把账号从 pending 切到 active 并写回仓储。
func activateAccount(t *testing.T, f *authFixture, account *domain.Account) {
	t.Helper()
	expected := account.Version
	if err := account.Activate(f.clock.Now()); err != nil {
		t.Fatalf("activate account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, expected); err != nil {
		t.Fatalf("save activated account: %v", err)
	}
}

func TestMeReturnsAccountProfile(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	account := f.seedAccount(t, "acct-me", "Alice@Example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	view, err := f.svc.Me(ctx, app.SessionActor{AccountID: "acct-me", TraceID: "trace-me"})
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if view.SubjectID != "acct-me" {
		t.Fatalf("subject_id = %q, want acct-me", view.SubjectID)
	}
	if view.Subject != "alice@example.com" {
		t.Fatalf("subject = %q, want normalized subject", view.Subject)
	}
	if view.Channel != domain.ChannelAdmin {
		t.Fatalf("channel = %q, want admin", view.Channel)
	}
	if view.MerchantID != "" {
		t.Fatalf("merchant_id = %q, want empty", view.MerchantID)
	}
	if view.Status != domain.AccountStatusActive {
		t.Fatalf("status = %q, want active", view.Status)
	}
	if view.MFAEnabled {
		t.Fatal("mfa_enabled = true, want false")
	}
	if view.TokenVersion < 1 {
		t.Fatalf("token_version = %d, want >= 1", view.TokenVersion)
	}
}

func TestMeMFAEnabledOnlyWhenConfirmed(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	account := f.seedAccount(t, "acct-mfa-me", "mfa@example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	saw, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me before binding: %v", err)
	}
	if saw.MFAEnabled {
		t.Fatal("mfa_enabled = true before binding")
	}

	// SetupMFA 只创建未确认绑定，不应让 /me 报告已启用。
	if _, err := f.svc.SetupMFA(ctx, app.MFASetupInput{
		Actor: app.SessionActor{AccountID: account.ID, TraceID: "trace-setup"},
	}); err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	saw, err = f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me after setup: %v", err)
	}
	if saw.MFAEnabled {
		t.Fatal("mfa_enabled = true for unconfirmed enrollment")
	}

	bindMFA(t, f, account.ID)
	saw, err = f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me after binding: %v", err)
	}
	if !saw.MFAEnabled {
		t.Fatal("mfa_enabled = false after confirmed binding")
	}
}

func TestMeRejectsUnavailableAccounts(t *testing.T) {
	ctx := context.Background()

	t.Run("missing account", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		_, err := f.svc.Me(ctx, app.SessionActor{AccountID: "missing"})
		requireAppCode(t, err, 40100)
	})

	t.Run("empty actor", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		_, err := f.svc.Me(ctx, app.SessionActor{})
		requireAppCode(t, err, 40100)
	})

	t.Run("disabled account", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		account := f.seedAccount(t, "acct-disabled", "disabled@example.com", "Correct#Passw0rd")
		activateAccount(t, f, account)
		expected := account.Version
		if err := account.Disable(f.clock.Now()); err != nil {
			t.Fatalf("disable account: %v", err)
		}
		if err := f.accounts.Save(ctx, account, expected); err != nil {
			t.Fatalf("save disabled account: %v", err)
		}
		_, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
		requireAppCode(t, err, 40100)
	})

	t.Run("soft deleted account", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		account := f.seedAccount(t, "acct-deleted", "deleted@example.com", "Correct#Passw0rd")
		activateAccount(t, f, account)
		deletedAt := f.clock.Now()
		account.DeletedAt = &deletedAt
		if err := f.accounts.Save(ctx, account, account.Version); err != nil {
			t.Fatalf("save deleted account: %v", err)
		}
		_, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
		requireAppCode(t, err, 40100)
	})

	t.Run("channel mismatch", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		bizAccount, err := domain.NewAccount(domain.NewAccountParams{
			ID:         "acct-biz",
			Channel:    domain.ChannelBiz,
			Subject:    "biz@example.com",
			MerchantID: "merchant-1",
			Now:        f.clock.Now(),
		})
		if err != nil {
			t.Fatalf("new biz account: %v", err)
		}
		if err := f.accounts.Save(ctx, bizAccount, 0); err != nil {
			t.Fatalf("save biz account: %v", err)
		}
		_, err = f.svc.Me(ctx, app.SessionActor{AccountID: bizAccount.ID})
		requireAppCode(t, err, 40100)
	})
}
