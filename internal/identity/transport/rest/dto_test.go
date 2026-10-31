package rest

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// TestDTOConversions 覆盖响应 DTO 转换与零值省略（MNT-03 handler 覆盖）。
func TestDTOConversions(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	locked := now.Add(time.Hour)
	account := accountDTOFrom(app.AccountSummary{
		ID: "acct-1", Subject: "alice", Channel: domain.ChannelAdmin,
		Status: domain.AccountStatusActive, TokenVersion: 2,
		LockedUntil: &locked, CreatedAt: now, UpdatedAt: now,
	})
	if account.Channel != "admin" || account.Status != "active" || account.TokenVersion != 2 {
		t.Fatalf("account dto = %+v", account)
	}
	me := meResponseFrom(app.MeView{
		SubjectID: "acct-1", Subject: "alice", DisplayName: "Alice",
		Channel: domain.ChannelAdmin, Status: domain.AccountStatusActive,
		PhoneBound: true, HasPassword: true, TokenVersion: 1,
	})
	if me.SubjectID != "acct-1" || me.Channel != "admin" || !me.PhoneBound {
		t.Fatalf("me dto = %+v", me)
	}
	absolute := now.Add(24 * time.Hour)
	token := tokenResponseFrom(app.TokenPair{AccessToken: "a", RefreshToken: "r", ExpiresIn: 900,
		AbsoluteExpiresAt: absolute})
	if token.TokenType != "Bearer" || token.AbsoluteExpiresAt == nil {
		t.Fatalf("token dto = %+v", token)
	}
	zero := tokenResponseFrom(app.TokenPair{ExpiresIn: 900})
	if zero.AbsoluteExpiresAt != nil {
		t.Fatalf("zero absolute expiry must be nil, got %v", zero.AbsoluteExpiresAt)
	}
	sessions := sessionListResponseFrom(app.SessionPage{
		Items: []app.SessionView{{
			ID: "s-1", Current: true, Status: "active", CreatedAt: now,
			LastUsedAt: now, ExpiresAt: now, AbsoluteExpiresAt: now,
		}},
		NextCursor: "cursor",
	})
	if len(sessions.Items) != 1 || sessions.NextCursor != "cursor" {
		t.Fatalf("session dto = %+v", sessions)
	}
}

// TestWriteLoginResultBranches 覆盖登录中间态与令牌响应分支。
func TestWriteLoginResultBranches(t *testing.T) {
	cases := []struct {
		name   string
		result app.LoginResult
	}{
		{"password_change", app.LoginResult{PasswordChangeRequired: true, PasswordChangeTicket: "t"}},
		{"mfa_binding", app.LoginResult{MFABindingRequired: true, MFASetupTicket: "t"}},
		{"mfa", app.LoginResult{MFARequired: true, MFATicket: "t"}},
		{"token", app.LoginResult{TokenPair: app.TokenPair{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeLoginResult(recorder, "trace-1", tc.result)
			if recorder.Code != 200 || recorder.Body.Len() == 0 {
				t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
			}
		})
	}
}
