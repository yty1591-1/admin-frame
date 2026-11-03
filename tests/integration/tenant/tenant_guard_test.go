package tenant_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// envDSN 是租户域集成测试的开关。
//
// CI 用与 IAM 集成本例相同的 mysql:8 service；本地可自行起同版本实例后设置。
// 未设置时显式 Skip 并打印原因，不允许「静默通过」造成 SQL 未被验证的错觉。
const envDSN = "TENANT_TEST_MYSQL_DSN"

var (
	setupOnce sync.Once
	setupDB   *sql.DB
	setupErr  error
)

// testDB 返回已完成迁移的测试库。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	if dsn == "" {
		t.Skipf("%s is not set: tenant integration test skipped (CI provides mysql:8 service)", envDSN)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("%s must enable parseTime=true so DATETIME(3) scans into time.Time", envDSN)
	}
	setupOnce.Do(func() { setupDB, setupErr = openAndMigrate(dsn) })
	if setupErr != nil {
		t.Fatalf("prepare integration database: %v", setupErr)
	}
	return setupDB
}

// openAndMigrate 打开连接并执行全部迁移。
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
	if err := testkit.Migrate(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// seqIDs 是测试用 ID 生成器。
type seqIDs struct {
	mu   sync.Mutex
	next int
}

func (g *seqIDs) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return int64(g.next), nil
}

func (g *seqIDs) NextString() (string, error) {
	id, err := g.Next()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// quietEmitter 丢弃审计事件（审计语义在 app 单测覆盖）。
type quietEmitter struct{}

func (quietEmitter) Emit(context.Context, audit.Event) error { return nil }

// fakeClock 是可推进的时钟，用来证明缓存 TTL 到期即生效，不必真等 30s。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// uniqueID 生成跨运行唯一的租户 ID，避免复用测试卷时主键冲突。
func uniqueID(t *testing.T) string {
	t.Helper()
	return "it-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// TestTenantGuardUsesRealStatusAndHonoursTTL 验证守卫读真实库、TTL 到期后停用生效。
//
// 这是「停用 ≤ 60s 拒绝访问」的核心链路：仓储 → 守卫缓存 → 判定。
// 用假时钟推进到 TTL 边界，证明最坏生效时延 = TTL 而不是无限。
func TestTenantGuardUsesRealStatusAndHonoursTTL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}

	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Second)}
	guard, err := app.NewGuard(repo, app.GuardOptions{
		TTL:         30 * time.Second,
		NotFoundTTL: 5 * time.Second,
		Clock:       clock.Now,
	})
	if err != nil {
		t.Fatalf("NewGuard() error = %v", err)
	}
	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{
		Service:     "admin-api",
		Invalidator: guard,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tenantID := uniqueID(t)
	tenant, err := domain.NewTenant(tenantID, "integration-tenant", clock.Now())
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", tenantID) })

	// 1) pending 不放行（未开通）。
	if err := guard.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("pending Check() error = %v, want ErrTenantNotActive", err)
	}

	actor := app.Actor{SubjectID: "it-user", ActorType: "user", Channel: "admin", TraceID: "it-trace"}
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusActive, Actor: actor,
	}); err != nil {
		t.Fatalf("activate error = %v", err)
	}

	// 2) active 放行；同进程迁移已失效缓存，因此这里必然查到新状态。
	if err := guard.Check(ctx, tenantID); err != nil {
		t.Fatalf("active Check() error = %v, want nil", err)
	}

	// 3) 另一个副本（独立守卫、独立缓存）先缓存「可服务」。
	replicaClock := &fakeClock{now: clock.Now()}
	replica, err := app.NewGuard(repo, app.GuardOptions{Clock: replicaClock.Now})
	if err != nil {
		t.Fatalf("NewGuard(replica) error = %v", err)
	}
	if err := replica.Check(ctx, tenantID); err != nil {
		t.Fatalf("replica Check() error = %v, want nil", err)
	}

	// 4) 停用：本进程立即拒绝，另一副本在 TTL 内仍放行。
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusSuspended, Reason: "集成测试停用", Actor: actor,
	}); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if err := guard.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("停用后本进程 Check() error = %v, want ErrTenantNotActive", err)
	}
	if err := replica.Check(ctx, tenantID); err != nil {
		t.Fatalf("TTL 内副本仍按旧缓存放行，error = %v", err)
	}

	// 5) 推进到 TTL 之后：副本也必须拒绝（最坏生效时延 = TTL < 60s）。
	replicaClock.Advance(31 * time.Second)
	if err := replica.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("TTL 到期后副本 Check() error = %v, want ErrTenantNotActive", err)
	}
}

