package app_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// newMFATicketService 构造带解析器的 A 端令牌服务；票据校验依赖 parser。
func newMFATicketService(t *testing.T, channel domain.Channel) (*app.TokenService, *jwt.Manager) {
	t.Helper()
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{string(channel)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	svc, err := app.NewTokenService(signer, channel, 15*time.Minute, 168*time.Hour, &seqIDs{}, app.WithTokenParser(signer))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	return svc, signer
}

// TestMFATicketIssueAndParse 验证票据签发声明与解析结果（§7.2）。
func TestMFATicketIssueAndParse(t *testing.T) {
	svc, _ := newMFATicketService(t, domain.ChannelAdmin)
	before := time.Now().UTC()

	ticket, err := svc.IssueMFATicket("acct-1")
	if err != nil {
		t.Fatalf("issue mfa ticket: %v", err)
	}
	if ticket.Ticket == "" || ticket.JTI == "" {
		t.Fatalf("ticket/jti must not be empty: %+v", ticket)
	}
	if got := ticket.ExpiresAt.Sub(before); got < app.MFATicketTTL-2*time.Second || got > app.MFATicketTTL+2*time.Second {
		t.Fatalf("expiry = %s, want about %s", got, app.MFATicketTTL)
	}

	claims, err := svc.ParseMFATicket(ticket.Ticket)
	if err != nil {
		t.Fatalf("parse mfa ticket: %v", err)
	}
	if claims.Subject != "acct-1" || claims.ID != ticket.JTI {
		t.Fatalf("unexpected claims: sub=%q jti=%q", claims.Subject, claims.ID)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "admin" {
		t.Fatalf("audience = %v, want [admin]", claims.Audience)
	}
	if claims.GetString("scope") != "mfa" || claims.GetString("channel") != "admin" {
		t.Fatalf("scope/channel missing: %+v", claims.Extra)
	}
	// 票据不得携带 ver / sid，否则可被当作普通访问令牌使用。
	if _, ok := claims.Extra["ver"]; ok {
		t.Fatal("mfa ticket must not carry ver")
	}
	if _, ok := claims.Extra["sid"]; ok {
		t.Fatal("mfa ticket must not carry sid")
	}
	if _, ok := claims.Extra["roles"]; ok {
		t.Fatal("mfa ticket must not carry roles")
	}

	if _, err := svc.IssueMFATicket("  "); err == nil {
		t.Fatal("empty account id must be rejected")
	}
}

// TestMFATicketChannels 验证 A/B 端可签发、C 端拒绝（BIZ-03）。
func TestMFATicketChannels(t *testing.T) {
	biz, _ := newMFATicketService(t, domain.ChannelBiz)
	if _, err := biz.IssueMFATicket("acct-1"); err != nil {
		t.Fatalf("biz channel must issue mfa tickets: %v", err)
	}
	c, _ := newMFATicketService(t, domain.ChannelC)
	if _, err := c.IssueMFATicket("acct-1"); err == nil {
		t.Fatal("c channel must not issue mfa tickets")
	}
}

// TestMFATicketRejectsInvalidTokens 表驱动覆盖票据校验的拒绝分支。
func TestMFATicketRejectsInvalidTokens(t *testing.T) {
	svc, signer := newMFATicketService(t, domain.ChannelAdmin)
	now := time.Now().UTC()

	claims := func(extra map[string]any) jwt.Claims {
		return jwt.Claims{
			Subject:   "acct-1",
			Audience:  []string{"admin"},
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Minute),
			ID:        "jti-1",
			Extra:     extra,
		}
	}
	sign := func(c jwt.Claims) string {
		t.Helper()
		raw, err := signer.Sign(c)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return raw
	}

	cases := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "malformed", token: "not-a-jwt"},
		{name: "wrong scope", token: sign(claims(map[string]any{"scope": "access", "channel": "admin"}))},
		{name: "missing scope", token: sign(claims(map[string]any{"channel": "admin"}))},
		{name: "wrong channel", token: sign(claims(map[string]any{"scope": "mfa", "channel": "biz"}))},
		{name: "wrong audience", token: sign(jwt.Claims{
			Subject:   "acct-1",
			Audience:  []string{"biz"},
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Minute),
			ID:        "jti-1",
			Extra:     map[string]any{"scope": "mfa", "channel": "admin"},
		})},
		{name: "expired", token: sign(jwt.Claims{
			Subject:   "acct-1",
			Audience:  []string{"admin"},
			IssuedAt:  now.Add(-10 * time.Minute),
			ExpiresAt: now.Add(-5 * time.Minute),
			ID:        "jti-1",
			Extra:     map[string]any{"scope": "mfa", "channel": "admin"},
		})},
		{name: "missing subject", token: sign(jwt.Claims{
			Audience:  []string{"admin"},
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Minute),
			ID:        "jti-1",
			Extra:     map[string]any{"scope": "mfa", "channel": "admin"},
		})},
		{name: "missing jti", token: sign(jwt.Claims{
			Subject:   "acct-1",
			Audience:  []string{"admin"},
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Minute),
			Extra:     map[string]any{"scope": "mfa", "channel": "admin"},
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.ParseMFATicket(tc.token); err == nil {
				t.Fatal("expected rejection")
			} else {
				requireAppCode(t, err, apperr.CodeUnauthorized)
			}
		})
	}
}

