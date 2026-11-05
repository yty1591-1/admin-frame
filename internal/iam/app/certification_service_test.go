package app_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// certScope 返回 A 端复核作用域。
func certScope() app.CertificationScope {
	return app.CertificationScope{Channel: domain.ChannelAdmin}
}

// createCertRole 创建带指定权限的角色。
func createCertRole(t *testing.T, f *fixture, code string, perm iam.PermissionCode) *domain.Role {
	t.Helper()
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        code,
		Name:        code,
		Permissions: []iam.PermissionCode{perm},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("create role %s: %v", code, err)
	}
	return role
}

// bindCertSubject 绑定主体与角色。
func bindCertSubject(t *testing.T, f *fixture, subjectID, roleID string) *domain.Binding {
	t.Helper()
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   subjectID,
		RoleID:      roleID,
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("bind %s: %v", subjectID, err)
	}
	return binding
}

// createCertCampaign 创建并返回草稿活动。
func createCertCampaign(
	t *testing.T, f *fixture, roleIDs []string, privilegedOnly bool,
) *domain.CertificationCampaign {
	t.Helper()
	campaign, err := f.certSvc.CreateCampaign(context.Background(), app.CreateCertificationCampaignCommand{
		Channel:        domain.ChannelAdmin,
		Name:           "2026 Q4 访问复核",
		RoleIDs:        roleIDs,
		PrivilegedOnly: privilegedOnly,
		DueAt:          time.Now().UTC().Add(48 * time.Hour),
		Actor:          "1001",
		Reason:         "季度复核",
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

func TestCertificationGenerateTasksIsIdempotent(t *testing.T) {
	f := newFixture(t)
	privileged := createCertRole(t, f, "platform_admin_cert", iam.PermRoleRead)
	plain := createCertRole(t, f, "featureflag_ops_cert", iam.PermFeatureFlagWrite)
	bindCertSubject(t, f, "2001", privileged.ID)
	bindCertSubject(t, f, "2002", plain.ID)

	campaign := createCertCampaign(t, f, nil, true)
	activated, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001")
	if err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}
	tasks, total, err := f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if total != 1 || len(tasks) != 1 || activated.TaskCount != 1 {
		t.Fatalf("total=%d tasks=%d campaign=%+v", total, len(tasks), activated)
	}
	if tasks[0].RoleID != privileged.ID {
		t.Fatalf("privileged-only picked role %s, want %s", tasks[0].RoleID, privileged.ID)
	}

	// 重复激活不得产生重复任务，也不得覆盖已存在任务。
	if _, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("repeat ActivateCampaign: %v", err)
	}
	_, total, err = f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks after repeat: %v", err)
	}
	if total != 1 {
		t.Fatalf("repeat generation created %d tasks, want 1", total)
	}
}

func TestCertificationConfirmAndRevoke(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert", iam.PermRoleRead)
	bindCertSubject(t, f, "2001", role.ID)
	bindCertSubject(t, f, "2002", role.ID)
	campaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}
	tasks, _, err := f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks=%d, want 2", len(tasks))
	}

	confirmed, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留此权限", "1001", certScope())
	if err != nil {
		t.Fatalf("Decide confirm: %v", err)
	}
	if confirmed.Status != domain.CertificationTaskStatusConfirmed {
		t.Fatalf("confirmed status=%s", confirmed.Status)
	}
	if f.certRevoker.count() != 0 {
		t.Fatalf("confirm must not revoke, calls=%d", f.certRevoker.count())
	}

	revoked, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[1].ID,
		domain.CertificationDecisionRevoke, "岗位已调整", "1001", certScope())
	if err != nil {
		t.Fatalf("Decide revoke: %v", err)
	}
	if revoked.Status != domain.CertificationTaskStatusRevoked {
		t.Fatalf("revoked status=%s", revoked.Status)
	}
	if f.certRevoker.count() != 1 {
		t.Fatalf("revoke calls=%d, want 1", f.certRevoker.count())
	}
}

