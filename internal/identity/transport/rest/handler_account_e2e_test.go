package rest

import (
	"context"
	"net/http"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestE2EAccountStatusOperations 覆盖停用 / 启用 / 锁定 / 解锁与幂等语义。
func TestE2EAccountStatusOperations(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd12")
	principal := f.principal("acct-operator", "")
	pathValues := map[string]string{"id": "acct-target"}

	recorder := f.invoke(f.handler.DisableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/disable",
		principal: principal, pathValues: pathValues, body: `{"reason":"incident"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	if status := f.reload(t, "acct-target").Status; status != domain.AccountStatusDisabled {
		t.Fatalf("status = %q, want disabled", status)
	}

	// 已停用账号幂等返回 204。
	recorder = f.invoke(f.handler.DisableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/disable",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.EnableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/enable",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	if status := f.reload(t, "acct-target").Status; status != domain.AccountStatusActive {
		t.Fatalf("status = %q, want active", status)
	}

	// 已启用账号再次启用按幂等返回 204。
	recorder = f.invoke(f.handler.EnableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/enable",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.LockAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/lock",
		principal: principal, pathValues: pathValues,
		body: `{"reason":"brute-force","duration_minutes":30}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	account := f.reload(t, "acct-target")
	if account.Status != domain.AccountStatusLocked || account.LockedUntil == nil {
		t.Fatalf("account = %+v, want locked with LockedUntil", account)
	}

	// 锁定中再次锁定幂等返回 204。
	recorder = f.invoke(f.handler.LockAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/lock",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.UnlockAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/unlock",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	if status := f.reload(t, "acct-target").Status; status != domain.AccountStatusActive {
		t.Fatalf("status = %q, want active", status)
	}

	// 未锁定账号解锁同样幂等返回 204。
	recorder = f.invoke(f.handler.UnlockAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/unlock",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	// 目标缺失与缺少鉴权。
	recorder = f.invoke(f.handler.DisableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/missing/disable",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.DisableAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/disable",
		pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EAccountPasswordAndDetail 覆盖密码重置、账号详情与读取审计。
func TestE2EAccountPasswordAndDetail(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd12")
	principal := f.principal("acct-operator", "")
	pathValues := map[string]string{"id": "acct-target"}

	recorder := f.invoke(f.handler.ResetAccountPassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/password/reset",
		principal: principal, pathValues: pathValues, body: `{"reason":"lost-password"}`,
	})
	requireStatus(t, recorder, http.StatusOK)
	var reset resetPasswordResponse
	decodeData(t, recorder, &reset)
	if reset.TemporaryPassword == "" {
		t.Fatal("temporary password must be returned once")
	}

	recorder = f.invoke(f.handler.GetAccount, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusOK)
	var detail accountDetailDTO
	decodeData(t, recorder, &detail)
	if detail.ID != "acct-target" || !detail.HasPassword || !detail.MustChangePassword {
		t.Fatalf("account detail = %+v", detail)
	}

	// 目标缺失、请求体非法与缺少鉴权。
	recorder = f.invoke(f.handler.GetAccount, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/missing",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.ResetAccountPassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/missing/password/reset",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.ResetAccountPassword, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/password/reset",
		principal: principal, pathValues: pathValues, body: `{"reason":`,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.GetAccount, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target", pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EAccountDeleteAndRestore 覆盖软删、详情可查与恢复。
func TestE2EAccountDeleteAndRestore(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd12")
	principal := f.principal("acct-operator", "")
	pathValues := map[string]string{"id": "acct-target"}

	recorder := f.invoke(f.handler.DeleteAccount, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/accounts/acct-target",
		principal: principal, pathValues: pathValues, body: `{"reason":"gdpr"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	deleted := f.reload(t, "acct-target")
	if deleted.DeletedAt == nil {
		t.Fatalf("account must be soft deleted: %+v", deleted)
	}

	// 软删账号仍可通过详情查询，便于恢复。
	recorder = f.invoke(f.handler.GetAccount, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusOK)

	recorder = f.invoke(f.handler.RestoreAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/restore",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	restored := f.reload(t, "acct-target")
	if restored.DeletedAt != nil || restored.Status != domain.AccountStatusActive {
		t.Fatalf("restored account = %+v", restored)
	}

	// 未删除账号恢复同样幂等返回 204。
	recorder = f.invoke(f.handler.RestoreAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/restore",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.DeleteAccount, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/accounts/missing",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.RestoreAccount, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/missing/restore",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)
}

// TestE2EAccountResetMFA 覆盖管理员重置 MFA 绑定。
func TestE2EAccountResetMFA(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd12")
	f.bindMFA(t, "acct-target")
	principal := f.principal("acct-operator", "")
	pathValues := map[string]string{"id": "acct-target"}

	recorder := f.invoke(f.handler.ResetAccountMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/mfa/reset",
		principal: principal, pathValues: pathValues, body: `{"reason":"device-lost"}`,
	})
	requireStatus(t, recorder, http.StatusNoContent)
	if _, err := f.mfa.FindByAccount(context.Background(), "acct-target"); err == nil {
		t.Fatal("mfa enrollment must be removed")
	}

	// 未绑定账号重置返回 40400。
	recorder = f.invoke(f.handler.ResetAccountMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/mfa/reset",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.ResetAccountMFA, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/missing/mfa/reset",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)
}

// TestE2EListAccounts 覆盖账号列表过滤、分页与参数校验。
func TestE2EListAccounts(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-alpha", "alpha@example.com", "Alpha#Passw0rd12")
	f.seedAccount(t, "acct-bravo", "bravo@example.com", "Bravo#Passw0rd12")
	f.seedAccount(t, "acct-charlie", "charlie@example.com", "Charlie#Passw0rd12")
	principal := f.principal("acct-alpha", "")

	recorder := f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?page=1&page_size=10",
		principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	var list accountListResponse
	decodeData(t, recorder, &list)
	if list.Total != 3 || len(list.Items) != 3 {
		t.Fatalf("account list = %+v", list)
	}

	// 关键词与主体精确过滤。
	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?keyword=bravo", principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	decodeData(t, recorder, &list)
	if list.Total != 1 || list.Items[0].Subject != "bravo@example.com" {
		t.Fatalf("keyword filter list = %+v", list)
	}

	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?subject=charlie@example.com",
		principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)
	decodeData(t, recorder, &list)
	if list.Total != 1 || list.Items[0].ID != "acct-charlie" {
		t.Fatalf("subject filter list = %+v", list)
	}

	// 状态过滤命中 pending。
	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?status=pending", principal: principal,
	})
	requireStatus(t, recorder, http.StatusOK)

	// 非法状态、超限分页与缺少鉴权。
	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?status=bogus", principal: principal,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts?page_size=101", principal: principal,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.ListAccounts, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts",
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EAccountSessions 覆盖管理员视角的设备列表、单设备撤销与全量撤销。
func TestE2EAccountSessions(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd12")
	principal := f.principal("acct-operator", "")
	f.login(t, "target", "Target#Passw0rd12")
	f.login(t, "target", "Target#Passw0rd12")
	pathValues := map[string]string{"id": "acct-target"}

	recorder := f.invoke(f.handler.ListAccountSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target/sessions",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusOK)
	var page sessionListResponse
	decodeData(t, recorder, &page)
	if len(page.Items) != 2 {
		t.Fatalf("target sessions = %d, want 2", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Current {
			t.Fatal("admin view must not mark any session as current")
		}
	}

	recorder = f.invoke(f.handler.RevokeAccountSession, e2eCall{
		method:    http.MethodDelete,
		target:    "/admin/v1/auth/accounts/acct-target/sessions/" + page.Items[0].ID,
		principal: principal, pathValues: map[string]string{"id": "acct-target", "sid": page.Items[0].ID},
	})
	requireStatus(t, recorder, http.StatusNoContent)

	recorder = f.invoke(f.handler.RevokeAccountSession, e2eCall{
		method: http.MethodDelete, target: "/admin/v1/auth/accounts/acct-target/sessions/missing",
		principal: principal, pathValues: map[string]string{"id": "acct-target", "sid": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.RevokeAccountSessions, e2eCall{
		method: http.MethodPost, target: "/admin/v1/auth/accounts/acct-target/sessions/revoke-all",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusNoContent)

	// 分页参数非法与目标缺失。
	recorder = f.invoke(f.handler.ListAccountSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target/sessions?limit=0",
		principal: principal, pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.invoke(f.handler.ListAccountSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/missing/sessions",
		principal: principal, pathValues: map[string]string{"id": "missing"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)

	recorder = f.invoke(f.handler.ListAccountSessions, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-target/sessions", pathValues: pathValues,
	})
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2EAccountOperationCrossChannel 覆盖跨端目标不可见。
func TestE2EAccountOperationCrossChannel(t *testing.T) {
	f := newE2EFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-operator", "operator", "Operator#Passw0rd12")
	bizAccount, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-biz", Channel: domain.ChannelBiz, Subject: "biz@example.com",
		MerchantID: "merchant-1", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new biz account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), bizAccount, 0); err != nil {
		t.Fatalf("save biz account: %v", err)
	}
	principal := f.principal("acct-operator", "")

	recorder := f.invoke(f.handler.GetAccount, e2eCall{
		method: http.MethodGet, target: "/admin/v1/auth/accounts/acct-biz",
		principal: principal, pathValues: map[string]string{"id": "acct-biz"},
	})
	requireStatus(t, recorder, http.StatusNotFound)
	requireErrorCode(t, recorder, apperr.CodeNotFound)
}
