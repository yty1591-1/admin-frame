package app_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// seedActiveCampaign 直接落库一个进行中活动，便于构造「未过期」对照组。
func seedActiveCampaign(t *testing.T, f *fixture, name string, dueAt time.Time) *domain.CertificationCampaign {
	t.Helper()
	seed := strconv.FormatInt(certSeedCounter.Add(1), 10)
	campaign := &domain.CertificationCampaign{
		ID:        "9" + seed,
		Name:      name,
		Channel:   domain.ChannelAdmin,
		Status:    domain.CertificationStatusActive,
		DueAt:     dueAt.UTC(),
		CreatedBy: "1001",
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		Version:   1,
	}
	if err := f.certCampaigns.Save(context.Background(), campaign, 0); err != nil {
		t.Fatalf("seed active campaign: %v", err)
	}
	return campaign
}

func TestCertificationSweepOverdueExpiresOnlyOverdueCampaigns(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_sweep", iam.PermRoleRead)
	binding := bindCertSubject(t, f, "2001", role.ID)

	_, task := seedOverdueCertification(t, f, binding)
	seedActiveCampaign(t, f, "未到期复核", time.Now().UTC().Add(24*time.Hour))

	result, err := f.certSvc.SweepOverdue(context.Background())
	if err != nil {
		t.Fatalf("SweepOverdue: %v", err)
	}
	if result.Overdue != 1 || result.Expired != 1 || result.Failed != 0 || result.Tasks != 1 {
		t.Fatalf("result=%+v, want overdue=1 expired=1 failed=0 tasks=1", result)
	}
	if result.Scanned < 2 {
		t.Fatalf("scanned=%d, want >=2 (active campaigns include not-due one)", result.Scanned)
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

	// auto_revoke 关闭时未到期活动不受影响：再次扫描不应产生任何动作。
	second, err := f.certSvc.SweepOverdue(context.Background())
	if err != nil {
		t.Fatalf("second SweepOverdue: %v", err)
	}
	if second.Expired != 1 || second.Tasks != 0 {
		t.Fatalf("second sweep=%+v, want expired=1 tasks=0 (idempotent)", second)
	}
	if second.Scanned < result.Scanned {
		t.Fatalf("second scanned=%d, want >=%d", second.Scanned, result.Scanned)
	}
}

func TestCertificationSweepOverdueAutoRevokes(t *testing.T) {
	f := newFixture(t)
	role := createCertRole(t, f, "ops_cert_sweep_revoke", iam.PermRoleRead)
	binding := bindCertSubject(t, f, "2001", role.ID)

	_, task := seedOverdueCertification(t, f, binding)
	result, err := f.certSvcAutoRevoke.SweepOverdue(context.Background())
	if err != nil {
		t.Fatalf("SweepOverdue(auto): %v", err)
	}
	if result.Expired != 1 || result.Tasks != 1 {
		t.Fatalf("result=%+v, want expired=1 tasks=1", result)
	}
	if f.certRevoker.count() != 1 {
		t.Fatalf("auto revoke calls=%d, want 1", f.certRevoker.count())
	}
	updated, err := f.certTasks.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Status != domain.CertificationTaskStatusRevoked {
		t.Fatalf("status=%s, want revoked", updated.Status)
	}
	if updated.DecidedBy != "system:certification" {
		t.Fatalf("decided_by=%q, want system:certification", updated.DecidedBy)
	}
}

// TestCertificationSweepOverdueNoActiveCampaigns 验证空集扫描不报错。
func TestCertificationSweepOverdueNoActiveCampaigns(t *testing.T) {
	f := newFixture(t)
	result, err := f.certSvc.SweepOverdue(context.Background())
	if err != nil {
		t.Fatalf("SweepOverdue: %v", err)
	}
	if result != (app.CertificationSweepResult{}) {
		t.Fatalf("result=%+v, want zero value", result)
	}
}