func TestCertificationRevokeSkipsWhenFingerprintChanged(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_drift", iam.PermRoleRead)
	binding := bindCertSubject(t, f, "2001", role.ID)
	campaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}
	tasks, _, err := f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: err=%v tasks=%d", err, len(tasks))
	}

	// 复核期间绑定被撤销：指纹已变化，任务必须 skipped 且不得调用回收。
	if err := f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, Actor: "1001", Reason: "复核期间人工解绑",
	}); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	task, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[0].ID,
		domain.CertificationDecisionRevoke, "驳回", "1001", certScope())
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Decide err=%v, want conflict", err)
	}
	if bizCode(err) != 40900 {
		t.Fatalf("biz code=%d, want 40900", bizCode(err))
	}
	if task == nil || task.Status != domain.CertificationTaskStatusSkipped {
		t.Fatalf("task=%+v, want skipped", task)
	}
	if f.certRevoker.count() != 0 {
		t.Fatalf("fingerprint mismatch must not revoke, calls=%d", f.certRevoker.count())
	}
}

func TestCertificationDecideIsIdempotent(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_idem", iam.PermRoleRead)
	bindCertSubject(t, f, "2001", role.ID)
	campaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}
	tasks, _, err := f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: err=%v tasks=%d", err, len(tasks))
	}
	first, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留", "1001", certScope())
	if err != nil {
		t.Fatalf("first decision: %v", err)
	}
	version := first.Version
	second, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留", "1001", certScope())
	if err != nil {
		t.Fatalf("repeat decision: %v", err)
	}
	if second.Status != first.Status || second.Version != version {
		t.Fatalf("repeat decision mutated task: first=%+v second=%+v", first, second)
	}
}

func TestCertificationAutoExpireRespectsAutoRevoke(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_expire", iam.PermRoleRead)
	binding := bindCertSubject(t, f, "2001", role.ID)

	campaign, task := seedOverdueCertification(t, f, binding)
	affected, err := f.certSvc.AutoExpireOverdue(context.Background(), campaign.ID, certScope())
	if err != nil {
		t.Fatalf("AutoExpireOverdue: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected=%d, want 1", affected)
	}
	updated, err := f.certTasks.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Status != domain.CertificationTaskStatusExpired {
		t.Fatalf("status=%s, want expired", updated.Status)
	}
	if f.certRevoker.count() != 0 {
		t.Fatalf("auto_revoke=false must not revoke, calls=%d", f.certRevoker.count())
	}

	// 开启 auto_revoke 后，指纹一致的过期任务应被自动回收。
	campaign2, task2 := seedOverdueCertification(t, f, binding)
	affected, err = f.certSvcAutoRevoke.AutoExpireOverdue(context.Background(), campaign2.ID, certScope())
	if err != nil {
		t.Fatalf("AutoExpireOverdue(auto): %v", err)
	}
	if affected != 1 {
		t.Fatalf("auto affected=%d, want 1", affected)
	}
	updated2, err := f.certTasks.FindByID(context.Background(), task2.ID)
	if err != nil {
		t.Fatalf("FindByID(auto): %v", err)
	}
	if updated2.Status != domain.CertificationTaskStatusRevoked {
		t.Fatalf("auto status=%s, want revoked", updated2.Status)
	}
	if f.certRevoker.count() != 1 {
		t.Fatalf("auto revoke calls=%d, want 1", f.certRevoker.count())
	}
}

