package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestCreateMaterializesPlanQuotas 验证开账物化：默认套餐的配额落到 tenant_quota，
// 租户记录 plan_id，审计含套餐编码。
func TestCreateMaterializesPlanQuotas(t *testing.T) {
	service, emitter, repo := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Acme", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.PlanID != "plan-test" {
		t.Fatalf("PlanID = %q, want plan-test", created.PlanID)
	}
	quotas, err := repo.ListQuotas(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListQuotas() error = %v", err)
	}
	if len(quotas) != 2 ||
		quotas[0].Resource != "api.rps" || quotas[0].Limit != 100 ||
		quotas[1].Resource != "user" || quotas[1].Limit != 50 {
		t.Fatalf("物化配额 = %+v", quotas)
	}
	last, ok := emitter.last()
	if !ok || last.Attrs["plan"] != "standard" {
		t.Fatalf("审计 attrs = %+v, want plan=standard", last.Attrs)
	}
}

// TestCreateWithExplicitPlanCode 验证显式套餐编码与未知编码的 404。
func TestCreateWithExplicitPlanCode(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Beta", PlanCode: "standard", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create(standard) error = %v", err)
	}
	if created.PlanID != "plan-test" {
		t.Fatalf("PlanID = %q", created.PlanID)
	}

	_, err = service.Create(ctx, app.CreateInput{Name: "Gamma", PlanCode: "nope", Actor: adminActor()})
	if !isNotFound(err) {
		t.Fatalf("未知套餐 error = %v, want 404", err)
	}
}

// TestCreateSeedsOwner 验证 owner_subject_id 直落首名 owner 并写审计。
func TestCreateSeedsOwner(t *testing.T) {
	service, emitter, repo := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{
		Name: "Delta", OwnerSubjectID: " boss@biz ", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	member, err := repo.GetMember(ctx, created.ID, "boss@biz")
	if err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}
	if member.Role != domain.MemberRoleOwner || !member.Active() {
		t.Fatalf("种子 owner = %+v", member)
	}
	// 事务内审计顺序：member.add（种子 owner）在前，tenant.create 在后。
	actions := emitter.actions()
	if len(actions) != 2 || actions[0] != app.ActionMemberAdd || actions[1] != app.ActionCreate {
		t.Fatalf("审计动作 = %v", actions)
	}
	last, _ := emitter.last()
	if last.Attrs["plan"] != "standard" {
		t.Fatalf("create attrs = %v", last.Attrs)
	}
}

// TestActivateRequiresOwner 验证开通前置（T-12）：0 owner 的租户不能开通，
// 补 owner 后成功；该前置只作用于 pending → active。
func TestActivateRequiresOwner(t *testing.T) {
	service, _, repo := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Epsilon", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = service.Transition(ctx, app.TransitionInput{ID: created.ID, Target: domain.StatusActive, Actor: adminActor()})
	if !isConflict(err) {
		t.Fatalf("0 owner 开通 error = %v, want 409", err)
	}
	stored, getErr := repo.Get(ctx, created.ID)
	if getErr != nil || stored.Status != domain.StatusPending {
		t.Fatalf("失败开通后状态 = %v/%v, want pending", stored.Status, getErr)
	}

	if err := repo.UpsertMember(ctx, mustMember(t, created.ID, "boss@biz")); err != nil {
		t.Fatalf("seed owner error = %v", err)
	}
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("补 owner 后开通 error = %v", err)
	}
}

// isNotFound 断言 404。
func isNotFound(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeNotFound
}
