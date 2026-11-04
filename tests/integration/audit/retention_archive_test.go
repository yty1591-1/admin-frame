package audit_test

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// TestAuditRetentionArchivesThenDeletes 验证 AUD-07 的「到期归档销毁」：
// 每批先写归档对象再删行，归档总行数与删除行数一致，未到期数据不受影响。
func TestAuditRetentionArchivesThenDeletes(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	objects := testkit.Storage(t)
	clearExportTables(t, sqlDB)

	now := time.Now().UTC()
	longAgo := now.AddDate(0, 0, -400)
	emitter := audit.SQLEmitter{DB: sqlDB}
	const expired, kept = 12, 3
	for i := 0; i < expired; i++ {
		event := seedEvent("old-"+strconv.Itoa(i), audit.ActionAuthLogin, "admin-1", longAgo.Add(time.Duration(i)*time.Minute))
		if err := emitter.Emit(ctx, event); err != nil {
			t.Fatalf("seed expired %d: %v", i, err)
		}
	}
	for i := 0; i < kept; i++ {
		event := seedEvent("new-"+strconv.Itoa(i), audit.ActionAuthLogin, "admin-1", now.Add(-time.Duration(i)*time.Minute))
		if err := emitter.Emit(ctx, event); err != nil {
			t.Fatalf("seed recent %d: %v", i, err)
		}
	}

	store, err := persistence.NewSQLRetentionStore(sqlDB, "")
	if err != nil {
		t.Fatalf("NewSQLRetentionStore() error = %v", err)
	}
	// 每次运行用唯一前缀：MinIO 桶是持久卷，固定前缀会把上次运行的对象算进来。
	prefix := "testkit/audit-expired-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		cleanupPrefix(context.Background(), objects, prefix)
	})
	svc, err := app.NewRetentionService(store, app.ObjectArchiver{
		Objects: objects,
		Prefix:  prefix,
	}, audit.SQLEmitter{DB: sqlDB}, &seqIDs{next: time.Now().UnixNano()}, nil, app.RetentionOptions{
		RetentionDays: 180,
		BatchSize:     5, // 12 行 → 5 + 5 + 2 三个归档对象
		MaxBatches:    10,
	}, "worker")
	if err != nil {
		t.Fatalf("NewRetentionService() error = %v", err)
	}

	result, err := svc.PurgeOnce(ctx)
	if err != nil {
		t.Fatalf("PurgeOnce() error = %v", err)
	}
	if result.Purgeable != expired || result.Archived != expired || result.Deleted != expired {
		t.Fatalf("result = %+v, want purgeable=archived=deleted=%d", result, expired)
	}
	if result.Batches != 3 {
		t.Fatalf("Batches = %d, want 3", result.Batches)
	}

	// 归档对象内容必须能还原出被删除的全部事件行数。
	archivedRows := 0
	listed, err := objects.List(ctx, storage.ListOptions{Prefix: prefix, Limit: 100})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed.Items) != 3 {
		t.Fatalf("归档对象 = %d 个, want 3", len(listed.Items))
	}
	for _, item := range listed.Items {
		archivedRows += countCSVRows(ctx, t, objects, item.Key)
	}
	if archivedRows != expired {
		t.Fatalf("归档行数 = %d, want %d（归档内容与删除行数不符）", archivedRows, expired)
	}

	// 未到期数据必须保留。
	remaining := countByAction(t, sqlDB, audit.ActionAuthLogin, now.Add(-24*time.Hour), now.Add(time.Minute))
	if remaining != kept {
		t.Fatalf("剩余行数 = %d, want %d", remaining, kept)
	}
}

// countCSVRows 读取归档对象的数据行数（不含表头）。
func countCSVRows(ctx context.Context, t *testing.T, objects storage.Store, key string) int {
	t.Helper()
	rc, _, err := objects.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%s) error = %v", key, err)
	}
	defer func() {
		_ = rc.Close()
	}()
	payload, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	records, err := csv.NewReader(strings.NewReader(string(payload))).ReadAll()
	if err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}
	if len(records) == 0 {
		t.Fatalf("%s 为空文件", key)
	}
	if records[0][0] != "event_id" {
		t.Fatalf("%s 表头 = %v, want event_id 开头", key, records[0])
	}
	return len(records) - 1
}

// cleanupPrefix 删除测试前缀下的全部对象，避免污染持久化的测试桶。
func cleanupPrefix(ctx context.Context, objects storage.Store, prefix string) {
	listed, err := objects.List(ctx, storage.ListOptions{Prefix: prefix, Limit: 1000})
	if err != nil {
		return
	}
	for _, item := range listed.Items {
		_ = objects.Delete(ctx, item.Key)
	}
}
