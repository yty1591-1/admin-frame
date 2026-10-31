package rest

import (
	"net/http"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// tokenData 是登录 / 刷新响应的 data 段。
type tokenData struct {
	AccessToken       string     `json:"access_token"`
	RefreshToken      string     `json:"refresh_token"`
	TokenType         string     `json:"token_type"`
	ExpiresIn         int64      `json:"expires_in"`
	AbsoluteExpiresAt *time.Time `json:"absolute_expires_at"`
}

// TestE2ELoginSuccessAndFailure 覆盖密码登录成功、失败与请求体校验。
func TestE2ELoginSuccessAndFailure(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-login", "alice", "Alice#Passw0rd")

	cases := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantCode    int
	}{
		{name: "success", body: `{"subject":"alice","password":"Alice#Passw0rd"}`, wantStatus: http.StatusOK},
		{name: "wrong_password", body: `{"subject":"alice","password":"wrong-pass"}`, wantStatus: http.StatusUnauthorized, wantCode: apperr.CodeUnauthorized},
		{name: "unknown_subject", body: `{"subject":"nobody","password":"Alice#Passw0rd"}`, wantStatus: http.StatusUnauthorized, wantCode: apperr.CodeUnauthorized},
		{name: "invalid_body", body: `{"subject":`, wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest},
		{name: "unknown_field", body: `{"subject":"alice","password":"Alice#Passw0rd","role":"admin"}`, wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest},
		{name: "non_json_content_type", body: `{"subject":"alice","password":"Alice#Passw0rd"}`, contentType: "text/plain", wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest},
		{name: "empty_body", body: "", wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := f.invoke(f.handler.Login, e2eCall{
				method: http.MethodPost, target: "/admin/v1/auth/login",
				body: tc.body, contentType: tc.contentType,
			})
			requireStatus(t, recorder, tc.wantStatus)
			if tc.wantCode != 0 {
				requireErrorCode(t, recorder, tc.wantCode)
				return
			}
			var data tokenData
			decodeData(t, recorder, &data)
			if data.AccessToken == "" || data.RefreshToken == "" || data.TokenType != "Bearer" {
				t.Fatalf("token response = %+v", data)
			}
			if data.AbsoluteExpiresAt == nil {
				t.Fatal("absolute_expires_at must be present for device sessions")
			}
		})
	}
}

