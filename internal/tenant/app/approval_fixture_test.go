package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
)

// approverActor 是审批人：与 adminActor 不同主体，满足职责分离。
func approverActor() app.Actor {
	return app.Actor{SubjectID: "u-approver", ActorType: "user", Channel: "admin", TraceID: "trace-approval"}
}

// approveForTest 为租户补齐「提交 → 通过」的审批链。
//
// ENT-09 审核流把审批设为开通前置：任何 pending → active 的用例都必须先走
// 这一步，否则拿到的是「未审批」而不是它真正想验证的错误（owner / 并发 / 审计）。
func approveForTest(t *testing.T, service *app.Service, tenantID string) {
	t.Helper()
	if service == nil {
		t.Fatal("approveForTest requires a tenant service")
	}
	ctx := context.Background()
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{
		TenantID: tenantID, Note: "fixture application", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("submit approval error = %v", err)
	}
	if _, err := service.ApproveApproval(ctx, app.ApprovalInput{
		TenantID: tenantID, Note: "fixture approval", Actor: approverActor(),
	}); err != nil {
		t.Fatalf("approve error = %v", err)
	}
}
