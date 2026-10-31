package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// TestMemorySessionListActiveByFamilyFiltersAndCaps 验证撤销链路只读取可能存活的会话（PERF-03）。
func TestMemorySessionListActiveByFamilyFiltersAndCaps(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySessionRepository()
	activeSince := repoBaseTime.Add(-time.Hour)

	recentActive := mustSession(t, "s-active", "fam-1", "hash-active", repoBaseTime)
	if err := repo.Save(ctx, recentActive, 0); err != nil {
		t.Fatalf("save active: %v", err)
	}

	oldRotated := mustSession(t, "s-old-rotated", "fam-1", "hash-old", repoBaseTime.Add(-2*time.Hour))
	if err := oldRotated.Rotate(repoBaseTime.Add(-2 * time.Hour)); err != nil {
		t.Fatalf("rotate old: %v", err)
	}
	if err := repo.Save(ctx, oldRotated, 0); err != nil {
		t.Fatalf("save old rotated: %v", err)
	}

	revoked := mustSession(t, "s-revoked", "fam-1", "hash-revoked", repoBaseTime)
	if err := revoked.Revoke(domain.RevokeReasonLogout, repoBaseTime); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := repo.Save(ctx, revoked, 0); err != nil {
		t.Fatalf("save revoked: %v", err)
	}

	result, err := repo.ListActiveByFamily(ctx, "fam-1", activeSince)
	if err != nil {
		t.Fatalf("ListActiveByFamily() error = %v", err)
	}
	if len(result) != 1 || result[0].ID != recentActive.ID {
		t.Fatalf("result = %+v, want only %s", result, recentActive.ID)
	}

	// 异常高频刷新导致单链膨胀时，实现必须用硬上限保护内存与响应大小。
	for i := 0; i < maxActiveSessionsPerFamily+10; i++ {
		session := mustSession(t, fmt.Sprintf("s-bulk-%d", i), "fam-bulk", fmt.Sprintf("hash-bulk-%d", i), repoBaseTime)
		if err := repo.Save(ctx, session, 0); err != nil {
			t.Fatalf("save bulk %d: %v", i, err)
		}
	}
	bulk, err := repo.ListActiveByFamily(ctx, "fam-bulk", activeSince)
	if err != nil {
		t.Fatalf("ListActiveByFamily(bulk) error = %v", err)
	}
	if len(bulk) != maxActiveSessionsPerFamily {
		t.Fatalf("bulk len = %d, want hard cap %d", len(bulk), maxActiveSessionsPerFamily)
	}
}
