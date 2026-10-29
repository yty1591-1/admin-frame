package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewOAuthIdentityValidation(t *testing.T) {
	base := func() NewOAuthIdentityParams {
		return NewOAuthIdentityParams{
			ID:              "5001",
			AccountID:       "1001",
			Provider:        "github",
			ProviderSubject: "12345",
			Now:             baseTime,
		}
	}
	tests := []struct {
		name    string
		mutate  func(*NewOAuthIdentityParams)
		wantErr error
	}{
		{name: "valid", mutate: func(*NewOAuthIdentityParams) {}},
		{name: "missing id", mutate: func(p *NewOAuthIdentityParams) { p.ID = " " }, wantErr: ErrEmptyInput},
		{name: "missing account", mutate: func(p *NewOAuthIdentityParams) { p.AccountID = "" }, wantErr: ErrEmptyInput},
		{
			name:    "account id too long",
			mutate:  func(p *NewOAuthIdentityParams) { p.AccountID = strings.Repeat("x", MaxAccountIDLen+1) },
			wantErr: ErrEmptyInput,
		},
		{
			name:    "missing provider",
			mutate:  func(p *NewOAuthIdentityParams) { p.Provider = "  " },
			wantErr: ErrInvalidOAuthIdentity,
		},
		{
			name:    "provider too long",
			mutate:  func(p *NewOAuthIdentityParams) { p.Provider = strings.Repeat("x", MaxOAuthProviderLen+1) },
			wantErr: ErrInvalidOAuthIdentity,
		},
		{
			name:    "missing provider subject",
			mutate:  func(p *NewOAuthIdentityParams) { p.ProviderSubject = "" },
			wantErr: ErrInvalidOAuthIdentity,
		},
		{
			name:    "provider subject too long",
			mutate:  func(p *NewOAuthIdentityParams) { p.ProviderSubject = strings.Repeat("x", MaxOAuthProviderSubjectLen+1) },
			wantErr: ErrInvalidOAuthIdentity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := base()
			tt.mutate(&params)
			_, err := NewOAuthIdentity(params)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewOAuthIdentity() error = %v, want sentinel %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewOAuthIdentity() error = %v", err)
			}
		})
	}
}

func TestNewOAuthIdentityTrimsFields(t *testing.T) {
	identity, err := NewOAuthIdentity(NewOAuthIdentityParams{
		ID:              "5001",
		AccountID:       "1001",
		Provider:        "  github ",
		ProviderSubject: " 12345 ",
		Now:             baseTime,
	})
	if err != nil {
		t.Fatalf("NewOAuthIdentity() error = %v", err)
	}
	if identity.Provider != "github" {
		t.Fatalf("Provider = %q, want github", identity.Provider)
	}
	if identity.ProviderSubject != "12345" {
		t.Fatalf("ProviderSubject = %q, want 12345", identity.ProviderSubject)
	}
	if !identity.CreatedAt.Equal(baseTime) || !identity.UpdatedAt.Equal(baseTime) {
		t.Fatalf("timestamps = %v / %v, want %v", identity.CreatedAt, identity.UpdatedAt, baseTime)
	}
}
