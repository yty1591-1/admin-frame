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
	"github.com/admin-frame/admin-frame/pkg/outbox"
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

// TestTenantGuardUsesRealStatusAndHonoursTTL 验证守卫读真实库，且收敛只依赖 TTL。
//
// 「停用 ≤ 60s 拒绝访问」的证明方式是：TTL 内允许沿用旧缓存（A/B 端是两个进程，
// 不存在进程内失效），推进到 TTL 之后必须拒绝。若未来引入共享缓存，
// 本用例需要同步改成「写入后立刻拒绝」。
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
	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{Service: "admin-api"})
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

	// 1) pending 不放行（未开通），同时把该状态写入缓存。
	if err := guard.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("pending Check() error = %v, want ErrTenantNotActive", err)
	}

	actor := app.Actor{SubjectID: "it-user", ActorType: "user", Channel: "admin", TraceID: "it-trace"}
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusActive, Actor: actor,
	}); err != nil {
		t.Fatalf("activate error = %v", err)
	}
	// 2) TTL 内仍是旧缓存：证明没有进程内失效，收敛由 TTL 负责。
	if err := guard.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("TTL 内应沿用旧缓存（pending），error = %v", err)
	}
	clock.Advance(31 * time.Second)
	if err := guard.Check(ctx, tenantID); err != nil {
		t.Fatalf("TTL 到期后应放行 active，error = %v", err)
	}

	// 3) 停用后同样：TTL 内放行旧缓存，TTL 到期必须拒绝。
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: tenantID, Target: domain.StatusSuspended, Reason: "集成测试停用", Actor: actor,
	}); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if err := guard.Check(ctx, tenantID); err != nil {
		t.Fatalf("TTL 内应沿用旧缓存（active），error = %v", err)
	}
	clock.Advance(31 * time.Second)
	if err := guard.Check(ctx, tenantID); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("TTL 到期后 Check() error = %v, want ErrTenantNotActive", err)
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
	// 000010 是历史迁移：真实升级路径里它在 000013（新增 code 列）之前执行。
	// 在已应用 000013 的库上重放必须补上 code 列——这里按 000013 的兜底规则
	// （code = id）注入，验证回填语义（active / 占位名 / 幂等）在新 schema 下不变。
	statement := strings.Replace(string(raw),
		"INSERT INTO tenant (id, name,", "INSERT INTO tenant (id, code, name,", 1)
	statement = strings.Replace(statement,
		"SELECT m.merchant_id, m.merchant_id,", "SELECT m.merchant_id, m.merchant_id, m.merchant_id,", 1)
	if !strings.Contains(statement, "code") {
		t.Fatal("迁移文本结构变化，注入 code 列失败——请同步更新本用例")
	}
	// 执行两次验证幂等。
	for i := 0; i < 2; i++ {
		if _, err := sqlDB.ExecContext(ctx, statement); err != nil {
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

	list, _, err := members.List(ctx, tenantID, false, 0, 0)
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
	active, _, err := members.List(ctx, tenantID, false, 0, 0)
	if err != nil {
		t.Fatalf("List(active) error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("在册成员 = %d, want 1", len(active))
	}
	all, _, err := members.List(ctx, tenantID, true, 0, 0)
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

// TestConcurrentOwnerDemotionOnMySQL 验证行锁把「计数 + 写入」变成原子段。
//
// 没有 SELECT ... FOR UPDATE 时，两个事务都能读到 count=2 并各自提交，
// 结果租户 0 名 owner。本用例在真实 MySQL 上验证：至多一个成功，另一个 409。
func TestConcurrentOwnerDemotionOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}

	tenantID := uniqueID(t)
	tenant, err := domain.NewTenant(tenantID, "it-concurrent-owner", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	active, err := tenant.Transition(domain.StatusActive, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("Transition(active) error = %v", err)
	}
	if err := repo.UpdateStatus(ctx, active); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", tenantID) })

	members, err := app.NewMemberService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	actor := app.Actor{SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace"}
	for _, subject := range []string{"u1", "u2"} {
		if _, err := members.Add(ctx, app.AddMemberInput{
			TenantID: tenantID, SubjectID: subject, Role: domain.MemberRoleOwner, Actor: actor,
		}); err != nil {
			t.Fatalf("Add(%s) error = %v", subject, err)
		}
	}

	subjects := []string{"u1", "u2"}
	errs := make([]error, len(subjects))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, subject := range subjects {
		wg.Add(1)
		go func(idx int, sid string) {
			defer wg.Done()
			<-start
			role := domain.MemberRoleAdmin
			_, errs[idx] = members.Update(ctx, app.UpdateMemberInput{
				TenantID: tenantID, SubjectID: sid, Role: &role, Actor: actor,
			})
		}(i, subject)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("并发降级成功 %d 次，want 1（errs=%v）", succeeded, errs)
	}
	owners, err := repo.CountActiveByRole(ctx, tenantID, domain.MemberRoleOwner)
	if err != nil {
		t.Fatalf("CountActiveByRole() error = %v", err)
	}
	if owners != 1 {
		t.Fatalf("在册 owner = %d, want 1", owners)
	}
}

// failingAuditEmitter 模拟审计出口不可用。
type failingAuditEmitter struct{}

func (failingAuditEmitter) Emit(context.Context, audit.Event) error {
	return errors.New("audit sink down")
}

// TestAuditOutboxIsTransactionalOnMySQL 验证审计事件与业务写入同一事务。
//
// 成功：租户与 async_outbox 行同时存在；
// 审计失败：租户与 outbox 行都不存在（不是「状态已改、审计丢失」）。
func TestAuditOutboxIsTransactionalOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	store := outbox.NewMySQLStore(sqlDB)
	actor := app.Actor{SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace"}

	service, err := app.NewService(repo, &seqIDs{},
		audit.OutboxEmitter{Topic: app.TopicAudit, Events: store}, app.Options{
			Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
		})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	created, err := service.Create(ctx, app.CreateInput{Name: "it-outbox-ok-" + uniqueID(t), Actor: actor})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM async_outbox WHERE aggregate_id = ?", created.ID)
	})
	var outboxRows int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM async_outbox WHERE topic = ? AND aggregate_id = ?`,
		app.TopicAudit, created.ID).Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxRows != 1 {
		t.Fatalf("outbox 行数 = %d, want 1", outboxRows)
	}

	// 失败路径：审计出口报错 → 整个事务回滚，租户不落库。
	failingService, err := app.NewService(repo, &seqIDs{}, failingAuditEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewService(failing) error = %v", err)
	}
	failName := "it-outbox-fail-" + uniqueID(t)
	if _, err := failingService.Create(ctx, app.CreateInput{Name: failName, Actor: actor}); err == nil {
		t.Fatal("审计失败时 Create 应返回错误")
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE name = ?", failName) })
	var tenants int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant WHERE name = ?`, failName).Scan(&tenants); err != nil {
		t.Fatalf("count tenant: %v", err)
	}
	if tenants != 0 {
		t.Fatalf("审计失败必须回滚租户，实际 %d 行", tenants)
	}
}

