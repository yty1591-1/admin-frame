package app_test

import (
	"context"
	"testing"
	"time"

	identityapp "github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// fixedRevokerClock 是撤销器的固定时钟。
type fixedRevokerClock struct{ now time.Time }

func (c fixedRevokerClock) Now() time.Time { return c.now }

// newBizAccount 创建并激活一个 B 端账号。
func newBizAccount(ctx context.Context, t *testing.T, accounts domain.AccountRepository,
	id, merchantID string, now time.Time) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: id, Channel: domain.ChannelBiz, Subject: id + "@biz.example.com",
		MerchantID: merchantID, Now: now,
	})
	if err != nil {
		t.Fatalf("NewAccount(%s): %v", id, err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("Save(%s): %v", id, err)
	}
	if err := account.Activate(now); err != nil {
		t.Fatalf("Activate(%s): %v", id, err)
	}
	if err := accounts.Save(ctx, account, account.Version-1); err != nil {
		t.Fatalf("Save(%s) active: %v", id, err)
	}
	return account
}

// newSession 为账号创建一个 active 会话。
func newSession(ctx context.Context, t *testing.T, sessions domain.SessionRepository,
	id, accountID string, now time.Time) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionParams{
		ID: id, AccountID: accountID, FamilyID: "family-" + id,
		RefreshHash: "hash-" + id, TokenVersion: 1,
		ExpiresAt: now.Add(24 * time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("NewSession(%s): %v", id, err)
	}
	if err := sessions.Save(ctx, session, 0); err != nil {
		t.Fatalf("Save session %s: %v", id, err)
	}
}

// TestTenantSessionRevoker 验证按商户撤销：活跃账号撤会话 + 升版本，
// 停用账号只撤会话不动版本，其他商户不受影响。
func TestTenantSessionRevoker(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	accounts := persistence.NewMemoryAccountRepository()
	sessions := persistence.NewMemorySessionRepository()

	active := newBizAccount(ctx, t, accounts, "acct-active", "tenant-1", now)
	disabled := newBizAccount(ctx, t, accounts, "acct-disabled", "tenant-1", now)
	if err := disabled.Disable(now); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := accounts.Save(ctx, disabled, disabled.Version-1); err != nil {
		t.Fatalf("Save disabled: %v", err)
	}
	newBizAccount(ctx, t, accounts, "acct-other", "tenant-2", now)

	newSession(ctx, t, sessions, "sess-a1", "acct-active", now)
	newSession(ctx, t, sessions, "sess-a2", "acct-active", now)
	newSession(ctx, t, sessions, "sess-d1", "acct-disabled", now)
	newSession(ctx, t, sessions, "sess-o1", "acct-other", now)

	revoker, err := identityapp.NewTenantSessionRevoker(accounts, sessions, fixedRevokerClock{now: now})
	if err != nil {
		t.Fatalf("NewTenantSessionRevoker: %v", err)
	}

	revoked, err := revoker.RevokeByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("RevokeByTenant: %v", err)
	}
	if revoked != 3 {
		t.Fatalf("撤销会话数 = %d, want 3（不含其他商户）", revoked)
	}

	// 活跃账号 token_version 已递增 → 旧访问令牌立即失效。
	reloaded, err := accounts.FindByID(ctx, "acct-active")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if reloaded.TokenVersion != active.TokenVersion+1 {
		t.Fatalf("token_version = %d, want %d", reloaded.TokenVersion, active.TokenVersion+1)
	}
	// 停用账号不递增版本（Disable 自身已升过版本；撤销器只对活跃账号升版）。
	reloadedDisabled, err := accounts.FindByID(ctx, "acct-disabled")
	if err != nil {
		t.Fatalf("FindByID(disabled): %v", err)
	}
	if reloadedDisabled.TokenVersion != disabled.TokenVersion {
		t.Fatalf("停用账号 token_version = %d, want %d（撤销器不应再动）",
			reloadedDisabled.TokenVersion, disabled.TokenVersion)
	}
	// 其他商户会话不受影响。
	others, err := sessions.ListByAccount(ctx, "acct-other")
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(others) != 1 || others[0].Status != domain.SessionStatusActive {
		t.Fatalf("其他商户会话不应被撤销: %+v", others)
	}

	// 未知商户：成功返回 0（幂等，不存在也是一种终态）。
	revoked, err = revoker.RevokeByTenant(ctx, "tenant-unknown")
	if err != nil || revoked != 0 {
		t.Fatalf("未知商户 RevokeByTenant = (%d, %v), want (0, nil)", revoked, err)
	}

	// 空商户 ID：400。
	_, err = revoker.RevokeByTenant(ctx, "  ")
	requireAppCode(t, err, apperr.CodeBadRequest)
}