// TestBackfillMigrationRegistersExistingMerchants 验证 000010 回填迁移：
// 存量 merchant_id 会被登记为 active 租户，且重复执行幂等。
func TestBackfillMigrationRegistersExistingMerchants(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	merchantID := "m-backfill-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// 造一条带 merchant_id 的 IAM 绑定来源：用角色表即可（回填取三表并集）。
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO iam_role (id, channel, scope_type, merchant_id, code, name, description, role_type,
		                       status, version, created_by, updated_by, created_at, updated_at, deleted_at)
		 VALUES (?, 'biz', 'merchant', ?, 'it_backfill', '回填测试角色', '', 'custom',
		         'active', 1, 'it', 'it', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3), NULL)`,
		time.Now().UnixNano(), merchantID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DELETE FROM iam_role WHERE merchant_id = ?", merchantID)
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", merchantID)
	})

	raw, err := os.ReadFile("../../../migrations/000010_backfill_tenant_from_merchants.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// 迁移文件只有一条 INSERT，可直接执行；执行两次验证幂等。
	for i := 0; i < 2; i++ {
		if _, err := sqlDB.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("exec backfill (round %d): %v", i+1, err)
		}
	}

	var status, name string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT status, name FROM tenant WHERE id = ?`, merchantID).Scan(&status, &name); err != nil {
		t.Fatalf("query backfilled tenant: %v", err)
	}
	if status != string(domain.StatusActive) {
		t.Fatalf("回填状态 = %s, want active（登记不得改变可用性）", status)
	}
	if name != merchantID {
		t.Fatalf("回填名称 = %s, want 占位为 merchant_id", name)
	}
}

// TestMemberAndQuotaOnMySQL 验证成员与配额在真实 MySQL 上的语义：
// 幂等 upsert、角色排序、停用过滤、最后一名 owner 保护、配额不限量。
func TestMemberAndQuotaOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}

	tenantID := uniqueID(t)
	tenant, err := domain.NewTenant(tenantID, "integration-member", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create tenant error = %v", err)
	}
	// 外键级联：删租户会连带清理成员与配额。
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", tenantID) })

	members, err := app.NewMemberService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	quotas, err := app.NewQuotaService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}
	actor := app.Actor{SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace"}

	if _, err := members.Add(ctx, app.AddMemberInput{
		TenantID: tenantID, SubjectID: "u-owner", Role: domain.MemberRoleOwner, Actor: actor,
	}); err != nil {
		t.Fatalf("Add(owner) error = %v", err)
	}
	if _, err := members.Add(ctx, app.AddMemberInput{
		TenantID: tenantID, SubjectID: "u-staff", Role: domain.MemberRoleMember, Actor: actor,
	}); err != nil {
		t.Fatalf("Add(staff) error = %v", err)
	}

	list, err := members.List(ctx, tenantID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 || list[0].SubjectID != "u-owner" || list[0].Role != domain.MemberRoleOwner {
		t.Fatalf("成员列表 = %+v（owner 应排在前）", list)
	}

	// 降级唯一 owner 应被拒绝。
	if _, err := members.SetRole(ctx, app.SetMemberRoleInput{
		TenantID: tenantID, SubjectID: "u-owner", Role: domain.MemberRoleAdmin, Actor: actor,
	}); err == nil {
		t.Fatal("降级唯一 owner 应失败")
	}

	if _, err := members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: tenantID, SubjectID: "u-staff", Status: domain.MemberStatusDisabled,
		Reason: "离职", Actor: actor,
	}); err != nil {
		t.Fatalf("SetStatus(disabled) error = %v", err)
	}
	active, err := members.List(ctx, tenantID, false)
	if err != nil {
		t.Fatalf("List(active) error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("在册成员 = %d, want 1", len(active))
	}
	all, err := members.List(ctx, tenantID, true)
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("全部成员 = %d, want 2（停用不删除）", len(all))
	}

	// 配额：幂等覆盖 + 不限量。
	if _, err := quotas.Set(ctx, app.SetQuotaInput{
		TenantID: tenantID, Resource: "user", Limit: 50, Actor: actor,
	}); err != nil {
		t.Fatalf("SetQuota(user) error = %v", err)
	}
	if _, err := quotas.Set(ctx, app.SetQuotaInput{
		TenantID: tenantID, Resource: "api.rps", Limit: domain.QuotaUnlimited, Actor: actor,
	}); err != nil {
		t.Fatalf("SetQuota(api.rps) error = %v", err)
	}
	configured, err := quotas.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListQuotas() error = %v", err)
	}
	if len(configured) != 2 || configured[0].Resource != "api.rps" || !configured[0].Unlimited() {
		t.Fatalf("配额列表 = %+v", configured)
	}
	if configured[1].Limit != 50 {
		t.Fatalf("user 配额 = %d, want 50", configured[1].Limit)
	}
}
