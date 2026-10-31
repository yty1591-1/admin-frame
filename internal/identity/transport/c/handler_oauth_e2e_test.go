package c

import (
	"context"
	"net/http"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestE2EOAuthAuthorizeCallback 覆盖 OAuth 授权发起、回调登录与失败分支。
func TestE2EOAuthAuthorizeCallback(t *testing.T) {
	f := newCFixture(t)
	account := f.seedAccount(t, "acct-oauth", "oauth-user@example.com", true)
	f.seedIdentity(t, account.ID, "test", "sub:login-code")

	// OAuth 登录路由按 provider 名注册为字面量路径，Handler 依赖路径参数，
	// 因此这里直调 Handler 并显式注入 provider。
	providerValues := map[string]string{"provider": "test"}
	recorder := f.invoke(f.handler.OAuthAuthorize, http.MethodPost,
		"/c/v1/auth/oauth/test/authorize", "", "", providerValues)
	requireCStatus(t, recorder, http.StatusOK)
	var authorization oauthAuthorizeResponse
	decodeCData(t, recorder, &authorization)
	if authorization.AuthorizeURL == "" || authorization.State == "" {
		t.Fatalf("authorize response = %+v", authorization)
	}

	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback",
		`{"code":"login-code","state":"`+authorization.State+`"}`, "", providerValues)
	requireCStatus(t, recorder, http.StatusOK)
	var tokens tokenResponse
	decodeCData(t, recorder, &tokens)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("oauth login token response = %+v", tokens)
	}
	if tokens.AbsoluteExpiresAt == nil {
		t.Fatal("oauth login must return absolute expiry")
	}

	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback",
		`{"code":"login-code","state":"invalid-state"}`, "", providerValues)
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback", `{}`, "", providerValues)
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback", `{`, "", providerValues)
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)

	// 未配置 provider 的路由不存在。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/oauth/other/authorize", "", "")
	requireCStatus(t, recorder, http.StatusNotFound)

	// 有 state 但外部主体未映射且未开启自动建号。
	recorder = f.invoke(f.handler.OAuthAuthorize, http.MethodPost,
		"/c/v1/auth/oauth/test/authorize", "", "", providerValues)
	requireCStatus(t, recorder, http.StatusOK)
	decodeCData(t, recorder, &authorization)
	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback",
		`{"code":"orphan-code","state":"`+authorization.State+`"}`, "", providerValues)
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EOAuthBindings 覆盖身份绑定列表、发起、回调、解绑与失联保护。
func TestE2EOAuthBindings(t *testing.T) {
	f := newCFixture(t)
	account := f.seedAccount(t, "acct-bind", "bind-user@example.com", true)

	recorder := f.do(t, http.MethodGet, "/c/v1/me/bindings", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)
	var bindings []oauthBindingDTO
	decodeCData(t, recorder, &bindings)
	if len(bindings) != 0 {
		t.Fatalf("bindings = %+v, want empty", bindings)
	}

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/authorize", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)
	var authorization oauthAuthorizeResponse
	decodeCData(t, recorder, &authorization)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/callback",
		`{"code":"bind-code","state":"`+authorization.State+`"}`, account.ID)
	requireCStatus(t, recorder, http.StatusNoContent)

	recorder = f.do(t, http.MethodGet, "/c/v1/me/bindings", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)
	bindings = nil
	decodeCData(t, recorder, &bindings)
	if len(bindings) != 1 || bindings[0].Provider != "test" {
		t.Fatalf("bindings = %+v, want one test binding", bindings)
	}

	// 同一外部身份再次绑定返回 40900。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/authorize", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)
	decodeCData(t, recorder, &authorization)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/callback",
		`{"code":"bind-code","state":"`+authorization.State+`"}`, account.ID)
	requireCStatus(t, recorder, http.StatusConflict)
	requireCErrorCode(t, recorder, apperr.CodeConflict)

	// 解绑成功。
	recorder = f.do(t, http.MethodDelete, "/c/v1/me/bindings/test", "", account.ID)
	requireCStatus(t, recorder, http.StatusNoContent)

	// 未绑定的 provider 解绑返回 40400。
	recorder = f.do(t, http.MethodDelete, "/c/v1/me/bindings/test", "", account.ID)
	requireCStatus(t, recorder, http.StatusNotFound)
	requireCErrorCode(t, recorder, apperr.CodeNotFound)

	// 未认证分支。
	recorder = f.do(t, http.MethodGet, "/c/v1/me/bindings", "", "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/authorize", "", "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/callback",
		`{"code":"x","state":"y"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.do(t, http.MethodDelete, "/c/v1/me/bindings/test", "", "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 非法回调请求体。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/bindings/test/callback", `{`, account.ID)
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)
}

// TestE2EOAuthUnbindLastCredential 覆盖无密码且仅剩一个绑定时禁止解绑。
func TestE2EOAuthUnbindLastCredential(t *testing.T) {
	f := newCFixture(t)
	account := f.seedAccount(t, "acct-oauth-only", "oauth-only@example.com", false)
	f.seedIdentity(t, account.ID, "test", "sub:only")

	recorder := f.do(t, http.MethodDelete, "/c/v1/me/bindings/test", "", account.ID)
	requireCStatus(t, recorder, http.StatusConflict)
	requireCErrorCode(t, recorder, apperr.CodeConflict)

	// 绑定列表仍可读取。
	recorder = f.do(t, http.MethodGet, "/c/v1/me/bindings", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)
	var bindings []oauthBindingDTO
	decodeCData(t, recorder, &bindings)
	if len(bindings) != 1 {
		t.Fatalf("bindings = %+v, want one", bindings)
	}
}

// TestE2EOAuthDisabled 覆盖 OAuth 未启用时各端点的降级响应。
func TestE2EOAuthDisabled(t *testing.T) {
	f := newCFixture(t, withoutOAuth())

	recorder := f.invoke(f.handler.OAuthAuthorize, http.MethodPost,
		"/c/v1/auth/oauth/test/authorize", "", "", map[string]string{"provider": "test"})
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.OAuthCallback, http.MethodPost,
		"/c/v1/auth/oauth/test/callback", `{"code":"x","state":"y"}`, "",
		map[string]string{"provider": "test"})
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.ListBindings, http.MethodGet, "/c/v1/me/bindings", "", "acct-1", nil)
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.Unbind, http.MethodDelete, "/c/v1/me/bindings/test", "", "acct-1",
		map[string]string{"provider": "test"})
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.OAuthBindAuthorize, http.MethodPost,
		"/c/v1/auth/bindings/test/authorize", "", "acct-1", map[string]string{"provider": "test"})
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.OAuthBindCallback, http.MethodPost,
		"/c/v1/auth/bindings/test/callback", `{"code":"x","state":"y"}`, "acct-1",
		map[string]string{"provider": "test"})
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)
}

// TestE2EOAuthBindingsPathValues 覆盖 provider 路径参数缺失时的处理。
func TestE2EOAuthBindingsPathValues(t *testing.T) {
	f := newCFixture(t)
	account := f.seedAccount(t, "acct-bind-path", "bind-path@example.com", true)

	// 未知 provider 发起绑定返回 40400。
	recorder := f.invoke(f.handler.OAuthBindAuthorize, http.MethodPost,
		"/c/v1/auth/bindings/unknown/authorize", "", account.ID, map[string]string{"provider": "unknown"})
	requireCStatus(t, recorder, http.StatusNotFound)
	requireCErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.do(t, http.MethodDelete, "/c/v1/me/bindings/unknown", "", account.ID)
	requireCStatus(t, recorder, http.StatusNotFound)
	requireCErrorCode(t, recorder, apperr.CodeNotFound)

	// 无绑定账号的列表为空数组。
	recorder = f.do(t, http.MethodGet, "/c/v1/me/bindings", "", account.ID)
	requireCStatus(t, recorder, http.StatusOK)

	identities, err := f.identities.ListByAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(identities) != 0 {
		t.Fatalf("identities = %+v, want empty", identities)
	}

	// 账号须存在于 C 端；绑定回调仍按账号隔离。
	if _, err := f.accounts.FindByID(context.Background(), account.ID); err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Channel != domain.ChannelC {
		t.Fatalf("channel = %q, want c", account.Channel)
	}
}
