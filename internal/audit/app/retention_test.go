package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// fakeRetentionStore 是可编程的保留期仓储：按固定行数模拟分批取数 / 删除。
type fakeRetentionStore struct {
	remaining  int
	purgeable  int
	batchLog   []int
	countCalls int
}

// CountBefore 返回预置到期行数。
func (s *fakeRetentionStore) CountBefore(_ context.Context, _ time.Time) (int, error) {
	s.countCalls++
	return s.purgeable, nil
}

// ListBefore 取回最多 limit 条到期事件（内容只需唯一 ID，供归档与删除对账）。
func (s *fakeRetentionStore) ListBefore(_ context.Context, cutoff time.Time, limit int) ([]audit.Event, error) {
	if s.remaining <= 0 {
		return nil, nil
	}
	count := limit
	if count > s.remaining {
		count = s.remaining
	}
	events := make([]audit.Event, 0, count)
	for i := 0; i < count; i++ {
		events = append(events, audit.Event{
			EventID:    "expired-" + time.Duration(s.remaining-i).String(),
			Action:     audit.ActionAuthLogin,
			OccurredAt: cutoff.Add(-time.Hour),
		})
	}
	return events, nil
}

// DeleteBefore 按 limit 递减剩余行数（保留给直接调用方的旧路径）。
func (s *fakeRetentionStore) DeleteBefore(_ context.Context, _ time.Time, limit int) (int, error) {
	return s.DeleteIDs(context.Background(), make([]string, minInt(limit, s.remaining)))
}

// DeleteIDs 按 ID 删除并记录批次大小。
func (s *fakeRetentionStore) DeleteIDs(_ context.Context, ids []string) (int, error) {
	if s.remaining <= 0 || len(ids) == 0 {
		return 0, nil
	}
	deleted := len(ids)
	if deleted > s.remaining {
		deleted = s.remaining
	}
	s.remaining -= deleted
	s.batchLog = append(s.batchLog, deleted)
	return deleted, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRetentionPurgeBatchesAndAudits 验证分批删除、行数统计与清理元审计。
func TestRetentionPurgeBatchesAndAudits(t *testing.T) {
	store := &fakeRetentionStore{remaining: 250, purgeable: 250}
	emitter := &recordingEmitter{}
	svc, err := app.NewRetentionService(store, nil, emitter, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 180,
		BatchSize:     100,
		MaxBatches:    10,
	}, "worker")
	if err != nil {
		t.Fatalf("NewRetentionService() error = %v", err)
	}

	result, err := svc.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce() error = %v", err)
	}
	if result.Deleted != 250 {
		t.Fatalf("Deleted = %d, want 250", result.Deleted)
	}
	if result.Batches != 3 {
		t.Fatalf("Batches = %d, want 3 (100+100+50)", result.Batches)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Action != audit.ActionAuditRetentionPurge {
		t.Fatalf("audit action = %s, want %s", event.Action, audit.ActionAuditRetentionPurge)
	}
	if event.Channel != "worker" || event.ActorID != "system" {
		t.Fatalf("audit actor/channel = %s/%s, want system/worker", event.ActorID, event.Channel)
	}
	if event.Attrs["deleted"] != "250" {
		t.Fatalf("audit attrs deleted = %q, want 250", event.Attrs["deleted"])
	}
}

// TestRetentionDryRunAndLegalHoldSkipDelete 验证演练与法务保留只统计不删除。
func TestRetentionDryRunAndLegalHoldSkipDelete(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts app.RetentionOptions
	}{
		{name: "dry run", opts: app.RetentionOptions{RetentionDays: 180, BatchSize: 100, DryRun: true}},
		{name: "legal hold", opts: app.RetentionOptions{RetentionDays: 180, BatchSize: 100, LegalHold: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRetentionStore{remaining: 500, purgeable: 500}
			emitter := &recordingEmitter{}
			svc, err := app.NewRetentionService(store, nil, emitter, &seqIDs{}, nil, tc.opts, "worker")
			if err != nil {
				t.Fatalf("NewRetentionService() error = %v", err)
			}

			result, err := svc.PurgeOnce(context.Background())
			if err != nil {
				t.Fatalf("PurgeOnce() error = %v", err)
			}
			if result.Deleted != 0 {
				t.Fatalf("Deleted = %d, want 0", result.Deleted)
			}
			if result.Purgeable != 500 {
				t.Fatalf("Purgeable = %d, want 500", result.Purgeable)
			}
			if len(store.batchLog) != 0 {
				t.Fatalf("delete called %d times, want 0", len(store.batchLog))
			}
			if len(emitter.events) != 0 {
				t.Fatalf("audit events = %d, want 0 (no deletion, no meta audit)", len(emitter.events))
			}
		})
	}
}

