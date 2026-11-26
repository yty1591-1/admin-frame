package rest

// 本文件覆盖 M2M 机器身份的两组 HTTP 端点（m2m.go）：
// 协议端点 /v1/auth/token 的 RFC 6749 错误语义，以及管理端点的生命周期。

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// m2mTestScope 是测试客户端的可申请权限码。
const m2mTestScope = "identity:account:read"

// newM2MHandlerFixture 构造启用 M2M 端点的 Handler；limiter 为 nil 时回退端点限流器。
func newM2MHandlerFixture(t *testing.T, limiter app.LoginLimiter) (f *e2eFixture, handler *Handler) {
	t.Helper()
	f = newE2EFixture(t, domain.ChannelAdmin)
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{string(f.channel)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
		Clock:    f.clock,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, f.channel, 15*time.Minute, 168*time.Hour, f.ids,
		app.WithTokenClock(f.clock), app.WithTokenParser(signer))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	clients, secrets := persistence.NewMemoryM2MRepositories()
	svc, err := app.NewM2MService(app.M2MServiceConfig{
		Clients:       clients,
		Secrets:       secrets,
		Tokens:        tokens,
		IDs:           f.ids,
		Channel:       f.channel,
		TokenTTL:      15 * time.Minute,
		RotateGrace:   time.Hour,
		AllowedScopes: []string{m2mTestScope},
		Service:       "identity-test",
		Clock:         f.clock,
	})
	if err != nil {
		t.Fatalf("new m2m service: %v", err)
	}
	handler, err = NewHandler(HandlerConfig{
		Auth:            f.auth,
		Channel:         f.channel,
		EndpointLimiter: f.limiter,
		M2M:             svc,
		M2MLimiter:      limiter,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return f, handler
}

// createM2MTestClient 通过管理端点创建客户端，返回对外 client_id 与一次性密钥。
func createM2MTestClient(t *testing.T, f *e2eFixture, handler *Handler, name string) (clientID, clientSecret string) {
	t.Helper()
	recorder := f.invoke(handler.CreateM2MClient, e2eCall{
		method:    http.MethodPost,
		target:    "/admin/v1/auth/m2m/clients",
		body:      `{"name":"` + name + `","allowed_scopes":["` + m2mTestScope + `"]}`,
		principal: f.principal("acct-admin", "sess-admin"),
	})
	requireStatus(t, recorder, http.StatusCreated)
	var payload struct {
		Client       m2mClientDTO `json:"client"`
		ClientSecret string       `json:"client_secret"`
	}
	decodeData(t, recorder, &payload)
	if payload.Client.ClientID == "" || payload.ClientSecret == "" {
		t.Fatalf("created client = %+v", payload)
	}
	if payload.Client.Status != "active" {
		t.Fatalf("created client status = %s", payload.Client.Status)
	}
	return payload.Client.ClientID, payload.ClientSecret
}

// m2mTokenForm 构造 client_credentials 表单。
func m2mTokenForm(clientID, clientSecret, scope string) string {
	values := url.Values{}
	values.Set("grant_type", "client_credentials")
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("scope", scope)
	return values.Encode()
}

// basicAuthHeader 构造 HTTP Basic 认证头。
func basicAuthHeader(clientID, clientSecret string) string {
	raw := clientID + ":" + clientSecret
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

// requireHeader 断言响应头。
func requireHeader(t *testing.T, recorder *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	if got := recorder.Header().Get(name); got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

// TestM2MEnabled 覆盖装配状态判断与路由处理器返回 nil 的约定。
func TestM2MEnabled(t *testing.T) {
	disabled := newE2EFixture(t, domain.ChannelAdmin)
	if disabled.handler.M2MEnabled() {
		t.Fatal("m2m must be disabled without wiring")
	}
	if M2MTokenHandler(disabled.handler) != nil {
		t.Fatal("token handler must be nil when m2m is not wired")
	}
	if M2MTokenHandler(nil) != nil {
		t.Fatal("token handler must be nil for nil handler")
	}

	_, handler := newM2MHandlerFixture(t, nil)
	if !handler.M2MEnabled() {
		t.Fatal("m2m must be enabled after wiring")
	}
	if M2MTokenHandler(handler) == nil {
		t.Fatal("token handler must be non-nil when m2m is wired")
	}
}

// TestM2MTokenEndpoint 覆盖协议端点的错误体与成功响应。
func TestM2MTokenEndpoint(t *testing.T) {
	f, handler := newM2MHandlerFixture(t, nil)
	clientID, clientSecret := createM2MTestClient(t, f, handler, "billing-worker")

	t.Run("not_configured", func(t *testing.T) {
		recorder := f.invoke(f.handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body: m2mTokenForm(clientID, clientSecret, m2mTestScope),
		})
		requireStatus(t, recorder, http.StatusNotFound)
		requireHeader(t, recorder, "Cache-Control", "no-store")
		requireHeader(t, recorder, "X-Trace-Id", e2eTraceID)
	})

	t.Run("unsupported_content_type", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body: m2mTokenForm(clientID, clientSecret, m2mTestScope),
		})
		requireStatus(t, recorder, http.StatusUnsupportedMediaType)
		requireRFC6749Error(t, recorder, "invalid_request")
	})

	t.Run("query_parameters_rejected", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method:      http.MethodPost,
			target:      "/admin/v1/auth/token?client_id=" + url.QueryEscape(clientID),
			body:        m2mTokenForm(clientID, clientSecret, m2mTestScope),
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireRFC6749Error(t, recorder, "invalid_request")
	})

	t.Run("unsupported_grant_type", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        "grant_type=password&client_id=" + clientID + "&client_secret=" + clientSecret,
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireRFC6749Error(t, recorder, "unsupported_grant_type")
	})

	t.Run("missing_credentials", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        "grant_type=client_credentials&scope=" + m2mTestScope,
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireRFC6749Error(t, recorder, "invalid_client")
		if recorder.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("invalid_client must carry WWW-Authenticate")
		}
	})

	t.Run("basic_and_form_mismatch", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        m2mTokenForm(clientID, clientSecret, m2mTestScope),
			contentType: "application/x-www-form-urlencoded",
			headers:     map[string]string{"Authorization": basicAuthHeader(clientID, "other-secret")},
		})
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireRFC6749Error(t, recorder, "invalid_client")
		if recorder.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("mismatched credentials must carry WWW-Authenticate")
		}
	})

	t.Run("unknown_client", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        m2mTokenForm("client-not-exist", "secret", m2mTestScope),
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireRFC6749Error(t, recorder, "invalid_client")
	})

	t.Run("invalid_scope", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        m2mTokenForm(clientID, clientSecret, "order:write"),
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireRFC6749Error(t, recorder, "invalid_scope")
	})

	t.Run("success_with_form_credentials", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/token",
			body:        m2mTokenForm(clientID, clientSecret, m2mTestScope),
			contentType: "application/x-www-form-urlencoded",
		})
		requireStatus(t, recorder, http.StatusOK)
		requireHeader(t, recorder, "Cache-Control", "no-store")
		requireHeader(t, recorder, "X-Trace-Id", e2eTraceID)
		requireM2MTokenBody(t, recorder)
	})

	t.Run("success_with_basic_credentials", func(t *testing.T) {
		recorder := f.invoke(handler.M2MToken, e2eCall{
			method:      http.MethodPost,
			target:      "/admin/v1/auth/token",
			body:        "grant_type=client_credentials&scope=" + m2mTestScope,
			contentType: "application/x-www-form-urlencoded",
			headers:     map[string]string{"Authorization": basicAuthHeader(clientID, clientSecret)},
		})
		requireStatus(t, recorder, http.StatusOK)
		requireM2MTokenBody(t, recorder)
	})
}

