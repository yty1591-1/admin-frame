package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestEncodeCertificationRoleIDs(t *testing.T) {
	payload, err := encodeCertificationRoleIDs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "[]" {
		t.Fatalf("empty payload=%s, want []", payload)
	}
	payload, err = encodeCertificationRoleIDs([]string{"3001", "3002"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded []string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 2 || decoded[0] != "3001" || decoded[1] != "3002" {
		t.Fatalf("decoded=%v", decoded)
	}
}

func TestCertificationCampaignFilterClause(t *testing.T) {
	clause, args := certificationCampaignFilterClause(domain.CertificationCampaignFilter{
		Channel: domain.ChannelBiz, MerchantID: "m_01", Status: domain.CertificationStatusActive,
	})
	if clause != " AND channel = ? AND merchant_id = ? AND status = ?" {
		t.Fatalf("clause=%q", clause)
	}
	if len(args) != 3 || args[0] != "biz" || args[1] != "m_01" || args[2] != "active" {
		t.Fatalf("args=%v", args)
	}
	clause, args = certificationCampaignFilterClause(domain.CertificationCampaignFilter{})
	if clause != "" || len(args) != 0 {
		t.Fatalf("empty clause=%q args=%v", clause, args)
	}
}

// errScanRow 模拟数据库扫描错误，验证仓储把未知错误原样上抛而非吞掉。
type errScanRow struct{ err error }

func (r errScanRow) Scan(...any) error { return r.err }

func TestScanCertificationCampaignPropagatesError(t *testing.T) {
	want := errors.New("boom")
	if _, err := scanCertificationCampaign(errScanRow{err: want}); !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

// TestSQLCertificationRepositoryUnavailable 验证未配置数据库时返回 50300 而不是 panic。
func TestSQLCertificationRepositoryUnavailable(t *testing.T) {
	repo := NewSQLCertificationCampaignRepository(nil)
	if _, err := repo.FindByID(context.Background(), "7001"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("FindByID err=%v, want unavailable", err)
	}
	taskRepo := NewSQLCertificationTaskRepository(nil)
	if err := taskRepo.SaveAll(context.Background(), nil); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("SaveAll err=%v, want unavailable", err)
	}
}
