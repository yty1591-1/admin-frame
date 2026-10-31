package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// cappedFamilyRepo 模拟 SQL 仓储把 limit<=0 / 超限归一为 20 的行为。
type cappedFamilyRepo struct {
	domain.SessionRepository
	families []*domain.SessionFamily
}

func (r *cappedFamilyRepo) ListFamiliesByAccount(
	_ context.Context, _ string, afterLastUsed time.Time, afterID string, limit int,
) ([]*domain.SessionFamily, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	start := 0
	if !afterLastUsed.IsZero() {
		for i, family := range r.families {
			if family.LastUsedAt.Before(afterLastUsed) || (family.LastUsedAt.Equal(afterLastUsed) && family.ID < afterID) {
				start = i
				break
			}
			start = i + 1
		}
	}
	end := start + limit
	if end > len(r.families) {
		end = len(r.families)
	}
	if start > len(r.families) {
		start = len(r.families)
	}
	return r.families[start:end], nil
}

// TestListAllFamiliesPaginatesBeyondRepositoryCap 验证设备淘汰扫描不受仓储 20 台归一化影响（PERF-04）。
func TestListAllFamiliesPaginatesBeyondRepositoryCap(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	families := make([]*domain.SessionFamily, 0, 25)
	for i := 0; i < 25; i++ {
		family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
			ID:        fmt.Sprintf("%03d", 100-i),
			AccountID: "acct-1",
			ExpiresAt: now.Add(time.Hour),
			Now:       now.Add(-time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("new family: %v", err)
		}
		families = append(families, family)
	}
	repo := &cappedFamilyRepo{families: families}
	all, err := listAllFamilies(context.Background(), repo, "acct-1")
	if err != nil {
		t.Fatalf("list all families: %v", err)
	}
	if len(all) != len(families) {
		t.Fatalf("families = %d, want %d (must paginate beyond 20)", len(all), len(families))
	}
}
