package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// cleanupSessions 只覆盖清理相关方法，其余接口方法不会被调用。
type cleanupSessions struct {
	domain.SessionRepository
	expire              []domain.SessionExpireResult
	deleteBatch         []domain.SessionDeleteResult
	archiveBatch        []domain.SessionDeleteResult
	deleteArchivedBatch []domain.SessionDeleteResult
	expireCalls         int
	deleteCalls         int
	archiveCalls        int
	deleteArchivedCalls int
}

func (s *cleanupSessions) ExpireStale(context.Context, time.Time, int) (domain.SessionExpireResult, error) {
	result := s.expire[s.expireCalls]
	s.expireCalls++
	return result, nil
}

func (s *cleanupSessions) DeleteExpiredBefore(context.Context, time.Time, int) (domain.SessionDeleteResult, error) {
	result := s.deleteBatch[s.deleteCalls]
	s.deleteCalls++
	return result, nil
}

func (s *cleanupSessions) ArchiveExpiredBefore(context.Context, time.Time, int, time.Time) (domain.SessionDeleteResult, error) {
	result := s.archiveBatch[s.archiveCalls]
	s.archiveCalls++
	return result, nil
}

func (s *cleanupSessions) DeleteArchivedBefore(context.Context, time.Time, int) (domain.SessionDeleteResult, error) {
	result := s.deleteArchivedBatch[s.deleteArchivedCalls]
	s.deleteArchivedCalls++
	return result, nil
}

// cleanupAttempts 只覆盖 DeleteBefore。
type cleanupAttempts struct {
	domain.LoginAttemptRepository
	results []int
	calls   int
}

func (a *cleanupAttempts) DeleteBefore(context.Context, time.Time, int) (int, error) {
	result := a.results[a.calls]
	a.calls++
	return result, nil
}

// TestSessionCleanupLoopsUntilDrained 验证清理按批循环直到本批未满。
func TestSessionCleanupLoopsUntilDrained(t *testing.T) {
	sessions := &cleanupSessions{
		expire:      []domain.SessionExpireResult{{Sessions: 2, Families: 2}, {Sessions: 2, Families: 2}, {}},
		deleteBatch: []domain.SessionDeleteResult{{Sessions: 2, Families: 1}, {Sessions: 2, Families: 1}, {}},
	}
	attempts := &cleanupAttempts{results: []int{2, 2, 0}}
	cleanup, err := app.NewSessionCleanup(sessions, attempts, app.RetentionPolicy{
		SessionRetention:      time.Hour,
		LoginAttemptRetention: time.Hour,
		// 负值表示关闭归档，验证回滚路径仍保持原有「只删不归档」行为。
		ArchiveRetention: -time.Hour,
		BatchSize:        2,
		MaxBatchesPerRun: 3,
	}, nil)
	if err != nil {
		t.Fatalf("new cleanup: %v", err)
	}
	stats, err := cleanup.Run(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if stats.ExpiredSessions != 4 || stats.ExpiredFamilies != 4 {
		t.Fatalf("expired stats = %+v, want 4/4", stats)
	}
	if stats.DeletedSessions != 4 || stats.DeletedFamilies != 2 {
		t.Fatalf("deleted stats = %+v, want 4/2", stats)
	}
	if stats.DeletedLoginAttempts != 4 {
		t.Fatalf("deleted attempts = %d, want 4", stats.DeletedLoginAttempts)
	}
	if sessions.expireCalls != 3 || sessions.deleteCalls != 3 || attempts.calls != 3 {
		t.Fatalf("calls = expire:%d delete:%d attempts:%d, want 3/3/3",
			sessions.expireCalls, sessions.deleteCalls, attempts.calls)
	}
}

// TestSessionCleanupArchivesAndPrunesArchive 验证默认路径按批归档并清理过期归档。
func TestSessionCleanupArchivesAndPrunesArchive(t *testing.T) {
	sessions := &cleanupSessions{
		expire:              []domain.SessionExpireResult{{}},
		archiveBatch:        []domain.SessionDeleteResult{{Sessions: 2, Families: 1}, {Sessions: 2, Families: 1}, {}},
		deleteArchivedBatch: []domain.SessionDeleteResult{{Sessions: 1}, {}},
	}
	attempts := &cleanupAttempts{results: []int{0}}
	cleanup, err := app.NewSessionCleanup(sessions, attempts, app.RetentionPolicy{
		SessionRetention:      time.Hour,
		LoginAttemptRetention: time.Hour,
		ArchiveRetention:      24 * time.Hour,
		BatchSize:             2,
		MaxBatchesPerRun:      3,
	}, nil)
	if err != nil {
		t.Fatalf("new cleanup: %v", err)
	}
	stats, err := cleanup.Run(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if stats.ArchivedSessions != 4 || stats.ArchivedFamilies != 2 {
		t.Fatalf("archive stats = %+v, want 4/2", stats)
	}
	if stats.DeletedArchiveSessions != 1 || stats.DeletedArchiveFamilies != 0 {
		t.Fatalf("archive prune stats = %+v, want 1/0", stats)
	}
	if stats.DeletedSessions != 0 || sessions.deleteCalls != 0 {
		t.Fatalf("delete path must not run when archive enabled: stats=%+v calls=%d", stats, sessions.deleteCalls)
	}
	if sessions.archiveCalls != 3 || sessions.deleteArchivedCalls != 1 {
		t.Fatalf("archive calls = %d/%d, want 3/1", sessions.archiveCalls, sessions.deleteArchivedCalls)
	}
}
