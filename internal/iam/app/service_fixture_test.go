package app_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// seqIDs 是确定性 ID 生成器，便于断言与排障。
type seqIDs struct {
	mu sync.Mutex
	n  int64
}

func (s *seqIDs) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.n, nil
}

func (s *seqIDs) NextString() (string, error) {
	id, err := s.Next()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// capturedRecorder 记录审计事件；err 非空时模拟审计失败（fail-closed）。
type capturedRecorder struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (r *capturedRecorder) Record(_ context.Context, event audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, event)
	return nil
}

func (r *capturedRecorder) last(t *testing.T) audit.Event {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		t.Fatal("expected at least one audit event")
	}
	return r.events[len(r.events)-1]
}

func (r *capturedRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// capturedInvalidator 记录被失效的主体。
type capturedInvalidator struct {
	mu       sync.Mutex
	subjects []string
	err      error
}

func (i *capturedInvalidator) InvalidateSubject(_ context.Context, channel, merchantID, subjectType, subjectID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.err != nil {
		return i.err
	}
	i.subjects = append(i.subjects, channel+"|"+merchantID+"|"+subjectType+"|"+subjectID)
	return nil
}

func (i *capturedInvalidator) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.subjects)
}

// fixture 是 IAM 用例的内存装配，复用真实仓储以便覆盖端到端语义。
type fixture struct {
	roles       *persistence.MemoryRoleRepository
	bindings    *persistence.MemoryBindingRepository
	snapshots   *persistence.MemorySnapshotRepository
	changes     *persistence.MemoryChangeRequestRepository
	recorder    *capturedRecorder
	invalidator *capturedInvalidator
	roleSvc     *app.RoleService
	bindingSvc  *app.BindingService
	querySvc    *app.QueryService
	changeSvc   *app.ChangeRequestService
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	f := &fixture{
		roles:       roles,
		bindings:    bindings,
		snapshots:   persistence.NewMemorySnapshotRepository(roles, bindings),
		recorder:    &capturedRecorder{},
		invalidator: &capturedInvalidator{},
		changes:     persistence.NewMemoryChangeRequestRepository(),
	}
	ids := &seqIDs{}
	var err error
	f.roleSvc, err = app.NewRoleService(app.RoleServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    f.recorder,
		Invalidator: f.invalidator,
		IDs:         ids,
		Audit:       app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits:      app.RoleLimits{MaxPermissionsPerRole: 200, RequireReason: true},
	})
	if err != nil {
		t.Fatalf("NewRoleService: %v", err)
	}
	f.bindingSvc, err = app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    f.recorder,
		Invalidator: f.invalidator,
		IDs:         ids,
		Audit:       app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits:      app.BindingLimits{MaxBindingsPerSubject: 2, RequireReason: true},
	})
	if err != nil {
		t.Fatalf("NewBindingService: %v", err)
	}
	f.querySvc, err = app.NewQueryService(roles, bindings, f.snapshots)
	if err != nil {
		t.Fatalf("NewQueryService: %v", err)
	}
	applicator, err := app.NewRoleChangeApplicator(f.roleSvc, f.bindingSvc)
	if err != nil {
		t.Fatalf("NewRoleChangeApplicator: %v", err)
	}
	f.changeSvc, err = app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   f.changes,
		Applicator: applicator,
		IDs:        ids,
		Limits:     app.ChangeLimits{RequireReason: true},
	})
	if err != nil {
		t.Fatalf("NewChangeRequestService: %v", err)
	}
	return f
}

// bizCode 返回错误的稳定业务码；非 AppError 返回 0。
func bizCode(err error) int {
	var applicationError *apperr.AppError
	if errors.As(err, &applicationError) {
		return applicationError.Code
	}
	return 0
}

// scope 构造 order 资源的数据范围。
func scope(t *testing.T, scopeType domain.DataScopeType) domain.DataScope {
	t.Helper()
	value := domain.DataScope{Resource: "order", Type: scopeType}
	if scopeType == domain.DataScopeMerchantSet {
		value.MerchantIDs = []string{"m_001", "m_002"}
	}
	return value
}
