package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// newBreakglassFixture 在内存装配上播种系统破玻璃角色，并构造破玻璃用例。
func newBreakglassFixture(t *testing.T, limits app.BreakglassLimits) (*fixture, *app.BreakglassService) {
	t.Helper()
	f := newFixture(t)
	seedBreakglassRole(t, f)

	applicator, err := app.NewRoleChangeApplicator(f.roleSvc, f.bindingSvc)
	if err != nil {
		t.Fatalf("NewRoleChangeApplicator: %v", err)
	}
	changes, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   f.changes,
		Applicator: applicator,
		IDs:        &seqIDs{},
		Limits:     app.ChangeLimits{RequireReason: true, AllowBreakglass: true},
	})
	if err != nil {
		t.Fatalf("NewChangeRequestService: %v", err)
	}
	svc, err := app.NewBreakglassService(app.BreakglassServiceConfig{
		Changes:  changes,
		Recorder: f.recorder,
		IDs:      &seqIDs{},
		Audit:    app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits:   limits,
	})
	if err != nil {
		t.Fatalf("NewBreakglassService: %v", err)
	}
	return f, svc
}

// seedBreakglassRole 直接写入系统角色，模拟迁移 000004 的种子。
func seedBreakglassRole(t *testing.T, f *fixture) *domain.Role {
	t.Helper()
	role := &domain.Role{
		ID:        "9001",
		Channel:   domain.ChannelAdmin,
		Scope:     domain.RoleScopePlatform,
		Code:      domain.BreakglassRoleCode,
		Name:      "破玻璃超级权限",
		Type:      domain.RoleTypeBreakglass,
		Status:    domain.RoleStatusActive,
		Version:   1,
		CreatedAt: time.Now().UTC(),
		CreatedBy: "system",
		UpdatedBy: "system",
	}
	if err := f.roles.Save(context.Background(), role, 0); err != nil {
		t.Fatalf("seed breakglass role: %v", err)
	}
	return role
}

