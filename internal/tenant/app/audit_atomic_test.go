package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// failingEmitter 模拟审计出口不可用。
func failingEmitter() *recordEmitter {
	return &recordEmitter{err: errors.New("audit sink down")}
}

// TestCreateRollsBackWhenAuditFails 验证审计写失败时租户不会被创建。
//
// D-01=② 的硬约束：审计可以延迟，不可以丢失；Outbox 写不进去即业务失败。
func TestCreateRollsBackWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	service, err := app.NewService(repo, &seqIDs{}, failingEmitter(), testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if _, err := service.Create(ctx, app.CreateInput{Name: "Rollback", Actor: adminActor()}); err == nil {
		t.Fatal("审计失败时 Create 应返回错误")
	}
	items, total, err := repo.List(ctx, domain.Filter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("审计失败必须回滚租户，实际 total=%d items=%d", total, len(items))
	}
}

// TestTransitionRollsBackWhenAuditFails 验证迁移成功但审计失败时状态不变。
func TestTransitionRollsBackWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryRepository()
	now := time.Now().UTC()
	tenant, err := domain.NewTenant("t-rollback", "Rollback", now)
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	service, err := app.NewService(repo, &seqIDs{}, failingEmitter(), testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenant.ID, Target: domain.StatusActive, Actor: adminActor(),
	}); err == nil {
		t.Fatal("审计失败时 Transition 应返回错误")
	}
	stored, err := repo.Get(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != domain.StatusPending {
		t.Fatalf("审计失败必须回滚状态，实际 = %s", stored.Status)
	}
}

// TestAddMemberRollsBackWhenAuditFails 验证审计失败时成员不会被写入。
func TestAddMemberRollsBackWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryRepository()
	now := time.Now().UTC()
	tenant := domain.Tenant{ID: "t-member-rollback", Name: "T", Status: domain.StatusActive,
		Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, failingEmitter(), app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}

	if _, err := members.Add(ctx, app.AddMemberInput{
		TenantID: tenant.ID, SubjectID: "u1", Role: domain.MemberRoleMember, Actor: adminActor(),
	}); err == nil {
		t.Fatal("审计失败时 Add 应返回错误")
	}
	list, err := repo.ListMembers(ctx, tenant.ID, true)
	if err != nil {
		t.Fatalf("ListMembers() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("审计失败必须回滚成员，实际 %d 条", len(list))
	}
}