func TestCertificationListCountAndClose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := createCertCampaign(t, f, nil, false)
	second := createCertCampaign(t, f, nil, false)

	filter := domain.CertificationCampaignFilter{
		Status: domain.CertificationStatusDraft,
		Limit:  100,
	}
	campaigns, err := f.certSvc.ListCampaigns(ctx, certScope(), filter)
	if err != nil {
		t.Fatalf("ListCampaigns: %v", err)
	}
	if len(campaigns) != 2 {
		t.Fatalf("ListCampaigns len=%d, want 2", len(campaigns))
	}
	total, err := f.certSvc.CountCampaigns(ctx, certScope(), filter)
	if err != nil {
		t.Fatalf("CountCampaigns: %v", err)
	}
	if total != 2 {
		t.Fatalf("CountCampaigns=%d, want 2", total)
	}

	// B 端作用域缺少商户时不得退化为全量查询。
	if _, err := f.certSvc.ListCampaigns(ctx, app.CertificationScope{Channel: domain.ChannelBiz}, filter); !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("biz scope without merchant err=%v, want forbidden", err)
	}
	if _, err := f.certSvc.GetCampaign(ctx, "999999", certScope()); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("GetCampaign err=%v, want not found", err)
	}

	closed, err := f.certSvc.CloseCampaign(ctx, first.ID, certScope(), "1001", "复核结束")
	if err != nil {
		t.Fatalf("CloseCampaign: %v", err)
	}
	if closed.Status != domain.CertificationStatusClosed || closed.ClosedAt == nil {
		t.Fatalf("closed campaign=%+v", closed)
	}
	// 重复关闭幂等，不推进版本。
	again, err := f.certSvc.CloseCampaign(ctx, first.ID, certScope(), "1001", "复核结束")
	if err != nil {
		t.Fatalf("repeat CloseCampaign: %v", err)
	}
	if again.Version != closed.Version || again.Status != closed.Status {
		t.Fatalf("repeat close mutated campaign: first=%+v again=%+v", closed, again)
	}

	campaigns, err = f.certSvc.ListCampaigns(ctx, certScope(), filter)
	if err != nil {
		t.Fatalf("ListCampaigns after close: %v", err)
	}
	if len(campaigns) != 1 || campaigns[0].ID != second.ID {
		t.Fatalf("draft campaigns after close=%+v, want only %s", campaigns, second.ID)
	}
}

func TestCertificationErrorPaths(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	valid := app.CreateCertificationCampaignCommand{
		Channel: domain.ChannelAdmin,
		Name:    "错误路径复核",
		DueAt:   time.Now().UTC().Add(24 * time.Hour),
		Actor:   "1001",
		Reason:  "季度复核",
	}

	// 领域构造校验：名称缺失 / 截止时间已过。
	invalidName := valid
	invalidName.Name = ""
	if _, err := f.certSvc.CreateCampaign(ctx, invalidName); bizCode(err) != 40000 {
		t.Fatalf("empty name err=%v, want 40000", err)
	}
	overdue := valid
	overdue.DueAt = time.Now().UTC().Add(-time.Hour)
	if _, err := f.certSvc.CreateCampaign(ctx, overdue); bizCode(err) != 40000 {
		t.Fatalf("past due err=%v, want 40000", err)
	}
	noReason := valid
	noReason.Reason = ""
	if _, err := f.certSvc.CreateCampaign(ctx, noReason); bizCode(err) != 40000 {
		t.Fatalf("missing reason err=%v, want 40000", err)
	}

	if _, err := f.certSvc.ActivateCampaign(ctx, "999999", certScope(), "1001"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("activate missing err=%v, want not found", err)
	}
	campaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(ctx, campaign.ID, certScope(), ""); bizCode(err) != 40000 {
		t.Fatalf("activate without actor err=%v, want 40000", err)
	}
	if _, _, err := f.certSvc.ListTasks(ctx, campaign.ID, certScope(), domain.CertificationTaskFilter{
		Status: domain.CertificationTaskStatus("bogus"),
	}); bizCode(err) != 40000 {
		t.Fatalf("invalid task status err=%v, want 40000", err)
	}
	if _, _, err := f.certSvc.ListTasks(ctx, "999999", certScope(), domain.CertificationTaskFilter{}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("list missing tasks err=%v, want not found", err)
	}
	if _, err := f.certSvc.CloseCampaign(ctx, "999999", certScope(), "1001", "关闭"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("close missing err=%v, want not found", err)
	}
	if _, _, err := f.certSvc.ExportEvidence(ctx, campaign.ID, certScope(), "xml", "1001"); bizCode(err) != 40000 {
		t.Fatalf("export invalid format err=%v, want 40000", err)
	}
	if _, _, err := f.certSvc.ExportEvidence(ctx, "999999", certScope(), "csv", "1001"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("export missing err=%v, want not found", err)
	}

	// 未到期活动不应产生过期处理。
	if _, err := f.certSvc.ActivateCampaign(ctx, campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("activate for auto expire: %v", err)
	}
	if affected, err := f.certSvc.AutoExpireOverdue(ctx, campaign.ID, certScope()); err != nil || affected != 0 {
		t.Fatalf("auto expire before due affected=%d err=%v", affected, err)
	}

	// 关闭后不得再裁决。
	role := createCertRole(t, f, "ops_cert_closed", iam.PermRoleRead)
	bindCertSubject(t, f, "2001", role.ID)
	closedCampaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(ctx, closedCampaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tasks, _, err := f.certSvc.ListTasks(ctx, closedCampaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks err=%v tasks=%d", err, len(tasks))
	}
	if _, err := f.certSvc.CloseCampaign(ctx, closedCampaign.ID, certScope(), "1001", "结束"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := f.certSvc.DecideTask(ctx, closedCampaign.ID, tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留", "1001", certScope()); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("decide closed campaign err=%v, want conflict", err)
	}
	if _, err := f.certSvc.DecideTask(ctx, "999999", tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留", "1001", certScope()); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("decide missing campaign err=%v, want not found", err)
	}
	if _, err := f.certSvc.DecideTask(ctx, campaign.ID, "999999",
		domain.CertificationDecisionConfirm, "保留", "1001", certScope()); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("decide missing task err=%v, want not found", err)
	}
}

func TestCertificationAuditFailureIsFailClosed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, err := app.NewCertificationService(app.CertificationServiceConfig{}); err == nil {
		t.Fatal("empty config must fail")
	}

	f.recorder.err = errors.New("audit unavailable")
	if _, err := f.certSvc.CreateCampaign(ctx, app.CreateCertificationCampaignCommand{
		Channel: domain.ChannelAdmin,
		Name:    "审计失败复核",
		DueAt:   time.Now().UTC().Add(24 * time.Hour),
		Actor:   "1001",
		Reason:  "季度复核",
	}); err == nil {
		t.Fatal("create must fail when audit write fails")
	}

	// 审计恢复后创建成功，但激活阶段审计再次失败时整体回滚。
	f.recorder.err = nil
	campaign := createCertCampaign(t, f, nil, false)
	f.recorder.err = errors.New("audit unavailable")
	if _, err := f.certSvc.ActivateCampaign(ctx, campaign.ID, certScope(), "1001"); err == nil {
		t.Fatal("activate must fail when audit write fails")
	}
}