// TestPlanCatalogAndMaterializationOnMySQL 验证套餐链路在真实库上成立（T-24/T-12）：
// 迁移种子默认套餐、Create 物化配额、tenant.plan_id 落值。
func TestPlanCatalogAndMaterializationOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}

	// 迁移种子保证默认套餐存在。
	plan, err := repo.DefaultPlan(ctx)
	if err != nil {
		t.Fatalf("DefaultPlan() error = %v", err)
	}
	if plan.Code != "standard" || !plan.IsDefault {
		t.Fatalf("默认套餐 = %+v", plan)
	}

	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	// 不带 owner 创建：同一租户上接着验证 T-12 开通前置。
	created, err := service.Create(ctx, app.CreateInput{
		Name: "it-plan-" + uniqueID(t), Actor: app.Actor{
			SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID) })

	if created.PlanID != plan.ID {
		t.Fatalf("PlanID = %q, want %q", created.PlanID, plan.ID)
	}
	quotas, err := repo.ListQuotas(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListQuotas() error = %v", err)
	}
	if len(quotas) != len(plan.Quotas) {
		t.Fatalf("物化配额 = %+v, want %d 项", quotas, len(plan.Quotas))
	}
	// 0 owner 不能开通（T-12 在真实库同样生效）。
	if _, err := service.Transition(ctx, app.TransitionInput{
		ID: created.ID, Target: domain.StatusActive, Actor: app.Actor{
			SubjectID: "it-admin", ActorType: "user", Channel: "admin", TraceID: "it-trace",
		},
	}); err == nil {
		t.Fatal("0 owner 开通应被拒绝")
	}
}
