package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// stubRevoker 记录撤销调用并可注入失败。
type stubRevoker struct {
	calls    []string
	revoked  int
	failWith error
}

func (r *stubRevoker) RevokeByTenant(_ context.Context, tenantID string) (int, error) {
	if r.failWith != nil {
		return 0, r.failWith
	}
	r.calls = append(r.calls, tenantID)
	return r.revoked, nil
}

// revokerFixture 是撤销用例的最小基建：active 租户 + 注入指定撤销器的服务。
type revokerFixture struct {
	repo    *persistence.MemoryRepository
	service *app.Service
	emitter *recordEmitter
	tenant  domain.Tenant
}

func newRevokerFixture(t *testing.T, revoker app.SessionRevoker) *revokerFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	emitter := &recordEmitter{}
	service, err := app.NewService(repo, &seqIDs{}, emitter, app.Options{Service: "admin-api", Revoker: revoker})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	created, err := service.Create(context.Background(), app.CreateInput{Name: "Acme", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tenant, err := service.Transition(context.Background(), app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("activate error = %v", err)
	}
	return &revokerFixture{repo: repo, service: service, emitter: emitter, tenant: tenant}
}

// TestSuspendRevokesTenantSessions 验证停用触发会话撤销，且撤销数写进审计。
func TestSuspendRevokesTenantSessions(t *testing.T) {
	revoker := &stubRevoker{revoked: 7}
	fixture := newRevokerFixture(t, revoker)
	ctx := context.Background()

	if _, err := fixture.service.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusSuspended, Reason: "欠费", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if len(revoker.calls) != 1 || revoker.calls[0] != fixture.tenant.ID {
		t.Fatalf("撤销调用 = %v, want [%s]", revoker.calls, fixture.tenant.ID)
	}
	last, ok := fixture.emitter.last()
	if !ok || last.Attrs["revoked_sessions"] != "7" {
		t.Fatalf("审计 attrs = %v, want revoked_sessions=7", last.Attrs)
	}
}

// TestTerminateRevokesTenantSessions 验证终止同样触发撤销。
func TestTerminateRevokesTenantSessions(t *testing.T) {
	revoker := &stubRevoker{revoked: 1}
	fixture := newRevokerFixture(t, revoker)
	ctx := context.Background()

	if _, err := fixture.service.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusTerminated, Reason: "退租", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("terminate error = %v", err)
	}
	if len(revoker.calls) != 1 {
		t.Fatalf("终止应触发一次撤销，实际 %d 次", len(revoker.calls))
	}
}

// TestRevocationFailureAbortsSuspension 验证撤销失败时状态不变（D-02 核心约束）。
func TestRevocationFailureAbortsSuspension(t *testing.T) {
	revoker := &stubRevoker{failWith: errors.New("identity down")}
	fixture := newRevokerFixture(t, revoker)
	ctx := context.Background()

	_, err := fixture.service.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusSuspended, Reason: "欠费", Actor: adminActor(),
	})
	if err == nil {
		t.Fatal("撤销失败必须让停用失败")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInternal {
		t.Fatalf("error = %v, want 50000", err)
	}
	// 状态必须仍是 active：不允许「已停用、令牌还能换新」的反向出现。
	stored, getErr := fixture.repo.Get(ctx, fixture.tenant.ID)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	if stored.Status != domain.StatusActive {
		t.Fatalf("撤销失败后状态 = %s, want active", stored.Status)
	}
}

// TestActivateDoesNotRevoke 验证开通不触发撤销。
func TestActivateDoesNotRevoke(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	revoker := &stubRevoker{revoked: 0}
	service, err := app.NewService(repo, &seqIDs{}, &recordEmitter{}, app.Options{Service: "admin-api", Revoker: revoker})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	created, err := service.Create(context.Background(), app.CreateInput{Name: "Beta", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Transition(context.Background(), app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("activate error = %v", err)
	}
	if len(revoker.calls) != 0 {
		t.Fatalf("开通不应触发撤销，实际 %d 次", len(revoker.calls))
	}
}

// TestNilRevokerSuspendsAnyway 验证未配置撤销器时停用仍可用（单测 / 内存模式）。
func TestNilRevokerSuspendsAnyway(t *testing.T) {
	fixture := newRevokerFixture(t, nil)
	next, err := fixture.service.Transition(context.Background(), app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusSuspended, Reason: "维护", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if next.Status != domain.StatusSuspended {
		t.Fatalf("状态 = %s", next.Status)
	}
}
