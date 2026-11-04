package app_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// fixedNow 固定时钟，避免用例依赖真实时间。
var fixedNow = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

// seqIDs 是测试用 ID 生成器：固定前缀 + 递增序号。
type seqIDs struct {
	mu   sync.Mutex
	next int
}

func (g *seqIDs) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return int64(g.next), nil
}

func (g *seqIDs) NextString() (string, error) {
	id, err := g.Next()
	if err != nil {
		return "", err
	}
	return "t" + strconv.FormatInt(id, 10), nil
}

// recordEmitter 记录审计事件，供断言动作、结果与原因。
type recordEmitter struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (e *recordEmitter) Emit(_ context.Context, event audit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
	return e.err
}

func (e *recordEmitter) last() (audit.Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return audit.Event{}, false
	}
	return e.events[len(e.events)-1], true
}

func (e *recordEmitter) actions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, event := range e.events {
		out = append(out, event.Action)
	}
	return out
}

// newService 构造带内存仓储与记录型审计的用例对象。
func newService(t *testing.T) (*app.Service, *recordEmitter, *persistence.MemoryRepository) {
	t.Helper()
	emitter := &recordEmitter{}
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	service, err := app.NewService(repo, &seqIDs{}, emitter, testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, emitter, repo
}

// seedPlan 向仓储写入默认套餐；返回编码供断言使用。
func seedPlan(t *testing.T, repo *persistence.MemoryRepository) string {
	t.Helper()
	plan := domain.Plan{
		ID: "plan-test", Code: "standard", Name: "标准套餐", IsDefault: true,
		Quotas: []domain.Quota{
			{Resource: "user", Limit: 50},
			{Resource: "api.rps", Limit: 100},
		},
	}
	now := time.Now().UTC()
	plan.CreatedAt = now
	plan.UpdatedAt = now
	if err := repo.UpsertPlan(plan); err != nil {
		t.Fatalf("UpsertPlan() error = %v", err)
	}
	return plan.Code
}

// testPlanOptions 返回接好三仓储的 Options（套餐 / 成员 / 配额）。
func testPlanOptions(repo *persistence.MemoryRepository) app.Options {
	return app.Options{Service: "admin-api", Plans: repo, Members: repo, Quotas: repo}
}

// adminActor 是测试用操作者。
func adminActor() app.Actor {
	return app.Actor{SubjectID: "u1", ActorType: "user", Channel: "admin", TraceID: "trace-1"}
}

// TestCreateThenFullLifecycle 走完整生命周期并校验审计动作序列。
func TestCreateThenFullLifecycle(t *testing.T) {
	service, emitter, repo := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "  Acme  ", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != domain.StatusPending {
		t.Fatalf("Status = %s, want pending", created.Status)
	}
	if created.Name != "Acme" {
		t.Fatalf("Name = %q, want trimmed", created.Name)
	}

	steps := []struct {
		target domain.Status
		reason string
	}{
		{domain.StatusActive, ""},
		{domain.StatusSuspended, "逾期未付款"},
		{domain.StatusActive, ""},
		{domain.StatusTerminated, "客户退租"},
	}
	// T-12 开通前置：激活前先补一名在册 owner。
	if err := repo.UpsertMember(ctx, mustMember(t, created.ID, "owner-1")); err != nil {
		t.Fatalf("seed owner error = %v", err)
	}

	tenant := created
	for _, step := range steps {
		tenant, err = service.Transition(ctx, app.TransitionInput{
			ID: tenant.ID, Target: step.target, Reason: step.reason, Actor: adminActor(),
		})
		if err != nil {
			t.Fatalf("Transition(%s) error = %v", step.target, err)
		}
		if tenant.Status != step.target {
			t.Fatalf("Status = %s, want %s", tenant.Status, step.target)
		}
	}
	if tenant.TerminatedAt == nil {
		t.Fatal("终止后必须记录 terminated_at")
	}
	if tenant.Version != int64(len(steps)+1) {
		t.Fatalf("Version = %d, want %d", tenant.Version, len(steps)+1)
	}

	want := []string{
		app.ActionCreate,
		app.ActionActivate,
		app.ActionSuspend,
		app.ActionResume,
		app.ActionTerminate,
	}
	got := emitter.actions()
	if len(got) != len(want) {
		t.Fatalf("审计动作数 = %d, want %d（%v）", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	last, _ := emitter.last()
	if last.Reason != "客户退租" {
		t.Fatalf("审计原因 = %q, want 客户退租", last.Reason)
	}
	if last.ResourceType != app.ResourceTypeTenant || last.ResourceID != tenant.ID {
		t.Fatalf("审计资源 = %s/%s", last.ResourceType, last.ResourceID)
	}
}

// TestInvalidTransitionIsConflictAndAudited 验证非法迁移返回 409 且留下失败审计。
func TestInvalidTransitionIsConflictAndAudited(t *testing.T) {
	service, emitter, _ := newService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreateInput{Name: "Beta", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusSuspended, Reason: "未开通直接停用", Actor: adminActor(),
	})
	assertCode(t, err, apperr.CodeConflict)

	last, ok := emitter.last()
	if !ok {
		t.Fatal("非法迁移必须留审计")
	}
	if last.Outcome != audit.OutcomeFailure {
		t.Fatalf("Outcome = %s, want failure", last.Outcome)
	}
	if last.Action != app.ActionSuspend {
		t.Fatalf("Action = %s, want tenant.suspend", last.Action)
	}

	reloaded, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if reloaded.Status != domain.StatusPending {
		t.Fatalf("非法迁移后状态 = %s, want pending", reloaded.Status)
	}
}

// TestSuspendRequiresReason 验证停用缺原因返回 400，且状态不变。
func TestSuspendRequiresReason(t *testing.T) {
	service, _, repo := newService(t)
	ctx := context.Background()
	tenant, err := service.Create(ctx, app.CreateInput{Name: "Gamma", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// T-12 开通前置：激活前先补一名 owner（建号与激活允许分两步）。
	if err := repo.UpsertMember(ctx, mustMember(t, tenant.ID, "owner-1")); err != nil {
		t.Fatalf("seed owner error = %v", err)
	}
	tenant, err = service.Transition(ctx, app.TransitionInput{ID: tenant.ID, Target: domain.StatusActive, Actor: adminActor()})
	if err != nil {
		t.Fatalf("激活 error = %v", err)
	}

	_, err = service.Transition(ctx, app.TransitionInput{
		ID: tenant.ID, Target: domain.StatusSuspended, Reason: "   ", Actor: adminActor(),
	})
	assertCode(t, err, apperr.CodeBadRequest)
}

// TestTerminatedTenantCannotResume 验证终态拒绝恢复。
func TestTerminatedTenantCannotResume(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	tenant, err := service.Create(ctx, app.CreateInput{Name: "Delta", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tenant, err = service.Transition(ctx, app.TransitionInput{
		ID: tenant.ID, Target: domain.StatusTerminated, Reason: "退租", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("终止 error = %v", err)
	}
	_, err = service.Transition(ctx, app.TransitionInput{ID: tenant.ID, Target: domain.StatusActive, Actor: adminActor()})
	assertCode(t, err, apperr.CodeConflict)
}

// TestGetMissingTenantIsNotFound 验证不存在返回 404。
func TestGetMissingTenantIsNotFound(t *testing.T) {
	service, _, _ := newService(t)
	_, err := service.Get(context.Background(), "absent")
	assertCode(t, err, apperr.CodeNotFound)
}

// TestListCapsPageSizeAndRejectsUnknownStatus 验证分页收敛与状态枚举校验。
func TestListCapsPageSizeAndRejectsUnknownStatus(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	for _, name := range []string{"A", "B", "C"} {
		if _, err := service.Create(ctx, app.CreateInput{Name: name, Actor: adminActor()}); err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
	}

	result, err := service.List(ctx, domain.Filter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if result.Total != 3 || len(result.Items) != 3 {
		t.Fatalf("Total=%d Items=%d, want 3/3", result.Total, len(result.Items))
	}

	// 超过上限时按上限收敛，而不是报错：调用方拿到的是尽力而为的一页。
	capped, err := service.List(ctx, domain.Filter{Limit: 5000})
	if err != nil {
		t.Fatalf("List(capped) error = %v", err)
	}
	if len(capped.Items) > 100 {
		t.Fatalf("Items = %d, want <= 100", len(capped.Items))
	}

	_, err = service.List(ctx, domain.Filter{Status: "frozen"})
	assertCode(t, err, apperr.CodeBadRequest)

	active, err := service.List(ctx, domain.Filter{Status: domain.StatusActive})
	if err != nil {
		t.Fatalf("List(active) error = %v", err)
	}
	if active.Total != 0 {
		t.Fatalf("active Total = %d, want 0", active.Total)
	}
}

// TestCreateRequiresEmitter 验证缺审计出口时构造失败（不允许静默无审计）。
func TestCreateRequiresEmitter(t *testing.T) {
	if _, err := app.NewService(persistence.NewMemoryRepository(), &seqIDs{}, nil, app.Options{}); err == nil {
		t.Fatal("缺审计出口应构造失败")
	}
}

// TestConcurrentStatusChangeIsConflict 验证乐观锁冲突映射为 409。
func TestConcurrentStatusChangeIsConflict(t *testing.T) {
	repo := &conflictRepo{tenant: domain.Tenant{
		ID: "t1", Name: "acme", Status: domain.StatusActive, Version: 5,
		CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}}
	service, err := app.NewService(repo, &seqIDs{}, &recordEmitter{}, app.Options{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = service.Transition(context.Background(), app.TransitionInput{
		ID: "t1", Target: domain.StatusSuspended, Reason: "欠费", Actor: adminActor(),
	})
	assertCode(t, err, apperr.CodeConflict)
}

// conflictRepo 的 UpdateStatus 恒定返回版本冲突，模拟并发写入。
type conflictRepo struct {
	tenant domain.Tenant
}

func (r *conflictRepo) Create(context.Context, domain.Tenant) error { return nil }
func (r *conflictRepo) Get(context.Context, string) (domain.Tenant, error) {
	return r.tenant, nil
}
func (r *conflictRepo) List(context.Context, domain.Filter) ([]domain.Tenant, int, error) {
	return nil, 0, nil
}
func (r *conflictRepo) UpdateProfile(context.Context, domain.Tenant) error           { return nil }
func (r *conflictRepo) CountByStatus(context.Context) (map[domain.Status]int, error) { return nil, nil }

func (r *conflictRepo) ListDueForPurge(context.Context, time.Time, int) ([]domain.Tenant, error) {
	return nil, nil
}
func (r *conflictRepo) UpdateStatus(context.Context, domain.Tenant) error {
	return domain.ErrVersionConflict
}

// assertCode 断言错误是 apperr 且业务码匹配。
func assertCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %d", want)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want *apperr.AppError", err)
	}
	if appErr.Code != want {
		t.Fatalf("code = %d, want %d (message=%s)", appErr.Code, want, appErr.Message)
	}
}

// WithinTx 在测试中直接执行 fn（无事务语义）。
func (r *conflictRepo) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// mustMember 构造在册 owner 成员（开通前置用例使用）。
func mustMember(t *testing.T, tenantID, subjectID string) domain.Member {
	t.Helper()
	member, err := domain.NewMember(tenantID, subjectID, domain.MemberRoleOwner, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}
	return member
}
