package tenant_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// TestApprovalPersistsAndGatesActivation 在真实 MySQL 上验证：
//   - 审批事件 append-only 落库，倒序读取顺序稳定；
//   - 未审批不能开通；审批通过后（无 owner 前置时不注入成员仓储）可开通；
//   - 驳回后重提，历史事件全部保留。
func TestApprovalPersistsAndGatesActivation(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{
		Service: "admin-api", Approvals: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tenantID := uniqueID(t)
	tenant, err := domain.NewTenant(tenantID, "approval-integration", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		// 审批行随外键级联删除，无需单独清理。
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", tenantID)
	})

	adminActor := app.Actor{SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace"}
	approver := app.Actor{SubjectID: "it-approver", ActorType: "user", Channel: "admin", TraceID: "it-trace"}

	// 1) 未提交直接开通 → 冲突。
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusActive, Actor: adminActor,
	}); err == nil {
		t.Fatal("未审批开通应被拒绝")
	}

	// 2) 提交 → 驳回（带原因）→ 重提 → 通过。
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{TenantID: tenantID, Note: "首次提交", Actor: adminActor}); err != nil {
		t.Fatalf("SubmitApproval() error = %v", err)
	}
	if _, err := service.RejectApproval(ctx, app.ApprovalInput{TenantID: tenantID, Note: "缺少资质", Actor: approver}); err != nil {
		t.Fatalf("RejectApproval() error = %v", err)
	}
	if _, err := service.SubmitApproval(ctx, app.ApprovalInput{TenantID: tenantID, Note: "已补资质", Actor: adminActor}); err != nil {
		t.Fatalf("重新提交 error = %v", err)
	}
	view, err := service.ApproveApproval(ctx, app.ApprovalInput{TenantID: tenantID, Note: "通过", Actor: approver})
	if err != nil {
		t.Fatalf("ApproveApproval() error = %v", err)
	}
	if view.Status != domain.ApprovalStatusApproved {
		t.Fatalf("审批态 = %s, want approved", view.Status)
	}
	if len(view.History) != 4 {
		t.Fatalf("事件数 = %d, want 4（submit/reject/submit/approve）", len(view.History))
	}

	// 3) 仓储层顺序：倒序返回，最新一条是 approve。
	events, err := repo.ListRecentApprovals(ctx, tenantID, 10)
	if err != nil {
		t.Fatalf("ListRecentApprovals() error = %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("事件数 = %d, want 4", len(events))
	}
	if events[0].Action != domain.ApprovalActionApprove {
		t.Fatalf("最新事件 = %s, want approve", events[0].Action)
	}
	if events[0].ID <= events[1].ID {
		t.Fatalf("倒序读取失败：id %d 应大于 %d", events[0].ID, events[1].ID)
	}
	if events[3].Action != domain.ApprovalActionSubmit || events[3].Note != "首次提交" {
		t.Fatalf("最早事件 = %+v, want 首次提交", events[3])
	}

	// 4) 审批通过后可开通（本用例未注入成员仓储，owner 前置不参与）。
	activated, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusActive, Actor: adminActor,
	})
	if err != nil {
		t.Fatalf("审批通过后开通 error = %v", err)
	}
	if activated.Status != domain.StatusActive {
		t.Fatalf("状态 = %s, want active", activated.Status)
	}
}

// TestApprovalForeignKeyGuardsUnknownTenant 验证给不存在的租户写审批被拒。
func TestApprovalForeignKeyGuardsUnknownTenant(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	event, err := domain.NewApproval("it-missing-"+uniqueID(t), domain.ApprovalActionSubmit, "u1", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewApproval() error = %v", err)
	}
	if err := repo.AppendApproval(ctx, event); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AppendApproval(unknown) error = %v, want ErrNotFound", err)
	}
}