func TestCertificationBizScopeIsolation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bizScope := app.CertificationScope{Channel: domain.ChannelBiz, MerchantID: "merchant-1"}
	campaign, err := f.certSvc.CreateCampaign(ctx, app.CreateCertificationCampaignCommand{
		Channel:    domain.ChannelBiz,
		MerchantID: "merchant-1",
		Name:       "商户访问复核",
		DueAt:      time.Now().UTC().Add(24 * time.Hour),
		Actor:      "1001",
		Reason:     "商户季度复核",
	})
	if err != nil {
		t.Fatalf("CreateCampaign biz: %v", err)
	}
	campaigns, err := f.certSvc.ListCampaigns(ctx, bizScope, domain.CertificationCampaignFilter{Limit: 100})
	if err != nil || len(campaigns) != 1 || campaigns[0].ID != campaign.ID {
		t.Fatalf("biz ListCampaigns campaigns=%+v err=%v", campaigns, err)
	}
	// A 端与其它商户均不可见，统一返回 NotFound。
	if _, err := f.certSvc.GetCampaign(ctx, campaign.ID, certScope()); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("admin scope visibility err=%v, want not found", err)
	}
	if _, err := f.certSvc.GetCampaign(ctx, campaign.ID, app.CertificationScope{
		Channel: domain.ChannelBiz, MerchantID: "merchant-2",
	}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("other merchant visibility err=%v, want not found", err)
	}
	total, err := f.certSvc.CountCampaigns(ctx, bizScope, domain.CertificationCampaignFilter{Limit: 100})
	if err != nil || total != 1 {
		t.Fatalf("biz CountCampaigns total=%d err=%v", total, err)
	}
}

// certSeedCounter 保证手工落库的过期活动 ID 唯一。
var certSeedCounter atomic.Int64

