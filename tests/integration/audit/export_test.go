package audit_test

import (
	"context"
	"database/sql"
	"encoding/csv"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/audit/infrastructure/persistence"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/testdb"
	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// seqIDs 是测试用单调 ID，保证导出 ID 可预测且不与库内既有行冲突。
type seqIDs struct {
	mu   sync.Mutex
	next int64
}

func (g *seqIDs) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return g.next, nil
}

func (g *seqIDs) NextString() (string, error) {
	value, err := g.Next()
	return strconv.FormatInt(value, 10), err
}

// exportWindow 返回一个包含「刚刚」的查询窗口，避免与库内历史数据的时间范围重叠。
//
// Limit 只用于通过 Filter.Validate（必须为正）；异步导出执行时会按
// audit.export.max_rows 重新收敛，入库快照不持久化 Limit。
func exportWindow(now time.Time) domain.Filter {
	return domain.Filter{From: now.Add(-time.Hour), To: now.Add(time.Minute), Limit: 1}
}

// TestAuditExportJobEndToEnd 覆盖 ENT-04 T5 的完整链路：
// 建任务 → 真实 MySQL 抢锁执行 → 真实对象存储落 CSV → 下载 → 与库内行数对账。
func TestAuditExportJobEndToEnd(t *testing.T) {
	ctx := context.Background()
	sqlDB := testdb.MySQL(t).SQL()
	objects := testkit.Storage(t)
	clearExportTables(t, sqlDB)

	now := time.Now().UTC()
	emitter := platformaudit.SQLEmitter{DB: sqlDB}
	const seeded = 25
	for i := 0; i < seeded; i++ {
		event := seedEvent("exp-"+strconv.Itoa(i), audit.ActionAuthLogin, "admin-1", now.Add(-time.Duration(i)*time.Minute))
		if err := emitter.Emit(ctx, event); err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}

	svc := newExportService(t, sqlDB, objects, seeded+10, 2)

	filter := exportWindow(now)
	// 只导出 auth.login：导出自身产生的 data.export 审计不会落进同一份结果，
	// 行数因此可精确对账（否则第二个任务的行数会随审计写入而漂移）。
	filter.Action = audit.ActionAuthLogin
	job, err := svc.Create(ctx, filter, app.AccessMeta{ActorID: "admin-1", Channel: "admin", TraceID: "trace-e2e"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 两个并发 worker 同时抢占：SKIP LOCKED 必须让它们拿到不同任务而不是同一个。
	second, err := svc.Create(ctx, filter, app.AccessMeta{ActorID: "admin-2", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	runConcurrent(t, svc, 2)

	for _, id := range []string{job.ID, second.ID} {
		stored, err := svc.Get(ctx, id, app.AccessMeta{ActorID: actorOf(id, job.ID), Channel: "admin"})
		if err != nil {
			t.Fatalf("Get(%s) error = %v", id, err)
		}
		if stored.Status != domain.ExportSucceeded {
			t.Fatalf("job %s status = %s (last error %q), want succeeded", id, stored.Status, stored.LastError)
		}
		if stored.RowCount != seeded {
			t.Fatalf("job %s rows = %d, want %d", id, stored.RowCount, seeded)
		}
		if stored.ObjectKey == "" {
			t.Fatalf("job %s object key is empty", id)
		}
	}

	// 下载内容必须与库内真实行数一致（不只是「有文件」）。
	downloaded, body, err := svc.Open(ctx, job.ID, app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if downloaded.ID != job.ID {
		t.Fatalf("Open() job = %s, want %s", downloaded.ID, job.ID)
	}
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != seeded+1 {
		t.Fatalf("csv rows = %d (含表头), want %d", len(records), seeded+1)
	}
	if records[0][0] != "event_id" || records[0][2] != "action" {
		t.Fatalf("csv header = %v", records[0])
	}
	dbRows := countByAction(t, sqlDB, audit.ActionAuthLogin, filter.From, filter.To)
	if got := len(records) - 1; got != dbRows {
		t.Fatalf("csv 数据行 = %d, 库内 = %d（对账失败）", got, dbRows)
	}

	// 成功导出必须留审计：动作 data.export、mode=async、rows 与任务一致。
	asyncEvents := countExportEvents(t, sqlDB, "async")
	if asyncEvents == 0 {
		t.Fatal("缺少 data.export(mode=async) 审计事件")
	}
	// 下载另记一条 async-download，与「生成」区分开。
	if got := countExportEvents(t, sqlDB, "async-download"); got != 1 {
		t.Fatalf("async-download 审计事件 = %d, want 1", got)
	}

	// 到期清理：对象删除 + 状态置 expired，两者必须同时成立。
	if _, err := sqlDB.ExecContext(ctx,
		`UPDATE audit_export_job SET expires_at = ? WHERE export_id = ?`,
		now.Add(-time.Minute), job.ID); err != nil {
		t.Fatalf("expire job: %v", err)
	}
	expired, err := svc.ExpireOnce(ctx)
	if err != nil {
		t.Fatalf("ExpireOnce() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	exists, err := objects.Exists(ctx, downloaded.ObjectKey)
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Fatalf("对象 %s 到期后仍存在", downloaded.ObjectKey)
	}
	afterExpire := loadStatus(t, sqlDB, job.ID)
	if afterExpire != string(domain.ExportExpired) {
		t.Fatalf("status = %s, want expired", afterExpire)
	}
}

// TestAuditExportJobStaleRecovery 覆盖 worker 崩溃恢复：
// 未用尽重试的任务回 pending，已用尽的重试直接终态失败（避免毒丸饿死队列）。
func TestAuditExportJobStaleRecovery(t *testing.T) {
	ctx := context.Background()
	sqlDB := testdb.MySQL(t).SQL()
	clearExportTables(t, sqlDB)
	now := time.Now().UTC()
	svc := newExportService(t, sqlDB, testkit.Storage(t), 100, 2)

	poison, err := svc.Create(ctx, exportWindow(now), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	retryable, err := svc.Create(ctx, exportWindow(now), app.AccessMeta{ActorID: "admin-2", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stuck := now.Add(-time.Hour)
	for _, item := range []struct {
		id       string
		attempts int
	}{{poison.ID, 2}, {retryable.ID, 1}} {
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE audit_export_job SET status = 'running', started_at = ?, attempts = ? WHERE export_id = ?`,
			stuck, item.attempts, item.id); err != nil {
			t.Fatalf("mark running %s: %v", item.id, err)
		}
	}

	requeued, err := svc.RequeueStale(ctx)
	if err != nil {
		t.Fatalf("RequeueStale() error = %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1", requeued)
	}
	if got := loadStatus(t, sqlDB, poison.ID); got != string(domain.ExportFailed) {
		t.Fatalf("poison status = %s, want failed", got)
	}
	if got := loadStatus(t, sqlDB, retryable.ID); got != string(domain.ExportPending) {
		t.Fatalf("retryable status = %s, want pending", got)
	}
}

// newExportService 构造直连真实 MySQL + MinIO 的导出服务。
func newExportService(t *testing.T, sqlDB *sql.DB, objects storage.Store, maxRows, retryMax int) *app.ExportJobService {
	t.Helper()
	reader, err := persistence.NewSQLReader(sqlDB, "")
	if err != nil {
		t.Fatalf("NewSQLReader() error = %v", err)
	}
	store, err := persistence.NewSQLExportStore(sqlDB, "")
	if err != nil {
		t.Fatalf("NewSQLExportStore() error = %v", err)
	}
	svc, err := app.NewExportJobService(app.ExportJobConfig{
		Store:   store,
		Reader:  reader,
		Objects: objects,
		Emitter: platformaudit.SQLEmitter{DB: sqlDB},
		IDs:     &seqIDs{next: time.Now().UnixNano()},
		Options: app.ExportJobOptions{
			MaxRange: 24 * time.Hour,
			MaxRows:  maxRows,
			TTL:      time.Hour,
			RetryMax: retryMax,
		},
		Service: "admin-api",
	})
	if err != nil {
		t.Fatalf("NewExportJobService() error = %v", err)
	}
	return svc
}

// runConcurrent 让两个执行者同时抢占，验证 SKIP LOCKED 不会重复处理同一任务。
func runConcurrent(t *testing.T, svc *app.ExportJobService, workers int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, workers)
	processed := make([]bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			processed[idx], errs[idx] = svc.ProcessOnce(context.Background())
		}(i)
	}
	wg.Wait()
	handled := 0
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d ProcessOnce() error = %v", i, errs[i])
		}
		if processed[i] {
			handled++
		}
	}
	if handled != workers {
		t.Fatalf("并发处理数 = %d, want %d（说明两个 worker 抢到了同一条任务）", handled, workers)
	}
}

func clearExportTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, stmt := range []string{"DELETE FROM audit_export_job", "DELETE FROM audit_event"} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func countByAction(t *testing.T, sqlDB *sql.DB, action string, from, to time.Time) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM audit_event WHERE action = ? AND occurred_at >= ? AND occurred_at <= ?`,
		action, from, to).Scan(&count); err != nil {
		t.Fatalf("count by action: %v", err)
	}
	return count
}

// countExportEvents 统计带指定 mode 的导出审计（attrs 为 JSON 列，MySQL 用 JSON_EXTRACT）。
func countExportEvents(t *testing.T, sqlDB *sql.DB, mode string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM audit_event WHERE action = ? AND JSON_UNQUOTE(JSON_EXTRACT(attrs, '$.mode')) = ?`,
		audit.ActionDataExport, mode).Scan(&count); err != nil {
		t.Fatalf("count export events(%s): %v", mode, err)
	}
	return count
}

func loadStatus(t *testing.T, sqlDB *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM audit_export_job WHERE export_id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("load status %s: %v", id, err)
	}
	return status
}

// actorOf 返回任务归属的发起人，避免 Get 的归属校验把测试卡在 404。
func actorOf(id, first string) string {
	if id == first {
		return "admin-1"
	}
	return "admin-2"
}