// TestRetentionRejectsInvalidOptions 验证非法保留天数在构造期即被拒绝。
func TestRetentionRejectsInvalidOptions(t *testing.T) {
	if _, err := app.NewRetentionService(&fakeRetentionStore{}, nil, nil, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 0,
	}, "worker"); err == nil {
		t.Fatal("NewRetentionService() with retention_days=0: want error, got nil")
	}
	if _, err := app.NewRetentionService(nil, nil, nil, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 180,
	}, "worker"); err == nil {
		t.Fatal("NewRetentionService() without store: want error, got nil")
	}
}

// 编译期断言：假实现满足领域契约。
var (
	_ domain.Reader         = (*fakeReader)(nil)
	_ domain.RetentionStore = (*fakeRetentionStore)(nil)
)

// fakeArchiver 记录归档批次，可注入失败以验证「归档失败不删除」。
type fakeArchiver struct {
	calls int
	rows  []int
	fail  bool
}

func (a *fakeArchiver) Archive(_ context.Context, events []audit.Event, _ time.Time) (string, error) {
	a.calls++
	a.rows = append(a.rows, len(events))
	if a.fail {
		return "", errors.New("object store unavailable")
	}
	return "audit/expired/test.csv", nil
}

// TestRetentionArchivesBeforeDelete 验证每批先归档再删除，且归档行数与删除行数一致。
func TestRetentionArchivesBeforeDelete(t *testing.T) {
	store := &fakeRetentionStore{remaining: 250, purgeable: 250}
	archiver := &fakeArchiver{}
	svc, err := app.NewRetentionService(store, archiver, &recordingEmitter{}, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 180,
		BatchSize:     100,
		MaxBatches:    10,
	}, "worker")
	if err != nil {
		t.Fatalf("NewRetentionService() error = %v", err)
	}

	result, err := svc.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce() error = %v", err)
	}
	if result.Archived != 250 {
		t.Fatalf("Archived = %d, want 250", result.Archived)
	}
	if result.Deleted != 250 {
		t.Fatalf("Deleted = %d, want 250", result.Deleted)
	}
	if archiver.calls != 3 || len(archiver.rows) != 3 {
		t.Fatalf("archive calls = %d rows=%v, want 3 batches", archiver.calls, archiver.rows)
	}
	if archiver.rows[0] != 100 || archiver.rows[2] != 50 {
		t.Fatalf("archive batch sizes = %v, want [100 100 50]", archiver.rows)
	}
}

// TestRetentionArchiveFailureKeepsData 验证归档失败即中止：一行都不删，也不写「已删除」元审计。
func TestRetentionArchiveFailureKeepsData(t *testing.T) {
	store := &fakeRetentionStore{remaining: 250, purgeable: 250}
	emitter := &recordingEmitter{}
	svc, err := app.NewRetentionService(store, &fakeArchiver{fail: true}, emitter, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 180,
		BatchSize:     100,
		MaxBatches:    10,
	}, "worker")
	if err != nil {
		t.Fatalf("NewRetentionService() error = %v", err)
	}

	if _, err := svc.PurgeOnce(context.Background()); err == nil {
		t.Fatal("PurgeOnce() with failing archiver: want error, got nil")
	}
	if store.remaining != 250 {
		t.Fatalf("remaining = %d, want 250（归档失败不得删除）", store.remaining)
	}
	if len(store.batchLog) != 0 {
		t.Fatalf("delete called %d times, want 0", len(store.batchLog))
	}
	if len(emitter.events) != 0 {
		t.Fatalf("audit events = %d, want 0", len(emitter.events))
	}
}
