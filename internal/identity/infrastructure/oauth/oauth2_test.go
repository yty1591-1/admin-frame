package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// requireCode 断言错误携带指定业务数字码。
func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %d, got nil", want)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not *apperr.AppError", err)
	}
	if appErr.Code != want {
		t.Fatalf("error code = %d, want %d (%v)", appErr.Code, want, err)
	}
}

// testProvider 构造指向 httptest 服务器的 provider。
func testProvider(t *testing.T, server *httptest.Server, subjectField string) *OAuth2Provider {
	t.Helper()
	provider, err := NewOAuth2Provider(Config{
		Name:         "test",
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		AuthURL:      "https://idp.example.com/oauth2/authorize",
		TokenURL:     server.URL + "/token",
		UserInfoURL:  server.URL + "/userinfo",
		RedirectURI:  "https://c.example.com/callback",
		Scopes:       []string{"openid", "profile"},
		SubjectField: subjectField,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return provider
}

func TestNewOAuth2ProviderValidation(t *testing.T) {
	base := Config{
		Name: "test", ClientID: "id", ClientSecret: "secret",
		AuthURL: "https://idp/auth", TokenURL: "https://idp/token",
		UserInfoURL: "https://idp/userinfo", RedirectURI: "https://c/callback",
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{name: "missing_name", mut: func(c *Config) { c.Name = " " }},
		{name: "missing_client_id", mut: func(c *Config) { c.ClientID = "" }},
		{name: "missing_secret", mut: func(c *Config) { c.ClientSecret = "" }},
		{name: "missing_auth_url", mut: func(c *Config) { c.AuthURL = "" }},
		{name: "missing_token_url", mut: func(c *Config) { c.TokenURL = "" }},
		{name: "missing_userinfo_url", mut: func(c *Config) { c.UserInfoURL = "" }},
		{name: "missing_redirect", mut: func(c *Config) { c.RedirectURI = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			if _, err := NewOAuth2Provider(cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOAuth2ProviderAuthorizationURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(server.Close)
	provider := testProvider(t, server, "")

	raw, err := provider.AuthorizationURL("state-1", "verifier-1")
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	sum := sha256.Sum256([]byte("verifier-1"))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"redirect_uri":          "https://c.example.com/callback",
		"state":                 "state-1",
		"code_challenge":        wantChallenge,
		"code_challenge_method": "S256",
		"scope":                 "openid profile",
	}
	for key, want := range checks {
		if got := query.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestOAuth2ProviderExchange(t *testing.T) {
	cases := []struct {
		name         string
		subjectField string
		userinfo     string
		tokenStatus  int
		wantSubject  string
		wantCode     int
	}{
		{
			name:        "string_subject",
			userinfo:    `{"sub":"user-1","name":"Alice"}`,
			wantSubject: "user-1",
		},
		{
			name:         "custom_numeric_field",
			subjectField: "uid",
			userinfo:     `{"uid":12345}`,
			wantSubject:  "12345",
		},
		{
			name:     "missing_subject",
			userinfo: `{"name":"Alice"}`,
			wantCode: apperr.CodeUnauthorized,
		},
		{
			name:        "token_rejected",
			tokenStatus: http.StatusBadRequest,
			wantCode:    apperr.CodeUnauthorized,
		},
		{
			name:        "token_server_error",
			tokenStatus: http.StatusInternalServerError,
			wantCode:    apperr.CodeServiceUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if tc.tokenStatus != 0 {
						w.WriteHeader(tc.tokenStatus)
						return
					}
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse form: %v", err)
					}
					clientID, secret, ok := r.BasicAuth()
					if !ok || clientID != "client-1" || secret != "secret-1" {
						t.Errorf("basic auth = %q/%q, ok=%v", clientID, secret, ok)
					}
					if r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") != "verifier-1" {
						t.Errorf("form = %v", r.Form)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"Bearer"}`))
				case "/userinfo":
					if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
						t.Errorf("authorization = %q", got)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.userinfo))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)

			provider := testProvider(t, server, tc.subjectField)
			info, err := provider.Exchange(context.Background(), "code-1", "verifier-1")
			if tc.wantCode != 0 {
				requireCode(t, err, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}
			if info.Subject != tc.wantSubject {
				t.Fatalf("subject = %q, want %q", info.Subject, tc.wantSubject)
			}
		})
	}
}

func TestOAuth2ProviderExchangeMissingArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(server.Close)
	provider := testProvider(t, server, "")

	if _, err := provider.Exchange(context.Background(), "", "verifier-1"); err == nil {
		t.Fatal("missing code must be rejected")
	}
	if _, err := provider.Exchange(context.Background(), "code-1", " "); err == nil {
		t.Fatal("missing verifier must be rejected")
	}
	if _, err := provider.AuthorizationURL("", "verifier-1"); err == nil {
		t.Fatal("missing state must be rejected")
	}
}
