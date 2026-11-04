package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestApprovalGatesActivation 是审核流的核心验收：
// 未提交、已提交未裁决、自审通过都不能开通；通过后才允许开通。
//
// 先补 owner 再测：否则 409 可能来自「0 owner」，测不出审批前置是否真的生效
// （这是本用例第一版漏掉的假阳性）。
func TestApprovalGatesActivation(t *testing.T) {
	service, emitter, repo := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "审核流租户", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if seedErr := repo.UpsertMember(ctx, mustMember(t, created.ID, "owner-1")); seedErr != nil {
		t.Fatalf("seed owner error = %v", seedErr)
	}

	// 0) 初始为 draft。
	view, err := service.GetApproval(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetApproval() error = %v", err)
	}
	if view.Status != domain.ApprovalStatusDraft || view.Latest.Action != "" {
		t.Fatalf("初始审批态 = %+v, want draft/latest 空", view)
	}

	// 1) 未提交直接开通 → 409，且拒绝原因必须是审批（不是 owner）。
	_, err = service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	assertConflictMessage(t, err, "approved application")

	// 2) 提交后仍未裁决 → 409（同样来自审批）。
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Note: "营业执照与联系人已附", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SubmitApproval() error = %v", err)
	}
	_, err = service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	assertConflictMessage(t, err, "approved application")

	// 3) 提交人自审 → 409，且留一条失败审计。
	_, err = service.ApproveApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Actor: adminActor(),
	})
	assertCode(t, err, apperr.CodeConflict)
	if !hasAction(emitter.actions(), app.ActionApprovalApprove) {
		t.Fatalf("自审被拒也必须留审计, actions = %v", emitter.actions())
	}

	// 4) 他人通过后开通成功（owner 已就位）。
	if _, err := service.ApproveApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Note: "资质齐全", Actor: approverActor(),
	}); err != nil {
		t.Fatalf("ApproveApproval() error = %v", err)
	}
	activated, err := service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("approved + owner 后开通 error = %v", err)
	}
	if activated.Status != domain.StatusActive {
		t.Fatalf("开通后状态 = %s, want active", activated.Status)
	}
}

// assertConflictMessage 断言 409 且消息包含指定片段。
func assertConflictMessage(t *testing.T, err error, want string) {
	t.Helper()
	assertCode(t, err, apperr.CodeConflict)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want message containing %q", err, want)
	}
}

// TestRejectRequiresNoteAndAllowsResubmit 覆盖驳回原因必填、驳回后可重提且历史保留。
func TestRejectRequiresNoteAndAllowsResubmit(t *testing.T) {
	service, emitter, _ := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Rejectable", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Note: "首次提交", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SubmitApproval() error = %v", err)
	}

	// 驳回不带原因 → 400，且状态仍是 submitted。
	_, err = service.RejectApproval(ctx, app.ApprovalInput{TenantID: created.ID, Actor: approverActor()})
	assertCode(t, err, apperr.CodeBadRequest)

	rejected, err := service.RejectApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Note: "缺少法人证件", Actor: approverActor(),
	})
	if err != nil {
		t.Fatalf("RejectApproval() error = %v", err)
	}
	if rejected.Status != domain.ApprovalStatusRejected {
		t.Fatalf("驳回后状态 = %s, want rejected", rejected.Status)
	}

	// 驳回后重提：允许，且历史保留上一轮（3 条）。
	resubmitted, err := service.SubmitApproval(ctx, app.ApprovalInput{
		TenantID: created.ID, Note: "已补法人证件", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("重新提交 error = %v", err)
	}
	if resubmitted.Status != domain.ApprovalStatusSubmitted {
		t.Fatalf("重提后状态 = %s, want submitted", resubmitted.Status)
	}
	if len(resubmitted.History) != 3 {
		t.Fatalf("历史事件数 = %d, want 3（submit/reject/submit）", len(resubmitted.History))
	}

	actions := emitter.actions()
	for _, want := range []string{app.ActionApprovalSubmit, app.ActionApprovalReject} {
		if !hasAction(actions, want) {
			t.Fatalf("缺少审计动作 %s, actions = %v", want, actions)
		}
	}
}

// TestApprovalDuplicateAndTerminated 覆盖重复提交与终态租户。
func TestApprovalDuplicateAndTerminated(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Dup", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{TenantID: created.ID, Actor: adminActor()}); err != nil {
		t.Fatalf("SubmitApproval() error = %v", err)
	}
	_, err = service.SubmitApproval(ctx, app.ApprovalInput{TenantID: created.ID, Actor: adminActor()})
	assertCode(t, err, apperr.CodeConflict)

	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusTerminated, Reason: "客户放弃入驻", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	_, err = service.SubmitApproval(ctx, app.ApprovalInput{TenantID: created.ID, Actor: adminActor()})
	assertCode(t, err, apperr.CodeConflict)
}

// TestApprovalNotFound 覆盖不存在的租户（404）。
func TestApprovalNotFound(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	if _, err := service.GetApproval(ctx, "missing-tenant"); !isNotFound(err) {
		t.Fatalf("GetApproval(missing) error = %v, want 404", err)
	}
}

// hasAction 判断动作是否出现过。
func hasAction(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}