// TestE2ELoginMFAIntermediateStates 覆盖强制绑定、两步校验与强制改密三种中间态。
func TestE2ELoginMFAIntermediateStates(t *testing.T) {
	// 强制绑定：登录返回 setup 票据，绑定并发码后直接签发令牌。
	t.Run("mfa_binding_required", func(t *testing.T) {
		f := newE2EFixture(t, domain.ChannelAdmin, withE2EMFARequired())
		f.seedAccount(t, "acct-bind", "bind-user", "Bind#Passw0rd12")

		recorder := f.invoke(f.handler.Login, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/login",
			body: `{"subject":"bind-user","password":"Bind#Passw0rd12"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var binding struct {
			Required bool   `json:"mfa_binding_required"`
			Ticket   string `json:"mfa_setup_ticket"`
		}
		decodeData(t, recorder, &binding)
		if !binding.Required || binding.Ticket == "" {
			t.Fatalf("binding challenge = %+v", binding)
		}

		recorder = f.invoke(f.handler.SetupMFA, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/mfa/setup",
			body: `{"mfa_setup_ticket":"` + binding.Ticket + `"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var setup mfaSetupResponse
		decodeData(t, recorder, &setup)
		if setup.Secret == "" || setup.OTPAuthURI == "" || len(setup.RecoveryCodes) == 0 {
			t.Fatalf("setup response = %+v", setup)
		}

		recorder = f.invoke(f.handler.VerifyMFA, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/mfa/verify",
			body: `{"mfa_setup_ticket":"` + binding.Ticket + `","code":"` + totpCode(t, setup.Secret, f.clock.Now()) + `"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var tokens tokenData
		decodeData(t, recorder, &tokens)
		if tokens.AccessToken == "" {
			t.Fatal("forced binding must issue tokens")
		}
	})

	// 两步校验：已绑定 MFA 的账号登录只返回票据，校验通过后签发令牌。
	t.Run("mfa_required", func(t *testing.T) {
		f := newE2EFixture(t, domain.ChannelAdmin)
		f.seedAccount(t, "acct-mfa", "mfa-user", "Mfa#Passw0rd12")
		secret := f.bindMFA(t, "acct-mfa")

		recorder := f.invoke(f.handler.Login, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/login",
			body: `{"subject":"mfa-user","password":"Mfa#Passw0rd12"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var challenge struct {
			Required bool   `json:"mfa_required"`
			Ticket   string `json:"mfa_ticket"`
		}
		decodeData(t, recorder, &challenge)
		if !challenge.Required || challenge.Ticket == "" {
			t.Fatalf("mfa challenge = %+v", challenge)
		}

		recorder = f.invoke(f.handler.VerifyMFA, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/mfa/verify",
			body: `{"mfa_ticket":"` + challenge.Ticket + `","code":"` + totpCode(t, secret, f.clock.Now()) + `"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var tokens tokenData
		decodeData(t, recorder, &tokens)
		if tokens.AccessToken == "" {
			t.Fatal("mfa verify must issue tokens")
		}
	})

	// 强制改密：登录返回改密票据，凭票据改密后签发令牌。
	t.Run("password_change_required", func(t *testing.T) {
		f := newE2EFixture(t, domain.ChannelAdmin)
		f.seedAccount(t, "acct-reset", "reset-user", "Reset#Passw0rd12")
		f.forceMustChangePassword(t, "acct-reset")

		recorder := f.invoke(f.handler.Login, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/login",
			body: `{"subject":"reset-user","password":"Reset#Passw0rd12"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var challenge struct {
			Required bool   `json:"password_change_required"`
			Ticket   string `json:"password_change_ticket"`
		}
		decodeData(t, recorder, &challenge)
		if !challenge.Required || challenge.Ticket == "" {
			t.Fatalf("password change challenge = %+v", challenge)
		}

		recorder = f.invoke(f.handler.ChangePassword, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/password/change",
			body: `{"password_change_ticket":"` + challenge.Ticket + `","new_password":"Reset#Passw0rd34"}`,
		})
		requireStatus(t, recorder, http.StatusOK)
		var tokens tokenData
		decodeData(t, recorder, &tokens)
		if tokens.AccessToken == "" {
			t.Fatal("password change ticket must issue tokens")
		}
	})
}

// TestE2EMeAndUpdateMe 覆盖 GET /me 与 PATCH /me。
func TestE2EMeAndUpdateMe(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-me", "me-user", "Me#Passw0rd12")
	login := f.login(t, "me-user", "Me#Passw0rd12")
	principal := f.principal(login.AccountID, login.SessionID)

	recorder := f.invoke(f.handler.Me, e2eCall{
		method: http.MethodGet, target: "/admin/v1/me", principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	var me meResponse
	decodeData(t, recorder, &me)
	if me.SubjectID != "acct-me" || me.Channel != "admin" || !me.HasPassword {
		t.Fatalf("me response = %+v", me)
	}

	// 缺少鉴权上下文返回 40100，而非 404。
	recorder = f.invoke(f.handler.Me, e2eCall{method: http.MethodGet, target: "/admin/v1/me"})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.Me, e2eCall{
		method: http.MethodGet, target: "/admin/v1/me",
		principal: f.principal("missing", ""),
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 更新昵称成功并回读。
	recorder = f.invoke(f.handler.UpdateMe, e2eCall{
		method: http.MethodPatch, target: "/admin/v1/me", principal: principal,
		body: `{"display_name":"张三"}`,
	})
	requireStatus(t, recorder, http.StatusOK)
	var updated meResponse
	decodeData(t, recorder, &updated)
	if updated.DisplayName != "张三" {
		t.Fatalf("display_name = %q, want 张三", updated.DisplayName)
	}

	// 字段缺失按 400 处理；显式空串表示清除。
	recorder = f.invoke(f.handler.UpdateMe, e2eCall{
		method: http.MethodPatch, target: "/admin/v1/me", principal: principal,
		body: `{}`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.UpdateMe, e2eCall{
		method: http.MethodPatch, target: "/admin/v1/me", principal: principal,
		body: `{"display_name":""}`,
	})
	requireStatus(t, recorder, http.StatusOK)
	decodeData(t, recorder, &updated)
	if updated.DisplayName != "" {
		t.Fatalf("display_name = %q, want cleared", updated.DisplayName)
	}

	recorder = f.invoke(f.handler.UpdateMe, e2eCall{method: http.MethodPatch, target: "/admin/v1/me"})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EMFASetupVerifyDisable 覆盖已登录账号的 MFA 绑定、确认与解绑。
func TestE2EMFASetupVerifyDisable(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-mfa-life", "mfa-life", "Life#Passw0rd12")
	login := f.login(t, "mfa-life", "Life#Passw0rd12")
	principal := f.principal(login.AccountID, login.SessionID)

	// 已登录但不带请求体即可发起绑定。
	recorder := f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup", principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	var setup mfaSetupResponse
	decodeData(t, recorder, &setup)

	recorder = f.invoke(f.handler.VerifyMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/verify", principal: principal,
		body: `{"code":"` + totpCode(t, setup.Secret, f.clock.Now()) + `"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	f.clock.advance(30 * time.Second)

	// 已确认绑定后再次初始化返回 40900。
	recorder = f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup", principal: principal,
	})
	requireStatus(t, recorder, http.StatusConflict)
	requireErrorCode(t, recorder, apperr.CodeConflict)

	// 错误动态码拒绝。
	recorder = f.invoke(f.handler.DisableMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/disable", principal: principal,
		body: `{"code":"000000"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 正确动态码解绑成功。
	recorder = f.invoke(f.handler.DisableMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/disable", principal: principal,
		body: `{"code":"` + totpCode(t, setup.Secret, f.clock.Now()) + `"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	// 未认证与票据互斥的入参校验。
	recorder = f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup",
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.VerifyMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/verify",
		body: `{"mfa_ticket":"a","mfa_setup_ticket":"b","code":"123456"}`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.VerifyMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/verify",
		body: `{"code":"123456"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EChangePassword 覆盖 Bearer 自助改密与入参校验。
func TestE2EChangePassword(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-password", "pwd-user", "Old#Passw0rd12")
	login := f.login(t, "pwd-user", "Old#Passw0rd12")
	principal := f.principal(login.AccountID, login.SessionID)

	recorder := f.invoke(f.handler.ChangePassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/password/change", principal: principal,
		body: `{"old_password":"Old#Passw0rd12","new_password":"New#Passw0rd12"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	// 改密后旧密码立即失效。
	if _, err := f.auth.Login(t.Context(), app.LoginInput{Subject: "pwd-user", Password: "Old#Passw0rd12"}); err == nil {
		t.Fatal("old password must be rejected after change")
	}

	// 旧密码错误返回 40100；弱口令返回 40000；缺少鉴权返回 40100。
	recorder = f.invoke(f.handler.ChangePassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/password/change", principal: principal,
		body: `{"old_password":"Wrong#Passw0rd12","new_password":"Other#Passw0rd12"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.ChangePassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/password/change", principal: principal,
		body: `{"old_password":"New#Passw0rd12","new_password":"short"}`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.ChangePassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/password/change",
		body: `{"old_password":"New#Passw0rd12","new_password":"Other#Passw0rd12"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2ERefreshAndLogout 覆盖刷新轮换、重放、登出与全设备登出。
func TestE2ERefreshAndLogout(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-refresh", "refresh-user", "Refresh#Passw0rd12")
	login := f.login(t, "refresh-user", "Refresh#Passw0rd12")

	// 正常刷新返回新令牌对。
	recorder := f.invoke(f.handler.Refresh, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/refresh",
		body: `{"refresh_token":"` + login.RefreshToken + `"}`,
	})
	requireStatus(t, recorder, http.StatusOK)
	var refreshed tokenData
	decodeData(t, recorder, &refreshed)
	if refreshed.AccessToken == "" || refreshed.RefreshToken == login.RefreshToken {
		t.Fatalf("refresh response = %+v", refreshed)
	}
	if keys := f.limiter.snapshot(); len(keys) == 0 {
		t.Fatal("endpoint limiter must be consulted by refresh")
	}

	// 已轮换令牌再次出现按重放拒绝。
	recorder = f.invoke(f.handler.Refresh, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/refresh",
		body: `{"refresh_token":"` + login.RefreshToken + `"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 非法令牌与非法请求体。
	recorder = f.invoke(f.handler.Refresh, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/refresh",
		body: `{"refresh_token":"not-a-token"}`,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.Refresh, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/refresh", body: `{`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	// 登出当前会话；重复登出保持幂等 204。
	principal := f.principal(login.AccountID, login.SessionID)
	recorder = f.invoke(f.handler.Logout, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout", principal: principal,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.Logout, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout", principal: principal,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.Logout, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout",
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 全设备登出递增版本并撤销会话。
	recorder = f.invoke(f.handler.LogoutAll, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout-all", principal: principal,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.LogoutAll, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout-all",
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EEndpointRateLimit 覆盖 refresh / logout 的端点限流拒绝与 Retry-After。
func TestE2EEndpointRateLimit(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin, withE2EDenyLimiter(3*time.Second))
	f.seedAccount(t, "acct-limit", "limit-user", "Limit#Passw0rd12")
	login := f.login(t, "limit-user", "Limit#Passw0rd12")

	recorder := f.invoke(f.handler.Refresh, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/refresh",
		body: `{"refresh_token":"` + login.RefreshToken + `"}`,
	})
	requireStatus(t, recorder, http.StatusTooManyRequests)
	requireErrorCode(t, recorder, apperr.CodeTooManyRequests)
	if got := recorder.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After = %q, want 3", got)
	}

	recorder = f.invoke(f.handler.Logout, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/logout",
		principal: f.principal(login.AccountID, login.SessionID),
	})
	requireStatus(t, recorder, http.StatusTooManyRequests)
	requireErrorCode(t, recorder, apperr.CodeTooManyRequests)
}

// TestE2ESessionManagement 覆盖本人会话列表、撤销与分页参数校验。
func TestE2ESessionManagement(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-session", "session-user", "Session#Passw0rd12")
	first := f.login(t, "session-user", "Session#Passw0rd12")
	second := f.login(t, "session-user", "Session#Passw0rd12")

	principal := f.principal(first.AccountID, first.SessionID)
	recorder := f.invoke(f.handler.ListSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/sessions", principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	var page sessionListResponse
	decodeData(t, recorder, &page)
	if len(page.Items) != 2 {
		t.Fatalf("session items = %d, want 2", len(page.Items))
	}
	current := ""
	for _, item := range page.Items {
		if item.Current {
			current = item.ID
		}
	}
	if current == "" {
		t.Fatalf("current session must be marked, page=%+v", page)
	}
	if current != first.SessionID && current != second.SessionID {
		t.Fatalf("current session id = %q, unexpected", current)
	}

	// 撤销指定设备；不存在设备统一 404。
	recorder = f.invoke(f.handler.RevokeSession, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/sessions/" + current,
		principal: principal, pathValues: map[string]string{"id": current},
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.RevokeSession, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/sessions/missing",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	// 分页参数非法。
	recorder = f.invoke(f.handler.ListSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/sessions?limit=abc", principal: principal,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.ListSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/sessions?cursor=bad", principal: principal,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	// 未认证与吊销缺少设备 ID。
	recorder = f.invoke(f.handler.ListSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/sessions",
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.invoke(f.handler.RevokeSession, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/sessions/",
		principal: principal, pathValues: map[string]string{"id": ""},
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)
}

// TestE2EDecodeOptionalBody 覆盖可选请求体为空与非 JSON 内容类型。
func TestE2EDecodeOptionalBody(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-body", "body-user", "Body#Passw0rd12")
	login := f.login(t, "body-user", "Body#Passw0rd12")
	principal := f.principal(login.AccountID, login.SessionID)

	// 可选请求体为空视为未提供。
	recorder := f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup", principal: principal,
		body: "", contentType: "",
	})
	requireStatus(t, recorder, http.StatusOK)

	// 显式非 JSON 媒体类型被拒绝。
	recorder = f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup", principal: principal,
		body: `{"mfa_setup_ticket":""}`, contentType: "text/plain",
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	// 尾随 JSON 值被拒绝，避免解析歧义。
	recorder = f.invoke(f.handler.SetupMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/mfa/setup", principal: principal,
		body: `{"mfa_setup_ticket":""}{}`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)
}