func TestBreakglassRequestApproveExpandsWildcardSnapshot(t *testing.T) {
	ctx := context.Background()
	f, svc := newBreakglassFixture(t, app.BreakglassLimits{Enabled: true})

	request, err := svc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectStaff),
		SubjectID:   "1001",
		Duration:    30 * time.Minute,
		Reason:      "线上故障排查",
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if request.Status != domain.ChangeStatusPending || request.Action != domain.ChangeActionBreakglassRequest {
		t.Fatalf("request=%+v", request)
	}

	// 申请人自审必须被 SoD 拦住。
	if _, err := svc.Approve(ctx, app.ApproveBreakglassCommand{
		ID: request.ID, Version: request.Version, Approver: "1001",
	}); bizCode(err) != 40300 {
		t.Fatalf("self approve code=%d err=%v", bizCode(err), err)
	}

	applied, err := svc.Approve(ctx, app.ApproveBreakglassCommand{
		ID: request.ID, Version: request.Version, Approver: "1002",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if applied.Status != domain.ChangeStatusApplied || applied.TargetID == "" {
		t.Fatalf("applied=%+v", applied)
	}

	binding, err := f.bindings.FindByID(ctx, applied.TargetID)
	if err != nil {
		t.Fatalf("load binding: %v", err)
	}
	if binding.RoleID != "9001" || binding.ExpiresAt == nil {
		t.Fatalf("binding=%+v", binding)
	}
	if remaining := time.Until(*binding.ExpiresAt); remaining <= 0 || remaining > domain.MaxBreakglassDuration {
		t.Fatalf("remaining=%s", remaining)
	}

	snapshot, err := f.snapshots.Load(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "1001",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.Breakglass {
		t.Fatal("snapshot must be marked breakglass")
	}
	if !snapshot.HasPermission("order:read") || !snapshot.HasPermission("iam:role:grant") {
		t.Fatalf("breakglass snapshot must allow all, got %v", snapshot.Permissions)
	}

	// 重复审批按幂等成功返回，不能再产生第二条绑定。
	again, err := svc.Approve(ctx, app.ApproveBreakglassCommand{
		ID: request.ID, Version: applied.Version, Approver: "1002",
	})
	if err != nil {
		t.Fatalf("idempotent approve: %v", err)
	}
	if again.TargetID != applied.TargetID {
		t.Fatalf("binding changed: %s -> %s", applied.TargetID, again.TargetID)
	}

	// 撤销后快照立即失去通配符。
	if _, err := svc.Revoke(ctx, request.ID, applied.Version+1, "1002"); bizCode(err) != 40900 {
		t.Fatalf("stale revoke err=%v code=%d", err, bizCode(err))
	}
	if _, err := svc.Revoke(ctx, request.ID, again.Version, "1002"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, err := f.snapshots.Load(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "1001",
	})
	if err != nil {
		t.Fatalf("snapshot after revoke: %v", err)
	}
	if revoked.Breakglass || revoked.HasPermission("order:read") {
		t.Fatalf("revoked snapshot=%+v", revoked)
	}
}

func TestBreakglassWritesAuditTrail(t *testing.T) {
	ctx := context.Background()
	f, svc := newBreakglassFixture(t, app.BreakglassLimits{Enabled: true})

	request, err := svc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectStaff),
		SubjectID:   "1001",
		Duration:    time.Minute,
		Reason:      "审计检查",
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	applied, err := svc.Approve(ctx, app.ApproveBreakglassCommand{
		ID: request.ID, Version: request.Version, Approver: "1002",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.Revoke(ctx, request.ID, applied.Version, "1002"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	events := f.recorder.events
	var operations []string
	for _, event := range events {
		if event.Action != audit.ActionRBACBreakglass {
			continue
		}
		if event.ResourceType != app.ResourceTypeBreakglass || event.ResourceID != request.ID {
			t.Fatalf("event=%+v", event)
		}
		operations = append(operations, event.Attrs[app.AttrOperation])
	}
	if len(operations) != 3 || operations[0] != "request" || operations[1] != "approve" || operations[2] != "revoke" {
		t.Fatalf("operations=%v events=%d", operations, len(events))
	}
}

func TestBreakglassGuards(t *testing.T) {
	ctx := context.Background()
	f, svc := newBreakglassFixture(t, app.BreakglassLimits{
		Enabled:         true,
		AllowedSubjects: []string{"1001"},
	})
	if _, err := svc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectStaff),
		SubjectID:   "2002",
		Duration:    time.Minute,
		Reason:      "不在白名单",
		Actor:       "2002",
	}); bizCode(err) != 40300 {
		t.Fatalf("allowlist err=%v code=%d", err, bizCode(err))
	}

	// 直接绑定破玻璃角色必须被拒绝，否则绕过审批即获得 "*"。
	expiry := time.Now().UTC().Add(10 * time.Minute)
	if _, err := f.bindingSvc.Bind(ctx, app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "1001",
		RoleID: "9001", ExpiresAt: &expiry, Actor: "1001", Reason: "绕过审批",
	}); bizCode(err) != 40300 {
		t.Fatalf("direct breakglass bind err=%v code=%d", err, bizCode(err))
	}

	if _, _, err := f.roleSvc.ReplacePermissions(ctx, app.ReplacePermissionsCommand{
		ID: "9001", Permissions: []iam.PermissionCode{iam.PermOrderRead},
		Version: 1, Actor: "1001", Reason: "试图固化超级权限",
	}); bizCode(err) != 40300 {
		t.Fatalf("breakglass permission write err=%v code=%d", err, bizCode(err))
	}

	if _, err := svc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectStaff),
		SubjectID:   "1001",
		Duration:    domain.MaxBreakglassDuration + time.Minute,
		Reason:      "超时长",
		Actor:       "1001",
	}); bizCode(err) != 40000 {
		t.Fatalf("duration err=%v code=%d", err, bizCode(err))
	}

	if _, err := svc.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectMerchantStaff),
		SubjectID:   "1001",
		Duration:    time.Minute,
		Reason:      "主体类型不匹配",
		Actor:       "1001",
	}); err == nil {
		t.Fatal("expected subject type rejection")
	}

	disabled, err := app.NewBreakglassService(app.BreakglassServiceConfig{
		Changes:  f.changeSvc,
		Recorder: f.recorder,
		IDs:      &seqIDs{},
		Limits:   app.BreakglassLimits{Enabled: false},
	})
	if err != nil {
		t.Fatalf("NewBreakglassService: %v", err)
	}
	if _, err := disabled.Request(ctx, app.RequestBreakglassCommand{
		SubjectType: string(domain.SubjectStaff),
		SubjectID:   "1001",
		Duration:    time.Minute,
		Reason:      "开关关闭",
		Actor:       "1001",
	}); bizCode(err) != 40300 {
		t.Fatalf("disabled err=%v code=%d", err, bizCode(err))
	}
}