// TestM2MTokenRateLimited 覆盖签发端点限流拒绝与 Retry-After。
func TestM2MTokenRateLimited(t *testing.T) {
	f, handler := newM2MHandlerFixture(t, denyLimiter{retry: 3 * time.Second})
	clientID, clientSecret := createM2MTestClient(t, f, handler, "limited-worker")

	recorder := f.invoke(handler.M2MToken, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/token",
		body:        m2mTokenForm(clientID, clientSecret, m2mTestScope),
		contentType: "application/x-www-form-urlencoded",
	})
	requireStatus(t, recorder, http.StatusTooManyRequests)
	requireHeader(t, recorder, "Retry-After", "3")
	requireRFC6749Error(t, recorder, "temporarily_unavailable")
}

// TestM2MAdminEndpoints 覆盖客户端创建、查询、轮换、停用、启用与删除。
func TestM2MAdminEndpoints(t *testing.T) {
	f, handler := newM2MHandlerFixture(t, nil)
	principal := f.principal("acct-admin", "sess-admin")

	t.Run("not_configured", func(t *testing.T) {
		cases := []struct {
			name   string
			fn     http.HandlerFunc
			target string
		}{
			{name: "list", fn: f.handler.ListM2MClients, target: "/admin/v1/auth/m2m/clients"},
			{name: "create", fn: f.handler.CreateM2MClient, target: "/admin/v1/auth/m2m/clients"},
			{name: "rotate", fn: f.handler.RotateM2MSecret, target: "/admin/v1/auth/m2m/clients/x/secrets"},
			{name: "disable", fn: f.handler.DisableM2MClient, target: "/admin/v1/auth/m2m/clients/x/disable"},
			{name: "enable", fn: f.handler.EnableM2MClient, target: "/admin/v1/auth/m2m/clients/x/enable"},
			{name: "delete", fn: f.handler.DeleteM2MClient, target: "/admin/v1/auth/m2m/clients/x"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				recorder := f.invoke(tc.fn, e2eCall{
					method:     http.MethodPost,
					target:     tc.target,
					principal:  principal,
					pathValues: map[string]string{"id": "x"},
				})
				requireStatus(t, recorder, http.StatusNotFound)
				requireErrorCode(t, recorder, apperr.CodeNotFound)
			})
		}
	})

	t.Run("invalid_pagination", func(t *testing.T) {
		recorder := f.invoke(handler.ListM2MClients, e2eCall{
			method:    http.MethodGet,
			target:    "/admin/v1/auth/m2m/clients?page_size=101",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, apperr.CodeBadRequest)
	})

	t.Run("invalid_body", func(t *testing.T) {
		recorder := f.invoke(handler.CreateM2MClient, e2eCall{
			method:    http.MethodPost,
			target:    "/admin/v1/auth/m2m/clients",
			body:      `{"name":`,
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, apperr.CodeBadRequest)
	})

	t.Run("scope_not_allowed", func(t *testing.T) {
		recorder := f.invoke(handler.CreateM2MClient, e2eCall{
			method:    http.MethodPost,
			target:    "/admin/v1/auth/m2m/clients",
			body:      `{"name":"bad-worker","allowed_scopes":["order:write"]}`,
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, apperr.CodeBadRequest)
	})

	clientID, clientSecret := createM2MTestClient(t, f, handler, "billing-worker")

	t.Run("list_created_client", func(t *testing.T) {
		recorder := f.invoke(handler.ListM2MClients, e2eCall{
			method:    http.MethodGet,
			target:    "/admin/v1/auth/m2m/clients?status=active&channel=admin",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload struct {
			Items []m2mClientDTO `json:"items"`
			Total int            `json:"total"`
		}
		decodeData(t, recorder, &payload)
		if payload.Total != 1 || len(payload.Items) != 1 {
			t.Fatalf("list payload = %+v", payload)
		}
		if payload.Items[0].ClientID != clientID {
			t.Fatalf("client_id = %s, want %s", payload.Items[0].ClientID, clientID)
		}
	})

	t.Run("rotate_missing_client", func(t *testing.T) {
		recorder := f.invoke(handler.RotateM2MSecret, e2eCall{
			method:     http.MethodPost,
			target:     "/admin/v1/auth/m2m/clients/missing/secrets",
			principal:  principal,
			pathValues: map[string]string{"id": "missing"},
		})
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, apperr.CodeNotFound)
	})

	t.Run("rotate_secret", func(t *testing.T) {
		recorder := f.invoke(handler.RotateM2MSecret, e2eCall{
			method:     http.MethodPost,
			target:     "/admin/v1/auth/m2m/clients/" + clientID + "/secrets",
			principal:  principal,
			pathValues: map[string]string{"id": clientID},
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Fingerprint  string `json:"fingerprint"`
			RetiringAt   string `json:"retiring_expires_at"`
		}
		decodeData(t, recorder, &payload)
		if payload.ClientID != clientID || payload.ClientSecret == "" || payload.Fingerprint == "" {
			t.Fatalf("rotate payload = %+v", payload)
		}
		if payload.RetiringAt == "" {
			t.Fatal("rotate_grace enabled must return retiring_expires_at")
		}
	})

	t.Run("disable_client", func(t *testing.T) {
		recorder := f.invoke(handler.DisableM2MClient, e2eCall{
			method:     http.MethodPost,
			target:     "/admin/v1/auth/m2m/clients/" + clientID + "/disable",
			principal:  principal,
			pathValues: map[string]string{"id": clientID},
		})
		requireStatus(t, recorder, http.StatusNoContent)

		listed := f.invoke(handler.ListM2MClients, e2eCall{
			method:    http.MethodGet,
			target:    "/admin/v1/auth/m2m/clients?status=disabled",
			principal: principal,
		})
		requireStatus(t, listed, http.StatusOK)
		var payload struct {
			Items []m2mClientDTO `json:"items"`
		}
		decodeData(t, listed, &payload)
		if len(payload.Items) != 1 || payload.Items[0].Status != "disabled" {
			t.Fatalf("disabled list = %+v", payload)
		}
	})

	t.Run("enable_client", func(t *testing.T) {
		recorder := f.invoke(handler.EnableM2MClient, e2eCall{
			method:     http.MethodPost,
			target:     "/admin/v1/auth/m2m/clients/" + clientID + "/enable",
			principal:  principal,
			pathValues: map[string]string{"id": clientID},
		})
		requireStatus(t, recorder, http.StatusNoContent)

		listed := f.invoke(handler.ListM2MClients, e2eCall{
			method:    http.MethodGet,
			target:    "/admin/v1/auth/m2m/clients?status=active",
			principal: principal,
		})
		requireStatus(t, listed, http.StatusOK)
		var payload struct {
			Items []m2mClientDTO `json:"items"`
		}
		decodeData(t, listed, &payload)
		if len(payload.Items) != 1 || payload.Items[0].Status != "active" {
			t.Fatalf("enabled list = %+v", payload)
		}
	})

	t.Run("delete_client", func(t *testing.T) {
		recorder := f.invoke(handler.DeleteM2MClient, e2eCall{
			method:     http.MethodDelete,
			target:     "/admin/v1/auth/m2m/clients/" + clientID,
			principal:  principal,
			pathValues: map[string]string{"id": clientID},
		})
		requireStatus(t, recorder, http.StatusNoContent)

		listed := f.invoke(handler.ListM2MClients, e2eCall{
			method:    http.MethodGet,
			target:    "/admin/v1/auth/m2m/clients?status=deleted",
			principal: principal,
		})
		requireStatus(t, listed, http.StatusOK)
		var payload struct {
			Items []m2mClientDTO `json:"items"`
		}
		decodeData(t, listed, &payload)
		if len(payload.Items) != 1 || payload.Items[0].DeletedAt == "" {
			t.Fatalf("deleted list = %+v", payload)
		}
	})

	// 删除后旧密钥不可再签发令牌：客户端状态不再是 active。
	tokenRecorder := f.invoke(handler.M2MToken, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/token",
		body:        m2mTokenForm(clientID, clientSecret, m2mTestScope),
		contentType: "application/x-www-form-urlencoded",
	})
	requireStatus(t, tokenRecorder, http.StatusUnauthorized)
	requireRFC6749Error(t, tokenRecorder, "invalid_client")
}

// requireRFC6749Error 断言 RFC 6749 错误体中的 error 字段。
func requireRFC6749Error(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body rfc6749Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode rfc6749 error from %q: %v", recorder.Body.String(), err)
	}
	if body.Error != want {
		t.Fatalf("error = %q, want %q (body=%s)", body.Error, want, recorder.Body.String())
	}
}

// requireM2MTokenBody 断言令牌响应的关键字段。
func requireM2MTokenBody(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		AccessToken string  `json:"access_token"`
		TokenType   string  `json:"token_type"`
		ExpiresIn   float64 `json:"expires_in"`
		Scope       string  `json:"scope"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode token response from %q: %v", recorder.Body.String(), err)
	}
	if body.AccessToken == "" || body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		t.Fatalf("token response = %+v", body)
	}
	if body.Scope != m2mTestScope {
		t.Fatalf("scope = %q, want %q", body.Scope, m2mTestScope)
	}
}
