package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
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

func TestMeReportsCredentialAndPasswordState(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	account := f.seedAccount(t, "acct-creds-me", "creds@example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	view, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if !view.HasPassword {
		t.Fatal("has_password = false, want true for seeded password credential")
	}
	if view.PhoneBound {
		t.Fatal("phone_bound = true, want false before binding phone")
	}
	if view.MustChangePassword {
		t.Fatal("must_change_password = true, want false")
	}
	if view.PasswordUpdatedAt != nil {
		t.Fatalf("password_updated_at = %v, want nil before first password change", view.PasswordUpdatedAt)
	}

	// 绑定手机凭据后 phone_bound 必须为 true；号码本身永不回显。
	phone, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        "cred-phone-" + account.ID,
		AccountID: account.ID,
		Type:      domain.CredentialTypePhone,
		Hash:      "hash$phone",
		Algorithm: "hmac-sha256",
		Now:       f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new phone credential: %v", err)
	}
	if err := f.credentials.Save(ctx, phone); err != nil {
		t.Fatalf("save phone credential: %v", err)
	}

	// 改密时间与管理员的强制改密标记必须原样透出。
	changedAt := f.clock.Now().Add(-time.Hour)
	account.PasswordUpdatedAt = &changedAt
	account.MustChangePassword = true
	if err := f.accounts.Save(ctx, account, account.Version); err != nil {
		t.Fatalf("save account: %v", err)
	}

	view, err = f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me after updates: %v", err)
	}
	if !view.PhoneBound {
		t.Fatal("phone_bound = false, want true after binding phone")
	}
	if !view.MustChangePassword {
		t.Fatal("must_change_password = false, want true")
	}
	if view.PasswordUpdatedAt == nil || !view.PasswordUpdatedAt.Equal(changedAt) {
		t.Fatalf("password_updated_at = %v, want %v", view.PasswordUpdatedAt, changedAt)
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

func TestUpdateProfileRenamesDisplayName(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	account := f.seedAccount(t, "acct-rename", "rename@example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	// seedAccount 会写入密码凭据，这里先确认一下 token_version 基线。
	before, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me before rename: %v", err)
	}
	if before.DisplayName != "" {
		t.Fatalf("display_name = %q, want empty before rename", before.DisplayName)
	}

	view, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID, TraceID: "trace-rename"},
		DisplayName:  "  张三  ",
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if view.DisplayName != "张三" {
		t.Fatalf("display_name = %q, want trimmed %q", view.DisplayName, "张三")
	}
	if view.TokenVersion != before.TokenVersion {
		t.Fatalf("token_version = %d, want unchanged %d: renaming must not revoke sessions",
			view.TokenVersion, before.TokenVersion)
	}

	// 必须落库：重新读取 /me 仍能看到昵称，而不是只改了返回值。
	reread, err := f.svc.Me(ctx, app.SessionActor{AccountID: account.ID})
	if err != nil {
		t.Fatalf("me after rename: %v", err)
	}
	if reread.DisplayName != "张三" {
		t.Fatalf("persisted display_name = %q, want %q", reread.DisplayName, "张三")
	}

	events := capture.byAction(audit.ActionAuthProfileUpdated)
	if len(events) != 1 {
		t.Fatalf("expected one auth.profile.updated, got %d", len(events))
	}
	if events[0].Attrs["changed_fields"] != "display_name" {
		t.Fatalf("changed_fields = %q, want display_name", events[0].Attrs["changed_fields"])
	}
	// 昵称属个人信息，审计只记字段名，不得记录原文。
	if _, ok := events[0].Attrs["display_name"]; ok {
		t.Fatal("audit must not carry the display name value")
	}
	for _, value := range events[0].Attrs {
		if strings.Contains(value, "张三") {
			t.Fatalf("audit attrs leak display name: %v", events[0].Attrs)
		}
	}
}

func TestUpdateProfileClearsDisplayName(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	account := f.seedAccount(t, "acct-clear", "clear@example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	if _, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID},
		DisplayName:  "李四",
	}); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	view, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID},
		DisplayName:  "   ",
	})
	if err != nil {
		t.Fatalf("clear profile: %v", err)
	}
	if view.DisplayName != "" {
		t.Fatalf("display_name = %q, want empty after clear", view.DisplayName)
	}
}

func TestUpdateProfileRejectsInvalidDisplayName(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	account := f.seedAccount(t, "acct-invalid-name", "invalid-name@example.com", "Correct#Passw0rd")
	activateAccount(t, f, account)

	overlong := strings.Repeat("长", domain.MaxDisplayNameLen+1)
	_, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID},
		DisplayName:  overlong,
	})
	requireAppCode(t, err, 40000)

	_, err = f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID},
		DisplayName:  "张\n三",
	})
	requireAppCode(t, err, 40000)

	// 边界值仍应通过：恰好 MaxDisplayNameLen 个 rune。
	exact := strings.Repeat("长", domain.MaxDisplayNameLen)
	view, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
		SessionActor: app.SessionActor{AccountID: account.ID},
		DisplayName:  exact,
	})
	if err != nil {
		t.Fatalf("update profile with max length: %v", err)
	}
	if view.DisplayName != exact {
		t.Fatalf("display_name length = %d runes, want %d",
			len([]rune(view.DisplayName)), domain.MaxDisplayNameLen)
	}
}

func TestUpdateProfileRejectsUnavailableAccounts(t *testing.T) {
	ctx := context.Background()

	t.Run("empty actor", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		_, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{DisplayName: "张三"})
		requireAppCode(t, err, 40100)
	})

	t.Run("missing account", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		_, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
			SessionActor: app.SessionActor{AccountID: "missing"},
			DisplayName:  "张三",
		})
		requireAppCode(t, err, 40100)
	})

	t.Run("disabled account", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		account := f.seedAccount(t, "acct-rename-disabled", "rename-disabled@example.com", "Correct#Passw0rd")
		activateAccount(t, f, account)
		expected := account.Version
		if err := account.Disable(f.clock.Now()); err != nil {
			t.Fatalf("disable account: %v", err)
		}
		if err := f.accounts.Save(ctx, account, expected); err != nil {
			t.Fatalf("save disabled account: %v", err)
		}
		_, err := f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
			SessionActor: app.SessionActor{AccountID: account.ID},
			DisplayName:  "张三",
		})
		requireAppCode(t, err, 40100)
	})

	t.Run("channel mismatch", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		bizAccount, err := domain.NewAccount(domain.NewAccountParams{
			ID:         "acct-rename-biz",
			Channel:    domain.ChannelBiz,
			Subject:    "rename-biz@example.com",
			MerchantID: "merchant-1",
			Now:        f.clock.Now(),
		})
		if err != nil {
			t.Fatalf("new biz account: %v", err)
		}
		if err := f.accounts.Save(ctx, bizAccount, 0); err != nil {
			t.Fatalf("save biz account: %v", err)
		}
		_, err = f.svc.UpdateProfile(ctx, app.UpdateProfileInput{
			SessionActor: app.SessionActor{AccountID: bizAccount.ID},
			DisplayName:  "张三",
		})
		requireAppCode(t, err, 40100)
	})
}