// seedOverdueCertification 直接落库一个已过期的进行中活动与一条待裁决任务，
// 绕过构造器「截止时间必须在未来」的校验，用于覆盖自动过期路径。
func seedOverdueCertification(
	t *testing.T, f *fixture, binding *domain.Binding,
) (*domain.CertificationCampaign, *domain.CertificationTask) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	seed := strconv.FormatInt(certSeedCounter.Add(1), 10)
	campaign := &domain.CertificationCampaign{
		ID:        "7" + seed,
		Name:      "过期复核",
		Channel:   domain.ChannelAdmin,
		Status:    domain.CertificationStatusActive,
		DueAt:     now.Add(-time.Hour),
		CreatedBy: "1001",
		CreatedAt: now.Add(-2 * time.Hour),
		Version:   1,
		TaskCount: 1,
		RoleIDs:   []string{binding.RoleID},
	}
	if err := f.certCampaigns.Save(ctx, campaign, 0); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	task, err := domain.NewCertificationTask(domain.NewCertificationTaskParams{
		ID:                 "8" + seed,
		CampaignID:         campaign.ID,
		SubjectType:        binding.SubjectType,
		SubjectID:          binding.SubjectID,
		RoleID:             binding.RoleID,
		BindingID:          binding.ID,
		BindingFingerprint: domain.BindingFingerprint(binding),
		ReviewerID:         "1001",
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := f.certTasks.SaveAll(ctx, []*domain.CertificationTask{task}); err != nil {
		t.Fatalf("seed SaveAll: %v", err)
	}
	return campaign, task
}

func TestCertificationExportEvidence(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_export", iam.PermRoleRead)
	bindCertSubject(t, f, "2001", role.ID)
	bindCertSubject(t, f, "2002", role.ID)
	campaign := createCertCampaign(t, f, nil, false)
	if _, err := f.certSvc.ActivateCampaign(context.Background(), campaign.ID, certScope(), "1001"); err != nil {
		t.Fatalf("ActivateCampaign: %v", err)
	}
	tasks, _, err := f.certSvc.ListTasks(context.Background(), campaign.ID, certScope(), domain.CertificationTaskFilter{})
	if err != nil || len(tasks) != 2 {
		t.Fatalf("ListTasks: err=%v tasks=%d", err, len(tasks))
	}
	if _, err := f.certSvc.DecideTask(context.Background(), campaign.ID, tasks[0].ID,
		domain.CertificationDecisionConfirm, "保留", "1001", certScope()); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	csvPayload, csvType, err := f.certSvc.ExportEvidence(context.Background(), campaign.ID, certScope(), "csv", "1001")
	if err != nil {
		t.Fatalf("ExportEvidence csv: %v", err)
	}
	if csvType != "text/csv; charset=utf-8" {
		t.Fatalf("csv content type=%q", csvType)
	}
	rows, err := csv.NewReader(strings.NewReader(string(csvPayload))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") !=
		"campaign_id,campaign_name,task_id,subject_id,role_id,binding_id,reviewer_id,status,decision,reason,decided_by,decided_at,created_at" {
		t.Fatalf("csv header/rows=%v", rows)
	}
	if rows[1][0] != campaign.ID || rows[1][1] != campaign.Name {
		t.Fatalf("csv campaign fields=%v", rows[1])
	}

	jsonPayload, jsonType, err := f.certSvc.ExportEvidence(context.Background(), campaign.ID, certScope(), "json", "1001")
	if err != nil {
		t.Fatalf("ExportEvidence json: %v", err)
	}
	if jsonType != "application/json; charset=utf-8" {
		t.Fatalf("json content type=%q", jsonType)
	}
	var records []map[string]any
	if err := json.Unmarshal(jsonPayload, &records); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("json records=%d, want 2", len(records))
	}
	for _, key := range []string{
		"campaign_id", "campaign_name", "task_id", "subject_id", "role_id", "binding_id",
		"reviewer_id", "status", "decision", "reason", "decided_by", "decided_at", "created_at",
	} {
		if _, ok := records[0][key]; !ok {
			t.Fatalf("json missing field %q in %v", key, records[0])
		}
	}
}
