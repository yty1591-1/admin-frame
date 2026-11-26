package rest

// 本文件覆盖 SCIM 2.0 的 A 端管理端点（scim_admin.go）：
// 令牌的签发 / 列表 / 吊销，组列表与未装配分支。

import (
	"net/http"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// newSCIMHandlerFixture 构造启用 SCIM 管理端点的 Handler。
func newSCIMHandlerFixture(t *testing.T) (f *e2eFixture, handler *Handler) {
	t.Helper()
	f = newE2EFixture(t, domain.ChannelAdmin)
	scimTokens, scimGroups := persistence.NewMemoryScimRepositories()
	svc, err := app.NewScimService(app.ScimServiceConfig{
		Accounts:   f.accounts,
		Identities: persistence.NewMemoryOAuthIdentityRepository(),
		Groups:     scimGroups,
		Tokens:     scimTokens,
		Sessions:   f.sessions,
		IDs:        f.ids,
		Clock:      f.clock,
		Service:    "identity-test",
		Provider:   "okta",
		PageSize:   20,
		TokenGrace: time.Hour,
	})
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	handler, err = NewHandler(HandlerConfig{
		Auth:    f.auth,
		Channel: f.channel,
		SCIM:    svc,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return f, handler
}

// scimTokenList 是令牌列表响应的 data 段。
type scimTokenList struct {
	Items []scimTokenDTO `json:"items"`
	Total int            `json:"total"`
}

// listSCIMTokens 调用列表端点并解码。
func listSCIMTokens(t *testing.T, f *e2eFixture, handler *Handler, principal *auth.Principal) scimTokenList {
	t.Helper()
	recorder := f.invoke(handler.ListSCIMTokens, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/scim/tokens",
		principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	var payload scimTokenList
	decodeData(t, recorder, &payload)
	return payload
}

// TestSCIMAdminEndpoints 覆盖 SCIM 管理端点的正常与未装配分支。
func TestSCIMAdminEndpoints(t *testing.T) {
	disabled := newE2EFixture(t, domain.ChannelAdmin)
	if disabled.handler.SCIMEnabled() {
		t.Fatal("scim must be disabled without wiring")
	}

	t.Run("not_configured", func(t *testing.T) {
		cases := []struct {
			name   string
			fn     http.HandlerFunc
			target string
			method string
		}{
			{name: "tokens", fn: disabled.handler.ListSCIMTokens, target: "/admin/v1/auth/scim/tokens", method: http.MethodGet},
			{name: "create", fn: disabled.handler.CreateSCIMToken, target: "/admin/v1/auth/scim/tokens", method: http.MethodPost},
			{name: "revoke", fn: disabled.handler.RevokeSCIMToken, target: "/admin/v1/auth/scim/tokens/x", method: http.MethodDelete},
			{name: "groups", fn: disabled.handler.ListSCIMGroups, target: "/admin/v1/auth/scim/groups", method: http.MethodGet},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				recorder := disabled.invoke(tc.fn, e2eCall{
					method: tc.method, target: tc.target,
					pathValues: map[string]string{"id": "x"},
				})
				requireStatus(t, recorder, http.StatusNotFound)
				requireErrorCode(t, recorder, apperr.CodeNotFound)
			})
		}
	})

	f, handler := newSCIMHandlerFixture(t)
	if !handler.SCIMEnabled() {
		t.Fatal("scim must be enabled after wiring")
	}
	principal := f.principal("acct-admin", "sess-admin")

	if listed := listSCIMTokens(t, f, handler, principal); listed.Total != 0 {
		t.Fatalf("initial tokens = %+v", listed)
	}

	t.Run("create_without_actor", func(t *testing.T) {
		recorder := f.invoke(handler.CreateSCIMToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/scim/tokens",
		})
		requireStatus(t, recorder, http.StatusOK)
		requireHeader(t, recorder, "Cache-Control", "no-store")
		var payload struct {
			ID          string `json:"id"`
			Provider    string `json:"provider"`
			Fingerprint string `json:"fingerprint"`
			Status      string `json:"status"`
			CreatedBy   string `json:"created_by"`
			Token       string `json:"token"`
		}
		decodeData(t, recorder, &payload)
		if payload.Token == "" || payload.Fingerprint == "" || payload.Provider != "okta" {
			t.Fatalf("create payload = %+v", payload)
		}
		if payload.CreatedBy != "" {
			t.Fatalf("created_by = %q, want empty without principal", payload.CreatedBy)
		}
	})

	t.Run("create_with_actor_and_list", func(t *testing.T) {
		recorder := f.invoke(handler.CreateSCIMToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/scim/tokens",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		decodeData(t, recorder, &payload)
		if payload.Token == "" {
			t.Fatalf("create payload = %+v", payload)
		}

		listed := listSCIMTokens(t, f, handler, principal)
		if listed.Total == 0 || len(listed.Items) != listed.Total {
			t.Fatalf("token list = %+v", listed)
		}
		var found bool
		for _, item := range listed.Items {
			if item.ID == payload.ID {
				found = true
				if item.Status != string(domain.ScimTokenActive) {
					t.Fatalf("token status = %s", item.Status)
				}
				if item.CreatedBy != "acct-admin" {
					t.Fatalf("created_by = %q, want acct-admin", item.CreatedBy)
				}
				if item.ExpiresAt != "" || item.RevokedAt != "" {
					t.Fatalf("active token must not carry expiry/revoked_at: %+v", item)
				}
			}
		}
		if !found {
			t.Fatalf("created token %s not listed: %+v", payload.ID, listed)
		}
	})

	// 再签发两个令牌，触发最旧令牌进入 retiring 宽限（expires_at 非空）。
	for i := 0; i < 2; i++ {
		recorder := f.invoke(handler.CreateSCIMToken, e2eCall{
			method: http.MethodPost, target: "/admin/v1/auth/scim/tokens",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusOK)
	}

	listed := listSCIMTokens(t, f, handler, principal)
	var retiring *scimTokenDTO
	for i := range listed.Items {
		if listed.Items[i].Status == string(domain.ScimTokenRetiring) {
			retiring = &listed.Items[i]
		}
	}
	if retiring == nil || retiring.ExpiresAt == "" {
		t.Fatalf("retiring token with expires_at expected: %+v", listed.Items)
	}

	t.Run("revoke_token", func(t *testing.T) {
		var activeID string
		for _, item := range listed.Items {
			if item.Status == string(domain.ScimTokenActive) {
				activeID = item.ID
				break
			}
		}
		if activeID == "" {
			t.Fatalf("no active token to revoke: %+v", listed.Items)
		}

		recorder := f.invoke(handler.RevokeSCIMToken, e2eCall{
			method: http.MethodDelete, target: "/admin/v1/auth/scim/tokens/" + activeID,
			principal:  principal,
			pathValues: map[string]string{"id": activeID},
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload struct {
			Revoked bool `json:"revoked"`
		}
		decodeData(t, recorder, &payload)
		if !payload.Revoked {
			t.Fatal("revoke response must report revoked=true")
		}

		relisted := listSCIMTokens(t, f, handler, principal)
		var revoked *scimTokenDTO
		for i := range relisted.Items {
			if relisted.Items[i].ID == activeID {
				revoked = &relisted.Items[i]
			}
		}
		if revoked == nil || revoked.Status != string(domain.ScimTokenRevoked) || revoked.RevokedAt == "" {
			t.Fatalf("revoked token = %+v", revoked)
		}
	})

	t.Run("list_groups", func(t *testing.T) {
		recorder := f.invoke(handler.ListSCIMGroups, e2eCall{
			method: http.MethodGet, target: "/admin/v1/auth/scim/groups",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload struct {
			Items []scimGroupDTO `json:"items"`
			Total int            `json:"total"`
		}
		decodeData(t, recorder, &payload)
		if payload.Total != 0 || len(payload.Items) != 0 {
			t.Fatalf("groups payload = %+v", payload)
		}
	})

	t.Run("list_groups_invalid_pagination", func(t *testing.T) {
		recorder := f.invoke(handler.ListSCIMGroups, e2eCall{
			method: http.MethodGet, target: "/admin/v1/auth/scim/groups?page_size=101",
			principal: principal,
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, apperr.CodeBadRequest)
	})
}
