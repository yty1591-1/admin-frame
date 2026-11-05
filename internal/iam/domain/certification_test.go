package domain

import (
	"errors"
	"testing"
	"time"
)

// certCampaign 构造合法的 A 端活动，供状态机测试复用。
func certCampaign(t *testing.T, mutate func(*NewCertificationCampaignParams)) *CertificationCampaign {
	t.Helper()
	params := NewCertificationCampaignParams{
		ID:        "9001",
		Name:      "2026 Q4 特权账号复核",
		Channel:   ChannelAdmin,
		DueAt:     time.Now().UTC().Add(24 * time.Hour),
		CreatedBy: "1001",
	}
	if mutate != nil {
		mutate(&params)
	}
	campaign, err := NewCertificationCampaign(params)
	if err != nil {
		t.Fatalf("NewCertificationCampaign: %v", err)
	}
	return campaign
}

// certTask 构造合法的复核任务。
func certTask(t *testing.T, mutate func(*NewCertificationTaskParams)) *CertificationTask {
	t.Helper()
	params := NewCertificationTaskParams{
		ID:                 "9101",
		CampaignID:         "9001",
		SubjectType:        SubjectStaff,
		SubjectID:          "2001",
		RoleID:             "3001",
		BindingID:          "4001",
		BindingFingerprint: "fingerprint",
		ReviewerID:         "1001",
	}
	if mutate != nil {
		mutate(&params)
	}
	task, err := NewCertificationTask(params)
	if err != nil {
		t.Fatalf("NewCertificationTask: %v", err)
	}
	return task
}

