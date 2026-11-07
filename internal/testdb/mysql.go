package testdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// 注册 MySQL database/sql 驱动（S-06：空白导入仅用于驱动注册）。
	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/identity/bootstrap"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/migrate"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// MySQL 打开测试 MySQL 并应用 migrations/ 下的全部迁移；未配置 TEST_MYSQL_DSN 时跳过用例。
//
// 连接池参数沿用 pkg/db 默认值；Cleanup 关闭连接池。
// 生产环境迁移只允许 cmd/migrate 或部署 Job 执行，本函数仅服务于测试进程。
func MySQL(t *testing.T) *db.DB {
	t.Helper()
	dsn := testkit.RequireEnv(t, testkit.EnvMySQLDSN)
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("%s must enable parseTime=true (and loc=UTC) so DATETIME(3) scans into time.Time", testkit.EnvMySQLDSN)
	}
	// 迁移依赖连接 collation 与表级 DDL 一致，忽略 DSN 中可能遗留的旧模板值。
	dsn, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("normalize %s: %v", testkit.EnvMySQLDSN, err)
	}

	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatalf("open test mysql: %v", err)
	}
	t.Cleanup(func() {
		// 关闭失败仅影响连接池回收，不覆盖测试断言结果。
		_ = database.Close()
	})

	if err := Migrate(ctx, database.SQL()); err != nil {
		t.Fatalf("migrate test mysql: %v", err)
	}
	return database
}

// Migrate 对给定连接执行仓库 migrations/ 下的全部迁移，并创建测试用管理员。
//
// 迁移已是最终加密态，可一次跑完、无需中途回填；管理员由 bootstrap.EnsureAdmin
// 用固定测试密钥写入，密钥与 identity 集成测试一致（hash v1=0x11、AES k1=0x22），
// 保证测试后续写入的账号与引导账号可用同一保护器互读。
func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	if sqlDB == nil {
		return errors.New("testdb: nil *sql.DB")
	}
	root, err := testkit.RepoRoot()
	if err != nil {
		return err
	}
	migrations, err := migrate.LoadDir(filepath.Join(root, "migrations"), migrate.LoadOptions{})
	if err != nil {
		return fmt.Errorf("testdb: load migrations: %w", err)
	}
	runner, err := migrate.New(sqlDB, migrate.Config{
		Migrations: migrations,
		Dialect:    migrate.DialectMySQL,
		Table:      "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("testdb: new migration runner: %w", err)
	}

	migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := runner.Up(migrateCtx); err != nil {
		return fmt.Errorf("testdb: apply migrations: %w", err)
	}

	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x11}, 32)})
	if err != nil {
		return fmt.Errorf("testdb: build test hasher: %w", err)
	}
	ring, err := crypto.NewKeyRing(map[string][]byte{"k1": bytes.Repeat([]byte{0x22}, 32)}, "k1", nil)
	if err != nil {
		return fmt.Errorf("testdb: build test key ring: %w", err)
	}
	defer ring.Close()
	if _, err := bootstrap.EnsureAdmin(migrateCtx, sqlDB,
		persistence.NewSubjectProtector(hasher, ring), bootstrap.Options{}); err != nil {
		return fmt.Errorf("testdb: bootstrap admin: %w", err)
	}
	return nil
}
