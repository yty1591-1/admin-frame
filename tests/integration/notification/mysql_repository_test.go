// Package notify_test 是通知域（ENT-10）的 MySQL 集成测试。
//
// 覆盖 ENT-10 挂账的持久化契约：幂等键唯一约束、CAS 乐观锁、
// (channel, message_id) 回执对账索引、DLQ 载荷读取与事务注入。
// 未设置 NOTIFY_TEST_MYSQL_DSN 时显式跳过（不允许静默通过）。
package notify_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/testdb"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// envDSN 是通知域集成测试的连接串开关。
//
// CI 通过 mysql:8 service 注入；本地开发者用 docker 启动同版本实例后手动设置。
// 未设置时显式跳过并打印原因，避免「静默通过」造成 SQL 未被验证的错觉。
const envDSN = "NOTIFY_TEST_MYSQL_DSN"

var (
	setupOnce sync.Once
	setupDB   *sql.DB
	setupErr  error
)

// testDB 返回已完成迁移的测试数据库；未配置 DSN 时跳过当前用例。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	if dsn == "" {
		t.Skipf("%s is not set: notification integration test skipped (CI provides mysql:8 service)", envDSN)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("%s must enable parseTime=true so DATETIME(3) scans into time.Time", envDSN)
	}
	setupOnce.Do(func() { setupDB, setupErr = openAndMigrate(dsn) })
	if setupErr != nil {
		t.Fatalf("prepare integration database: %v", setupErr)
	}
	reset(t, setupDB)
	return setupDB
}

// openAndMigrate 打开连接并应用 migrations/ 下的全部脚本。
func openAndMigrate(rawDSN string) (*sql.DB, error) {
	dsn, err := db.ForceMySQLCollation("mysql", rawDSN, db.MySQLDefaultCollation)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := testdb.Migrate(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// reset 清空通知域相关表（含 async_dlq——DLQ 用例直接插桩）。
func reset(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		"DELETE FROM async_dlq",
		"DELETE FROM notification_delivery",
		"DELETE FROM notification_request",
		"DELETE FROM notification_template",
		"DELETE FROM notification_preference",
		"DELETE FROM notification_blacklist",
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("reset %s: %v", stmt, err)
		}
	}
}

// mustNotification 构造一条合法通知单（channel 集中在用例里裁剪）。
func mustNotification(t *testing.T, id, tenantID, idemKey string, now time.Time) domain.Notification {
	t.Helper()
	n, err := domain.NewNotification(id, tenantID, "tenant.invitation", "user-1", idemKey,
		[]domain.Channel{domain.ChannelEmail, domain.ChannelSms}, "tenant.invitation", "zh-CN",
		domain.PriorityNormal, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("build notification: %v", err)
	}
	return n
}

var testNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// TestRequestRepositoryIdempotencyKeyUnique 幂等键唯一约束 → ErrDuplicateRequest；
// 同键读取返回同一通知单（消费端幂等续跑的数据基础）。
func TestRequestRepositoryIdempotencyKeyUnique(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	n := mustNotification(t, "req-it-1", "t-1", "idem-1", testNow)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	dup := mustNotification(t, "req-it-2", "t-1", "idem-1", testNow)
	if err := repo.Create(ctx, dup); !errors.Is(err, domain.ErrDuplicateRequest) {
		t.Fatalf("duplicate idempotency key must be ErrDuplicateRequest, got %v", err)
	}

	got, err := repo.FindByIdempotencyKey(ctx, "idem-1")
	if err != nil {
		t.Fatalf("find by idempotency key: %v", err)
	}
	if got.ID != "req-it-1" {
		t.Fatalf("idempotency key must resolve to first request, got %s", got.ID)
	}

	// Roundtrip：列映射完整性（渠道列表 / 优先级 / 时效）。
	full, err := repo.Get(ctx, "req-it-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(full.Channels) != 2 || full.Channels[0] != domain.ChannelEmail ||
		full.Priority != domain.PriorityNormal || !full.ExpiresAt.Equal(n.ExpiresAt) {
		t.Fatalf("roundtrip mismatch: %+v", full)
	}
}

// TestRequestRepositoryCAS 乐观锁：版本冲突被拒绝（并发收口不互相覆盖）。
func TestRequestRepositoryCAS(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	n := mustNotification(t, "req-cas-1", "t-1", "idem-cas", testNow)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	first, err := repo.Get(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// 陈旧版本更新必须被 CAS 拒绝。
	stale := first
	next, err := first.Transition(domain.RequestDispatched, "", testNow.Add(time.Second))
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := repo.UpdateStatus(ctx, next); err != nil {
		t.Fatalf("update with current version: %v", err)
	}
	if err := repo.UpdateStatus(ctx, stale); err == nil {
		t.Fatal("stale version update must fail CAS")
	}
}

// TestRequestRepositoryListFailClosed 空租户键 fail closed（杜绝全表查询路径）。
func TestRequestRepositoryListFailClosed(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	n := mustNotification(t, "req-fc-1", "t-1", "idem-fc", testNow)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, err := repo.List(ctx, domain.RequestFilter{TenantID: ""}); err == nil {
		t.Fatal("empty tenant key must fail closed")
	}
	items, total, err := repo.List(ctx, domain.RequestFilter{TenantID: "t-1"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != n.ID {
		t.Fatalf("tenant filter mismatch: total=%d", total)
	}
}

// TestRequestRepositoryWithinTx 事务注入：tx 内写入对外不可见，回滚即消失。
func TestRequestRepositoryWithinTx(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	// 回滚路径：tx 内写入后返回错误，外部不可见。
	err = repo.WithinTx(ctx, func(txCtx context.Context) error {
		n := mustNotification(t, "req-tx-1", "t-1", "idem-tx", testNow)
		if cerr := repo.Create(txCtx, n); cerr != nil {
			return cerr
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if _, gerr := repo.Get(ctx, "req-tx-1"); !errors.Is(gerr, domain.ErrNotFound) {
		t.Fatalf("rolled-back request must be invisible, got %v", gerr)
	}

	// 提交路径：tx 内写入正常提交。
	if err := repo.WithinTx(ctx, func(txCtx context.Context) error {
		n := mustNotification(t, "req-tx-2", "t-1", "idem-tx-2", testNow)
		return repo.Create(txCtx, n)
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	if _, err := repo.Get(ctx, "req-tx-2"); err != nil {
		t.Fatalf("committed request must be visible: %v", err)
	}
}

// TestDeliveryRepositoryReceiptLookup (channel, message_id) 回执对账索引：
// Upsert 后按 message_id 可查（NT-07 ReceiptService 的数据基础）。
func TestDeliveryRepositoryReceiptLookup(t *testing.T) {
	sqlDB := testDB(t)
	requests, err := persistence.NewMySQLRequestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new request repo: %v", err)
	}
	deliveries, err := persistence.NewMySQLDeliveryRepository(sqlDB)
	if err != nil {
		t.Fatalf("new delivery repo: %v", err)
	}
	ctx := context.Background()

	n := mustNotification(t, "req-rcpt-1", "t-1", "idem-rcpt", testNow)
	if err := requests.Create(ctx, n); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	d, err := domain.NewDelivery("dlv-1", n.ID, "t-1", domain.ChannelSms, testNow)
	if err != nil {
		t.Fatalf("new delivery: %v", err)
	}
	if err := deliveries.Upsert(ctx, d); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 推进到 accepted 并绑定 message_id。
	accepted, err := d.Transition(domain.DeliveryAccepted, testNow.Add(time.Second))
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	accepted.MessageID = "msg-it-1"
	if err := deliveries.Update(ctx, accepted); err != nil {
		t.Fatalf("update: %v", err)
	}

	found, err := deliveries.FindByMessageID(ctx, domain.ChannelSms, "msg-it-1")
	if err != nil {
		t.Fatalf("find by message id: %v", err)
	}
	if found.RequestID != n.ID || found.Status != domain.DeliveryAccepted {
		t.Fatalf("receipt lookup mismatch: %+v", found)
	}

	// 未绑定 message_id 的明细不参与对账。
	if _, err := deliveries.FindByMessageID(ctx, domain.ChannelSms, "missing"); !errors.Is(err, domain.ErrDeliveryNotFound) {
		t.Fatalf("missing message id must be ErrDeliveryNotFound, got %v", err)
	}

	// CountByStatus 汇总（notify_delivery_active Gauge 数据源）。
	counts, err := deliveries.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts[domain.DeliveryAccepted] < 1 {
		t.Fatalf("accepted count must be >= 1, got %v", counts)
	}
}

// TestTemplateRepositoryVersioning 模板版本化读取（渲染走确定版本）。
func TestTemplateRepositoryVersioning(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLTemplateRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	t1, err := domain.NewTemplate("tenant.invitation", domain.ChannelEmail, "zh-CN",
		"邀请", "你好 {{.email}}", "admin-1", testNow)
	if err != nil {
		t.Fatalf("new template: %v", err)
	}
	saved, err := repo.Upsert(ctx, t1)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.Find(ctx, "tenant.invitation", domain.ChannelEmail, "zh-CN")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Subject != "邀请" || got.Version < 1 {
		t.Fatalf("template mismatch: %+v", got)
	}

	// 同键同渠道同语言 Upsert = 库内版本自增（新版本顶替旧版本）。
	t2 := got
	t2.Subject = "邀请（更新）"
	saved2, err := repo.Upsert(ctx, t2)
	if err != nil {
		t.Fatalf("upsert v2: %v", err)
	}
	if saved2.Version <= saved.Version {
		t.Fatalf("version must increase: %d -> %d", saved.Version, saved2.Version)
	}
	if latest, ferr := repo.Find(ctx, "tenant.invitation", domain.ChannelEmail, "zh-CN"); ferr != nil || latest.Subject != "邀请（更新）" {
		t.Fatalf("latest version must win: %+v err=%v", latest, ferr)
	}
}

// TestDLQReaderPayloadBySource DLQ 载荷读取（ReplayService 的补偿数据源）。
func TestDLQReaderPayloadBySource(t *testing.T) {
	sqlDB := testDB(t)
	reader, err := persistence.NewMySQLDLQReader(sqlDB)
	if err != nil {
		t.Fatalf("new dlq reader: %v", err)
	}
	ctx := context.Background()

	// 直接插桩一条死信（模拟 worker retry.Permanent 落 DLQ 的产物）。
	// JSON 列会规范化键序与空白，断言按解析后的字段比较。
	payload := `{"schema_version":"1","request_id":"req-dlq-1","tenant_id":"t-1"}`
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO async_dlq (source_id, topic, event_type, payload, attempts, last_error, failed_at)
		 VALUES (?, 'notification.outbox', 'notification.requested', ?, 3, 'boom', ?)`,
		"evt-dlq-1", payload, testNow); err != nil {
		t.Fatalf("seed dlq: %v", err)
	}

	got, err := reader.PayloadBySource(ctx, "evt-dlq-1")
	if err != nil {
		t.Fatalf("payload by source: %v", err)
	}
	// JSON 列规范化后键序可能变化，按字段断言（保证契约字段完整）。
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded["request_id"] != "req-dlq-1" || decoded["tenant_id"] != "t-1" || decoded["schema_version"] != "1" {
		t.Fatalf("payload fields mismatch: %v", decoded)
	}
	if _, err := reader.PayloadBySource(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing source must be ErrNotFound, got %v", err)
	}
}
