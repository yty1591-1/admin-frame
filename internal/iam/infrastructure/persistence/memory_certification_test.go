package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// newTestCampaign 构造活动仓储测试数据。
func newTestCampaign(t *testing.T, id string) *domain.CertificationCampaign {
	t.Helper()
	campaign, err := domain.NewCertificationCampaign(domain.NewCertificationCampaignParams{
		ID:        id,
		Name:      "季度复核",
		Channel:   domain.ChannelAdmin,
		DueAt:     time.Now().UTC().Add(24 * time.Hour),
		CreatedBy: "1001",
	})
	if err != nil {
		t.Fatalf("NewCertificationCampaign: %v", err)
	}
	return campaign
}

// newTestTask 构造任务仓储测试数据。
func newTestTask(t *testing.T, id, campaignID, subjectID, roleID string) *domain.CertificationTask {
	t.Helper()
	task, err := domain.NewCertificationTask(domain.NewCertificationTaskParams{
		ID:                 id,
		CampaignID:         campaignID,
		SubjectType:        domain.SubjectStaff,
		SubjectID:          subjectID,
		RoleID:             roleID,
		BindingID:          "4001",
		BindingFingerprint: "fingerprint",
		ReviewerID:         "1001",
	})
	if err != nil {
		t.Fatalf("NewCertificationTask: %v", err)
	}
	return task
}

func TestMemoryCertificationCampaignRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryCertificationCampaignRepository()
	campaign := newTestCampaign(t, "7001")

	if err := repo.Save(ctx, campaign, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(ctx, campaign, 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate create err=%v, want conflict", err)
	}
	loaded, err := repo.FindByID(ctx, "7001")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Name != campaign.Name || loaded.Version != 1 {
		t.Fatalf("loaded=%+v", loaded)
	}

	loaded.Status = domain.CertificationStatusActive
	loaded.Version = 2
	if err := repo.Save(ctx, loaded, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := repo.Save(ctx, loaded, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale version err=%v, want conflict", err)
	}

	list, err := repo.List(ctx, domain.CertificationCampaignFilter{
		Channel: domain.ChannelAdmin, Status: domain.CertificationStatusActive, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "7001" {
		t.Fatalf("list=%+v", list)
	}
	if total, err := repo.Count(ctx, domain.CertificationCampaignFilter{Channel: domain.ChannelAdmin}); err != nil || total != 1 {
		t.Fatalf("Count total=%d err=%v", total, err)
	}
	// B 端过滤不得看到 A 端活动。
	if list, err := repo.List(ctx, domain.CertificationCampaignFilter{
		Channel: domain.ChannelBiz, MerchantID: "m_01",
	}); err != nil || len(list) != 0 {
		t.Fatalf("biz list=%+v err=%v", list, err)
	}
}

func TestMemoryCertificationTaskRepositorySaveAllIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryCertificationTaskRepository()
	first := newTestTask(t, "8001", "7001", "2001", "3001")
	if err := repo.SaveAll(ctx, []*domain.CertificationTask{first}); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	// 同一 (campaign, subject, role) 的重复生成必须保留原任务主键。
	duplicate := newTestTask(t, "8002", "7001", "2001", "3001")
	if err := repo.SaveAll(ctx, []*domain.CertificationTask{duplicate}); err != nil {
		t.Fatalf("duplicate SaveAll: %v", err)
	}
	loaded, err := repo.FindByID(ctx, "8001")
	if err != nil {
		t.Fatalf("FindByID original: %v", err)
	}
	if _, err := repo.FindByID(ctx, "8002"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("duplicate id must not be created, err=%v", err)
	}
	if loaded.Status != domain.CertificationTaskStatusPending {
		t.Fatalf("original task mutated: %+v", loaded)
	}

	second := newTestTask(t, "8003", "7001", "2002", "3001")
	if err := repo.SaveAll(ctx, []*domain.CertificationTask{second}); err != nil {
		t.Fatalf("SaveAll second: %v", err)
	}
	tasks, err := repo.ListByCampaign(ctx, "7001")
	if err != nil {
		t.Fatalf("ListByCampaign: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != "8001" || tasks[1].ID != "8003" {
		t.Fatalf("tasks=%+v", tasks)
	}
	if total, err := repo.CountByCampaign(ctx, "7001"); err != nil || total != 2 {
		t.Fatalf("count=%d err=%v", total, err)
	}

	page, err := repo.ListPage(ctx, domain.CertificationTaskFilter{
		CampaignID: "7001", Status: domain.CertificationTaskStatusPending, Limit: 1, Offset: 1,
	})
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page) != 1 || page[0].ID != "8003" {
		t.Fatalf("page=%+v", page)
	}

	loaded.Status = domain.CertificationTaskStatusConfirmed
	loaded.Version = 2
	if err := repo.Save(ctx, loaded, 1); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	if err := repo.Save(ctx, loaded, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale task err=%v, want conflict", err)
	}
}
