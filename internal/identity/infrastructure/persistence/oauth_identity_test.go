package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mustOAuthIdentity 构造合法的外部身份映射。
func mustOAuthIdentity(t *testing.T, id, accountID, provider, subject string) *domain.OAuthIdentity {
	t.Helper()
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              id,
		AccountID:       accountID,
		Provider:        provider,
		ProviderSubject: subject,
		Now:             repoBaseTime,
	})
	if err != nil {
		t.Fatalf("NewOAuthIdentity() error = %v", err)
	}
	return identity
}

func TestNewOAuthIdentityValidate(t *testing.T) {
	cases := []struct {
		name string
		in   domain.NewOAuthIdentityParams
		want error
	}{
		{name: "missing id", in: domain.NewOAuthIdentityParams{AccountID: "1001", Provider: "github", ProviderSubject: "s1"}, want: domain.ErrEmptyInput},
		{name: "missing account", in: domain.NewOAuthIdentityParams{ID: "2001", Provider: "github", ProviderSubject: "s1"}, want: domain.ErrEmptyInput},
		{name: "missing provider", in: domain.NewOAuthIdentityParams{ID: "2001", AccountID: "1001", Provider: " ", ProviderSubject: "s1"}, want: domain.ErrInvalidOAuthIdentity},
		{name: "missing subject", in: domain.NewOAuthIdentityParams{ID: "2001", AccountID: "1001", Provider: "github"}, want: domain.ErrInvalidOAuthIdentity},
		{name: "provider too long", in: domain.NewOAuthIdentityParams{ID: "2001", AccountID: "1001", Provider: strings.Repeat("x", domain.MaxOAuthProviderLen+1), ProviderSubject: "s1"}, want: domain.ErrInvalidOAuthIdentity},
		{name: "subject too long", in: domain.NewOAuthIdentityParams{ID: "2001", AccountID: "1001", Provider: "github", ProviderSubject: string(make([]byte, 256))}, want: domain.ErrInvalidOAuthIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewOAuthIdentity(tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	identity := mustOAuthIdentity(t, "2001", "1001", " github ", " sub-1 ")
	if identity.Provider != "github" || identity.ProviderSubject != "sub-1" {
		t.Fatalf("identity = %+v, want normalized provider/subject", identity)
	}
	if !identity.CreatedAt.Equal(repoBaseTime) || !identity.UpdatedAt.Equal(repoBaseTime) {
		t.Fatalf("timestamps = %v/%v, want %v", identity.CreatedAt, identity.UpdatedAt, repoBaseTime)
	}
}

func TestMemoryOAuthIdentityRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryOAuthIdentityRepository()
	identity := mustOAuthIdentity(t, "2001", "1001", "github", "sub-1")

	if err := repo.Save(ctx, identity); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := repo.Save(ctx, mustOAuthIdentity(t, "2002", "1002", "github", "sub-1")); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate subject) error = %v, want conflict", err)
	}
	if err := repo.Save(ctx, mustOAuthIdentity(t, "2001", "1003", "github", "sub-2")); codeOf(err) != apperr.CodeConflict {
		t.Fatalf("Save(duplicate id) error = %v, want conflict", err)
	}

	found, err := repo.FindByProviderSubject(ctx, "github", "sub-1")
	if err != nil {
		t.Fatalf("FindByProviderSubject() error = %v", err)
	}
	if found.AccountID != "1001" {
		t.Fatalf("account = %s, want 1001", found.AccountID)
	}
	// 返回副本：修改结果不影响仓储内部状态。
	found.AccountID = "9999"
	again, err := repo.FindByProviderSubject(ctx, "github", "sub-1")
	if err != nil {
		t.Fatalf("FindByProviderSubject() error = %v", err)
	}
	if again.AccountID != "1001" {
		t.Fatalf("account = %s, want 1001 (copy isolation)", again.AccountID)
	}
	if _, err := repo.FindByProviderSubject(ctx, "github", "missing"); codeOf(err) != apperr.CodeNotFound {
		t.Fatalf("FindByProviderSubject(missing) error = %v, want not found", err)
	}
}

// TestSQLOAuthIdentityRepositoryNotConfigured 验证未注入 *sql.DB 时失败关闭。
func TestSQLOAuthIdentityRepositoryNotConfigured(t *testing.T) {
	repo := NewSQLOAuthIdentityRepository(nil)
	if err := repo.Save(context.Background(), mustOAuthIdentity(t, "2001", "1001", "github", "sub-1")); codeOf(err) != apperr.CodeServiceUnavailable {
		t.Fatalf("Save() error = %v, want unavailable", err)
	}
}
