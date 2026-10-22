package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// txToken 标记 context 中是否已存在事务（模拟 db.TxFromContext 的语义）。
type txToken struct{}

// countingUoW 记录事务开启次数，并像真实实现一样复用外层事务。
//
// 复用语义很关键：审批应用会调用角色 / 绑定用例，若每次写入都新开事务，
// MySQL 下会各自提交，原子性反而被破坏。
type countingUoW struct {
	begins   int
	depth    int
	maxDepth int
	err      error
}

func (u *countingUoW) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if u.err != nil {
		return u.err
	}
	if ctx.Value(txToken{}) != nil {
		// 已处于事务中：直接执行，不再开启。
		return fn(ctx)
	}
	u.begins++
	u.depth++
	if u.depth > u.maxDepth {
		u.maxDepth = u.depth
	}
	defer func() { u.depth-- }()
	return fn(context.WithValue(ctx, txToken{}, true))
}

// TestCreateRoleRunsInsideSingleTransaction 验证「角色写入 + 审计」同事务。
func TestCreateRoleRunsInsideSingleTransaction(t *testing.T) {
	uow := &countingUoW{}
	f := newFixtureWithUOW(t, uow)

	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "ops_lead",
		Name:        "运营主管",
		Permissions: []iam.PermissionCode{iam.PermOrderWrite, iam.PermOrderRead},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeMerchantSet)},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if uow.begins != 1 {
		t.Fatalf("unit of work begins = %d, want 1", uow.begins)
	}
	if f.recorder.count() != 1 {
		t.Fatalf("audit events = %d, want 1", f.recorder.count())
	}
	if _, err := f.roles.FindByID(context.Background(), role.ID); err != nil {
		t.Fatalf("role must be persisted: %v", err)
	}
}

// TestCreateRolePropagatesUnitOfWorkFailure 验证事务开启失败时不写任何数据。
func TestCreateRolePropagatesUnitOfWorkFailure(t *testing.T) {
	// 注入一个事务失败：仓储都正常，但事务边界无法建立（模拟数据库不可用）。
	broken := &countingUoW{err: apperr.Unavailable("iam unit of work is not configured")}
	f := newFixtureWithUOW(t, broken)

	_, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "ops_lead",
		Name:        "运营主管",
		Permissions: []iam.PermissionCode{iam.PermOrderRead},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeAll)},
		Actor:       "1001",
	})
	if bizCode(err) != apperr.CodeServiceUnavailable {
		t.Fatalf("err = %v, want 50300", err)
	}
	if f.recorder.count() != 0 {
		t.Fatalf("audit must not be written when tx fails")
	}
	list, listErr := f.roles.List(context.Background(), domain.RoleFilter{})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(list) != 0 {
		t.Fatalf("role must not be persisted when tx fails: %d rows", len(list))
	}
}

// TestBreakglassApprovalReusesOuterTransaction 验证审批应用全程只开一个事务。
//
// 该路径最长：审批单状态 + 绑定创建 + 角色/绑定审计 + 破玻璃审计。
// 任何一步另开事务都会破坏「要么全部生效、要么全部不生效」的保证。
func TestBreakglassApprovalReusesOuterTransaction(t *testing.T) {
	uow := &countingUoW{}
	f := newFixtureWithUOW(t, uow)
	ctx := context.Background()

	seedBreakglassRole(t, f)
	// 破玻璃需要显式开启；这里单独装配审批服务，保持与运行时一致的依赖关系。
	applicator, err := app.NewRoleChangeApplicator(f.roleSvc, f.bindingSvc)
	if err != nil {
		t.Fatalf("NewRoleChangeApplicator: %v", err)
	}
	changes, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   f.changes,
		Applicator: applicator,
		UnitOfWork: uow,
		IDs:        &seqIDs{},
		Limits:     app.ChangeLimits{RequireReason: true, AllowBreakglass: true},
	})
	if err != nil {
		t.Fatalf("NewChangeRequestService: %v", err)
	}

	breakglassSvc, err := app.NewBreakglassService(app.BreakglassServiceConfig{
		Changes:  changes,
		Recorder: f.recorder,
		IDs:      &seqIDs{},
		Audit:    app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits:   app.BreakglassLimits{Enabled: true},
	})
	if err != nil {
		t.Fatalf("NewBreakglassService: %v", err)
	}

	request, err := breakglassSvc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: "staff",
		SubjectID:   "1001",
		Duration:    30 * time.Minute,
		Reason:      "生产故障排查",
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	// 申请人不能自批：换一个审批人。
	if _, err := breakglassSvc.Approve(ctx, app.ApproveBreakglassCommand{
		ID:       request.ID,
		Version:  request.Version,
		Approver: "2002",
	}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if uow.maxDepth != 1 {
		t.Fatalf("nested transactions detected: maxDepth = %d, want 1", uow.maxDepth)
	}
	// 申请 + 审批各一个外层事务。
	if uow.begins != 2 {
		t.Fatalf("outer transactions = %d, want 2", uow.begins)
	}
	if f.recorder.count() < 3 {
		t.Fatalf("audit events = %d, want request + role/binding + breakglass", f.recorder.count())
	}
}

// TestApplyChangeRequestPropagatesCommitFailure 验证应用失败不留半成品。
func TestApplyChangeRequestPropagatesCommitFailure(t *testing.T) {
	uow := &countingUoW{}
	f := newFixtureWithUOW(t, uow)
	ctx := context.Background()

	request, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleUpdate,
		TargetID: "1001",
		Payload:  []byte(`{"status":"disabled"}`),
		Reason:   "下线离职人员角色",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(ctx, request.ID, request.Version, "2002", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	before := uow.begins

	// 目标角色不存在：applicator 返回 404，事务内失败必须整体回滚。
	_, applyErr := f.changeSvc.Apply(ctx, app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "2002",
		Scope:   adminScope,
	})
	if bizCode(applyErr) != apperr.CodeNotFound {
		t.Fatalf("apply err = %v, want 40400", applyErr)
	}
	if uow.begins != before+1 {
		t.Fatalf("apply must open exactly one transaction: %d -> %d", before, uow.begins)
	}
	current, err := f.changes.FindByID(ctx, approved.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if current.Status != domain.ChangeStatusApproved {
		t.Fatalf("status = %s, want approved（失败不得标记 applied）", current.Status)
	}
}

// TestNoopUnitOfWorkPropagatesErrors 验证默认实现的语义（无事务、不吞错）。
func TestNoopUnitOfWorkPropagatesErrors(t *testing.T) {
	sentinel := errors.New("boom")
	err := app.NoopUnitOfWork{}.WithinTx(context.Background(), func(context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if err := (app.NoopUnitOfWork{}).WithinTx(context.Background(), nil); bizCode(err) != apperr.CodeInternal {
		t.Fatalf("nil func err = %v, want 50000", err)
	}
	// 审计接口契约：Noop 不得改变事件内容。
	recorder := &capturedRecorder{}
	err = app.NoopUnitOfWork{}.WithinTx(context.Background(), func(ctx context.Context) error {
		return recorder.Record(ctx, audit.Event{Action: audit.ActionRBACRoleChange})
	})
	if err != nil || recorder.count() != 1 {
		t.Fatalf("noop uow record = %v, count = %d", err, recorder.count())
	}
}