func TestCertificationCampaignLifecycle(t *testing.T) {
	campaign := certCampaign(t, nil)
	now := time.Now().UTC()
	if campaign.Status != CertificationStatusDraft || campaign.Version != 1 || campaign.TaskCount != 0 {
		t.Fatalf("initial campaign=%+v", campaign)
	}
	if campaign.AcceptsDecisions(now) {
		t.Fatal("draft campaign must not accept decisions")
	}
	if err := campaign.Activate(now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if campaign.Status != CertificationStatusActive || campaign.Version != 2 {
		t.Fatalf("active campaign=%+v", campaign)
	}
	if !campaign.AcceptsDecisions(now) {
		t.Fatal("active campaign before due date must accept decisions")
	}
	version := campaign.Version
	if err := campaign.Activate(now); err != nil || campaign.Version != version {
		t.Fatalf("repeat Activate: err=%v version=%d", err, campaign.Version)
	}
	if err := campaign.Close(now, "1001"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if campaign.Status != CertificationStatusClosed || campaign.ClosedAt == nil {
		t.Fatalf("closed campaign=%+v", campaign)
	}
	if err := campaign.Close(now, "1001"); err != nil {
		t.Fatalf("repeat Close must be idempotent: %v", err)
	}
	if err := campaign.Activate(now); !errors.Is(err, ErrCertificationNotActive) {
		t.Fatalf("Activate closed err=%v, want not active", err)
	}
	if campaign.AcceptsDecisions(now) {
		t.Fatal("closed campaign must not accept decisions")
	}
}

func TestCertificationCampaignValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*NewCertificationCampaignParams)
		wantErr error
	}{
		{"missing name", func(p *NewCertificationCampaignParams) { p.Name = "" }, ErrInvalidCertificationCampaign},
		{"missing creator", func(p *NewCertificationCampaignParams) { p.CreatedBy = "" }, ErrMissingActor},
		{"past due", func(p *NewCertificationCampaignParams) { p.DueAt = time.Now().UTC().Add(-time.Hour) }, ErrInvalidCertificationCampaign},
		{"admin with merchant", func(p *NewCertificationCampaignParams) { p.MerchantID = "m_01" }, ErrInvalidCertificationTenant},
		{"biz without merchant", func(p *NewCertificationCampaignParams) { p.Channel = ChannelBiz }, ErrInvalidCertificationTenant},
		{"empty role id", func(p *NewCertificationCampaignParams) { p.RoleIDs = []string{"3001", " "} }, ErrInvalidCertificationCampaign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCertificationCampaign(NewCertificationCampaignParams{
				ID:        "9001",
				Name:      "复核",
				Channel:   ChannelAdmin,
				DueAt:     time.Now().UTC().Add(time.Hour),
				CreatedBy: "1001",
			}.withMutation(tc.mutate))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

// withMutation 在构造测试入参时就地应用变体。
func (p NewCertificationCampaignParams) withMutation(mutate func(*NewCertificationCampaignParams)) NewCertificationCampaignParams {
	mutate(&p)
	return p
}

func TestCertificationCampaignOverdue(t *testing.T) {
	campaign := certCampaign(t, nil)
	now := time.Now().UTC()
	if err := campaign.Activate(now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if campaign.IsOverdue(now) {
		t.Fatal("future due date must not be overdue")
	}
	if !campaign.IsOverdue(campaign.DueAt) {
		t.Fatal("due date itself counts as overdue")
	}
	if campaign.AcceptsDecisions(campaign.DueAt) {
		t.Fatal("overdue campaign must not accept decisions")
	}
}

func TestCertificationTaskDecisionIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	task := certTask(t, nil)
	if err := task.Decide(CertificationDecisionConfirm, "1001", "保留权限", now); err != nil {
		t.Fatalf("Decide confirm: %v", err)
	}
	if task.Status != CertificationTaskStatusConfirmed || task.DecidedAt == nil || task.Version != 2 {
		t.Fatalf("task=%+v", task)
	}
	version := task.Version
	if err := task.Decide(CertificationDecisionConfirm, "1001", "保留权限", now); err != nil || task.Version != version {
		t.Fatalf("repeat confirm err=%v version=%d", err, task.Version)
	}
	if err := task.Decide(CertificationDecisionRevoke, "1001", "改为驳回", now); !errors.Is(err, ErrCertificationNotPending) {
		t.Fatalf("opposite decision err=%v, want not pending", err)
	}
}

func TestCertificationTaskDecisionRequiresReason(t *testing.T) {
	task := certTask(t, nil)
	err := task.Decide(CertificationDecisionRevoke, "1001", "  ", time.Now().UTC())
	if !errors.Is(err, ErrInvalidDecisionReason) {
		t.Fatalf("err=%v, want invalid reason", err)
	}
	if task.Status != CertificationTaskStatusPending {
		t.Fatalf("task must stay pending, got %s", task.Status)
	}
}

func TestCertificationTaskExpireAndSkip(t *testing.T) {
	now := time.Now().UTC()
	expired := certTask(t, nil)
	if err := expired.Expire(now); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if expired.Status != CertificationTaskStatusExpired || expired.DecidedAt == nil {
		t.Fatalf("expired task=%+v", expired)
	}
	if err := expired.Expire(now); err != nil {
		t.Fatalf("repeat Expire: %v", err)
	}
	if err := expired.Decide(CertificationDecisionConfirm, "1001", "确认", now); !errors.Is(err, ErrCertificationNotPending) {
		t.Fatalf("decide expired err=%v, want not pending", err)
	}

	skipped := certTask(t, func(p *NewCertificationTaskParams) { p.ID = "9102" })
	if err := skipped.MarkSkipped("system:certification", "绑定已变化", now); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if skipped.Status != CertificationTaskStatusSkipped || skipped.DecidedBy != "system:certification" {
		t.Fatalf("skipped task=%+v", skipped)
	}
	if err := skipped.MarkSkipped("system:certification", "绑定已变化", now); err != nil {
		t.Fatalf("repeat MarkSkipped: %v", err)
	}
}

func TestBindingFingerprintTracksMaterialFields(t *testing.T) {
	expires := time.Now().UTC().Add(24 * time.Hour)
	binding := &Binding{
		Channel:     ChannelAdmin,
		SubjectType: SubjectStaff,
		SubjectID:   "2001",
		RoleID:      "3001",
		ExpiresAt:   &expires,
	}
	first := BindingFingerprint(binding)
	if first == "" || first != BindingFingerprint(binding) {
		t.Fatalf("fingerprint must be stable, got %q", first)
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint length=%d, want sha256 hex 64", len(first))
	}
	changed := *binding
	later := expires.Add(time.Hour)
	changed.ExpiresAt = &later
	if BindingFingerprint(&changed) == first {
		t.Fatal("expiry change must change fingerprint")
	}
	revokedAt := time.Now().UTC()
	revoked := *binding
	revoked.RevokedAt = &revokedAt
	if BindingFingerprint(&revoked) == first {
		t.Fatal("revoked change must change fingerprint")
	}
	if BindingFingerprint(nil) != "" {
		t.Fatal("nil binding fingerprint must be empty")
	}
}
