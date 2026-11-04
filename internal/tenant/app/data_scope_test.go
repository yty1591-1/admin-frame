package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// isForbidden 判定错误是否为 40300（越权统一语义）。
func isForbidden(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeForbidden
}

// TestDataScopeBlocksCrossTenantWrite ENT-12：主体数据范围为 merchant_set
// 时，集合外租户的全部写用例（经 mutableTenant 汇聚点）必须 403。
func TestDataScopeBlocksCrossTenantWrite(t *testing.T) {
	fixture := newNotifyingInvitationFixture(t, nil)
	tenant := fixture.tenant

	// 运营专员：merchant_set 只含其他租户。
	outOfScope := authz.WithSnapshot(context.Background(), authz.Snapshot{
		Channel: "admin", SubjectID: "ops-1", MerchantID: "m-other",
		Scopes: map[string]authz.Scope{
			"tenant": {Type: authz.ScopeMerchantSet, MerchantIDs: []string{"m-other"}},
		},
	})

	if _, _, err := fixture.invitations.Create(outOfScope, app.CreateInvitationInput{
		TenantID: tenant.ID, Email: "bob@acme.com", Role: domain.MemberRoleMember,
	}); !isForbidden(err) {
		t.Fatalf("cross-tenant invitation must be 40300, got %v", err)
	}
	if _, err := fixture.members.Add(outOfScope, app.AddMemberInput{
		TenantID: tenant.ID, SubjectID: "u-9", Role: domain.MemberRoleMember,
	}); !isForbidden(err) {
		t.Fatalf("cross-tenant member add must be 40300, got %v", err)
	}
	if _, err := fixture.quotas.Set(outOfScope, app.SetQuotaInput{
		TenantID: tenant.ID, Resource: "user", Limit: 10,
	}); !isForbidden(err) {
		t.Fatalf("cross-tenant quota set must be 40300, got %v", err)
	}
}

// TestDataScopeBlocksCrossTenantRead ENT-12：越权读与越权写同样拒绝
// （SEC-14：禁止只靠前端隐藏入口）。
func TestDataScopeBlocksCrossTenantRead(t *testing.T) {
	fixture := newNotifyingInvitationFixture(t, nil)
	tenant := fixture.tenant

	outOfScope := authz.WithSnapshot(context.Background(), authz.Snapshot{
		Channel: "admin", SubjectID: "ops-1", MerchantID: "m-other",
		Scopes: map[string]authz.Scope{
			"tenant": {Type: authz.ScopeMerchant, MerchantIDs: []string{"m-elsewhere"}},
		},
	})

	if _, _, err := fixture.members.List(outOfScope, tenant.ID, false, 20, 0); !isForbidden(err) {
		t.Fatalf("cross-tenant member list must be 40300, got %v", err)
	}
	if _, err := fixture.quotas.List(outOfScope, tenant.ID); !isForbidden(err) {
		t.Fatalf("cross-tenant quota list must be 40300, got %v", err)
	}
	if _, _, err := fixture.invitations.List(outOfScope, tenant.ID, "", 20, 0); !isForbidden(err) {
		t.Fatalf("cross-tenant invitation list must be 40300, got %v", err)
	}
}

// TestDataScopeAllowsInScopeTenant ENT-12：范围内主体（merchant_set 命中）
// 的读写不受影响；快照缺失（未启用 iam 部署形态）维持放行。
func TestDataScopeAllowsInScopeTenant(t *testing.T) {
	fixture := newNotifyingInvitationFixture(t, nil)
	tenant := fixture.tenant

	inScope := authz.WithSnapshot(context.Background(), authz.Snapshot{
		Channel: "admin", SubjectID: "ops-1", MerchantID: "m-1",
		Scopes: map[string]authz.Scope{
			"tenant": {Type: authz.ScopeMerchantSet, MerchantIDs: []string{tenant.ID}},
		},
	})

	invitation, token, err := fixture.invitations.Create(inScope, app.CreateInvitationInput{
		TenantID: tenant.ID, Email: "bob@acme.com", Role: domain.MemberRoleMember,
	})
	if err != nil || invitation.ID == "" || token == "" {
		t.Fatalf("in-scope invitation must pass: err=%v id=%q", err, invitation.ID)
	}
	if _, _, err := fixture.members.List(inScope, tenant.ID, false, 20, 0); err != nil {
		t.Fatalf("in-scope member list must pass: %v", err)
	}

	// 快照缺失：放行（RequirePermission 层挡功能权限）。
	if _, _, err := fixture.members.List(context.Background(), tenant.ID, false, 20, 0); err != nil {
		t.Fatalf("missing snapshot must pass through: %v", err)
	}
}
