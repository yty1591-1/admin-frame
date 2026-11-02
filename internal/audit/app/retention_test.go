package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// fakeRetentionStore 是可编程的保留期仓储：按固定行数模拟分批删除。
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

// DeleteBefore 按 limit 递减剩余行数并记录批次大小。
func (s *fakeRetentionStore) DeleteBefore(_ context.Context, _ time.Time, limit int) (int, error) {
	if s.remaining <= 0 {
		return 0, nil
	}
	deleted := limit
	if deleted > s.remaining {
		deleted = s.remaining
	}
	s.remaining -= deleted
	s.batchLog = append(s.batchLog, deleted)
	return deleted, nil
}

// TestRetentionPurgeBatchesAndAudits 验证分批删除、行数统计与清理元审计。
func TestRetentionPurgeBatchesAndAudits(t *testing.T) {
	store := &fakeRetentionStore{remaining: 250, purgeable: 250}
	emitter := &recordingEmitter{}
	svc, err := app.NewRetentionService(store, emitter, &seqIDs{}, nil, app.RetentionOptions{
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
			svc, err := app.NewRetentionService(store, emitter, &seqIDs{}, nil, tc.opts, "worker")
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
	if _, err := app.NewRetentionService(&fakeRetentionStore{}, nil, &seqIDs{}, nil, app.RetentionOptions{
		RetentionDays: 0,
	}, "worker"); err == nil {
		t.Fatal("NewRetentionService() with retention_days=0: want error, got nil")
	}
	if _, err := app.NewRetentionService(nil, nil, &seqIDs{}, nil, app.RetentionOptions{
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
