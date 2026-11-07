// Package featureflag_test 是特性开关域（ENT-11）的 MySQL 集成测试。
//
// 覆盖 ENT-11 挂账的持久化契约：规则 JSON 序列化往返、CAS 乐观锁（调用方
// 递增版本）、治理状态落库语义（expired 为派生态——表只存 enabled 布尔，
// 到期由 ListExpired 扫描 + ExpireService 迁移）。
// 未设置 FEATUREFLAG_TEST_MYSQL_DSN 时显式跳过（不允许静默通过）。
package featureflag_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/featureflag/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/testdb"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// envDSN 是特性开关域集成测试的连接串开关。
const envDSN = "FEATUREFLAG_TEST_MYSQL_DSN"

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
		t.Skipf("%s is not set: featureflag integration test skipped (CI provides mysql:8 service)", envDSN)
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

// reset 清空 feature_flag（ent16_test 与通知套件共享，reset 互补）。
func reset(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	if _, err := sqlDB.Exec("DELETE FROM feature_flag"); err != nil {
		t.Fatalf("reset feature_flag: %v", err)
	}
}

var testNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// mustFlag 构造一条合法开关（NewFlag 要求到期时间在未来，测试用例通过
// Update 调整为过去时点模拟已到期）。
func mustFlag(t *testing.T, key string, rules []domain.Rule, permanent bool, expiresAt time.Time) domain.Flag {
	t.Helper()
	f, err := domain.NewFlag(key, "集成测试开关 "+key, rules, permanent, expiresAt, "admin-1", testNow)
	if err != nil {
		t.Fatalf("build flag %s: %v", key, err)
	}
	return f
}

// TestFlagRulesJSONRoundtrip 规则 JSON 序列化往返：渠道 / 租户集合 / 百分比
// 三维度不丢（规则单维度约束：channel XOR tenant_ids XOR percentage）。
func TestFlagRulesJSONRoundtrip(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	rules := []domain.Rule{
		{Channel: "biz"},
		{TenantIDs: []string{"t-1", "t-2"}},
		{Percentage: 300},
	}
	f := mustFlag(t, "it.roundtrip", rules, true, time.Time{})
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.Get(ctx, "it.roundtrip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.StatusEnabled || !got.Permanent || got.Version != 1 {
		t.Fatalf("flag head mismatch: %+v", got)
	}
	if len(got.Rules) != 3 ||
		got.Rules[0].Channel != "biz" ||
		len(got.Rules[1].TenantIDs) != 2 || got.Rules[1].TenantIDs[0] != "t-1" ||
		got.Rules[2].Percentage != 300 {
		t.Fatalf("rules roundtrip mismatch: %+v", got.Rules)
	}
}

// TestFlagDuplicateKey 同 key 二次创建 → ErrDuplicateKey（uk 约束）。
func TestFlagDuplicateKey(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	f := mustFlag(t, "it.dup", nil, true, time.Time{})
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	dup := mustFlag(t, "it.dup", nil, true, time.Time{})
	if err := repo.Create(ctx, dup); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate key must be ErrDuplicateKey, got %v", err)
	}
}

// TestFlagCASVersionConflict 乐观锁：仓储按 expectedVersion 条件更新，
// 版本由调用方递增（service 层职责），陈旧版本更新被拒。
func TestFlagCASVersionConflict(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	f := mustFlag(t, "it.cas", []domain.Rule{{Percentage: 100}}, true, time.Time{})
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	current, err := repo.Get(ctx, "it.cas")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// 调用方递增版本后更新成功。
	next := current
	next.Description = "更新后的描述"
	next.Version = current.Version + 1
	if err := repo.Update(ctx, next, current.Version); err != nil {
		t.Fatalf("update with bumped version: %v", err)
	}
	after, err := repo.Get(ctx, "it.cas")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if after.Description != "更新后的描述" || after.Version != current.Version+1 {
		t.Fatalf("update mismatch: desc=%q version=%d", after.Description, after.Version)
	}

	// 陈旧版本更新必须失败。
	stale := current
	stale.Description = "陈旧描述"
	if err := repo.Update(ctx, stale, current.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale version must be ErrVersionConflict, got %v", err)
	}
}

// TestFlagGovernanceLifecycle 治理状态落库语义：enabled 布尔承载 kill switch，
// 到期是派生态（enabled=1 + permanent=0 + expires_at<=now 由 ListExpired 扫出）；
// expired 终态语义（不可复活）由领域状态机守卫（单测覆盖）。
func TestFlagGovernanceLifecycle(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	f := mustFlag(t, "it.gov", []domain.Rule{{Percentage: domain.MaxPercentage}}, true, time.Time{})
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 停用（kill switch）→ 落库 enabled=0。
	disabled, err := f.Transition(domain.StatusDisabled, testNow.Add(time.Second))
	if err != nil {
		t.Fatalf("transition disabled: %v", err)
	}
	disabled.Version = f.Version + 1
	if err := repo.Update(ctx, disabled, f.Version); err != nil {
		t.Fatalf("save disabled: %v", err)
	}
	got, err := repo.Get(ctx, "it.gov")
	if err != nil {
		t.Fatalf("get disabled: %v", err)
	}
	if got.Status != domain.StatusDisabled {
		t.Fatalf("expected disabled, got %s", got.Status)
	}

	// kill switch 解除（重新启用）。
	enabled, err := got.Transition(domain.StatusEnabled, testNow.Add(2*time.Second))
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	enabled.Version = got.Version + 1
	if err := repo.Update(ctx, enabled, got.Version); err != nil {
		t.Fatalf("save enabled: %v", err)
	}

	// 转 non-permanent 并把到期时间划到过去 → ListExpired 扫出（治理口径）。
	current, err := repo.Get(ctx, "it.gov")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	due := current
	due.Permanent = false
	due.ExpiresAt = testNow.Add(-time.Hour)
	due.Version = current.Version + 1
	if err := repo.Update(ctx, due, current.Version); err != nil {
		t.Fatalf("save expiry: %v", err)
	}
	listed, err := repo.ListExpired(ctx, testNow, 100)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}
	if len(listed) != 1 || listed[0].Key != "it.gov" {
		t.Fatalf("expired flag must be due for governance, got %+v", listed)
	}

	// 到期自动禁用（ExpireService 动作）：enabled → disabled 落库后不再被扫出。
	expired, err := due.Transition(domain.StatusDisabled, testNow)
	if err != nil {
		t.Fatalf("expire transition: %v", err)
	}
	expired.Version = due.Version + 1
	if err := repo.Update(ctx, expired, due.Version); err != nil {
		t.Fatalf("save expired-disable: %v", err)
	}
	again, err := repo.ListExpired(ctx, testNow, 100)
	if err != nil {
		t.Fatalf("re-list expired: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("disabled flag must not be due, got %+v", again)
	}
}

// TestFlagDeleteAndNotFound 删除后读取返回 ErrNotFound（物理删除语义）。
func TestFlagDeleteAndNotFound(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()

	f := mustFlag(t, "it.del", nil, true, time.Time{})
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	disabled, err := f.Transition(domain.StatusDisabled, testNow.Add(time.Second))
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled.Version = f.Version + 1
	if err := repo.Update(ctx, disabled, f.Version); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.Delete(ctx, "it.del"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, "it.del"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted flag must be ErrNotFound, got %v", err)
	}
	if err := repo.Delete(ctx, "it.del"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete must be ErrNotFound, got %v", err)
	}
}