// numericClaim 把 JSON 反序列化出的数字统一转成 int64，便于断言。
func numericClaim(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return -1
	}
}

// newTokenService 构造 HS256 签发服务；seqIDs 由 login_guard_test.go 提供。
func newTokenService(t *testing.T, channel domain.Channel) (*app.TokenService, *jwt.Manager) {
	t.Helper()
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{string(channel)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	svc, err := app.NewTokenService(signer, channel, 15*time.Minute, 168*time.Hour, &seqIDs{})
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	return svc, signer
}

func TestTokenServiceIssueAdminClaims(t *testing.T) {
	svc, verifier := newTokenService(t, domain.ChannelAdmin)
	account := &domain.Account{ID: "acct-1", Channel: domain.ChannelAdmin, TokenVersion: 3}

	pair, err := svc.Issue(account, "sess-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if pair.ExpiresIn != int64((15*time.Minute)/time.Second) {
		t.Fatalf("expires_in = %d, want 900", pair.ExpiresIn)
	}
	if !strings.HasPrefix(pair.RefreshToken, app.RefreshPrefixAdmin) {
		t.Fatalf("refresh token must use admin prefix: %q", pair.RefreshToken)
	}
	if pair.RefreshHash == "" || strings.Contains(pair.RefreshHash, pair.RefreshToken) {
		t.Fatal("refresh hash must be a digest, not the raw token")
	}

	claims, err := verifier.Parse(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.Subject != "acct-1" || claims.ID == "" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "admin" {
		t.Fatalf("audience = %v, want [admin]", claims.Audience)
	}
	if claims.GetString("sid") != "sess-1" || claims.GetString("channel") != "admin" {
		t.Fatalf("sid / channel missing: %+v", claims.Extra)
	}
	if got := numericClaim(claims.Extra["ver"]); got != 3 {
		t.Fatalf("ver = %v, want 3", claims.Extra["ver"])
	}
	if _, ok := claims.Extra["roles"]; ok {
		t.Fatal("access token must not carry roles (T10)")
	}
	if claims.Extra["merchant_id"] != nil {
		t.Fatal("admin token must not carry merchant_id")
	}
}

func TestTokenServiceIssueBizIncludesMerchant(t *testing.T) {
	svc, verifier := newTokenService(t, domain.ChannelBiz)
	account := &domain.Account{
		ID:           "acct-2",
		Channel:      domain.ChannelBiz,
		TokenVersion: 1,
		MerchantID:   "m-1",
	}

	pair, err := svc.Issue(account, "sess-2")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(pair.RefreshToken, app.RefreshPrefixBiz) {
		t.Fatalf("refresh token must use biz prefix: %q", pair.RefreshToken)
	}
	claims, err := verifier.Parse(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.GetString("merchant_id") != "m-1" {
		t.Fatalf("merchant_id missing: %+v", claims.Extra)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "biz" {
		t.Fatalf("audience = %v, want [biz]", claims.Audience)
	}
}

func TestTokenServiceRejectsInvalidTTL(t *testing.T) {
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	if _, err := app.NewTokenService(signer, domain.ChannelAdmin, 15*time.Minute, 15*time.Minute, &seqIDs{}); !errors.Is(err, app.ErrInvalidTokenTTL) {
		t.Fatalf("refresh_ttl == access_ttl error = %v, want ErrInvalidTokenTTL", err)
	}
	if _, err := app.NewTokenService(signer, domain.ChannelAdmin, 0, time.Hour, &seqIDs{}); !errors.Is(err, app.ErrInvalidTokenTTL) {
		t.Fatalf("zero access_ttl error = %v, want ErrInvalidTokenTTL", err)
	}
}

func TestRefreshTokenPrefixAndHash(t *testing.T) {
	raw, err := app.GenerateRefreshToken(domain.ChannelAdmin)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !app.RefreshPrefixMatches(domain.ChannelAdmin, raw) {
		t.Fatal("admin prefix must match")
	}
	if app.RefreshPrefixMatches(domain.ChannelBiz, raw) {
		t.Fatal("biz prefix must not match admin token")
	}

	hash, err := app.HashRefreshToken(raw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	again, err := app.HashRefreshToken(raw)
	if err != nil {
		t.Fatalf("hash again: %v", err)
	}
	if hash != again || hash == raw {
		t.Fatal("hash must be deterministic and not equal to raw token")
	}

	if _, err := app.HashRefreshToken("ar_short"); !errors.Is(err, app.ErrInvalidRefreshToken) {
		t.Fatalf("malformed token error = %v, want ErrInvalidRefreshToken", err)
	}
	if _, err := app.HashRefreshToken("xx_" + raw[3:]); !errors.Is(err, app.ErrInvalidRefreshToken) {
		t.Fatalf("unknown prefix error = %v, want ErrInvalidRefreshToken", err)
	}
}
