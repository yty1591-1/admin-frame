package smoke_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// TestSmokeMySQLMigration 验证测试基座可连接 MySQL，且 migrations/ 能完整执行。
//
// schema_migrations 由迁移执行器在首次运行时创建；能读到版本记录即证明建表与种子脚本已落地。
func TestSmokeMySQLMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database := testkit.MySQL(t)
	var version int64
	if err := database.SQL().QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version <= 0 {
		t.Fatalf("schema_migrations is empty: migrations were not applied")
	}
}

// TestSmokeRedis 验证测试基座可连接 Redis 并完成一次 Ping。
func TestSmokeRedis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := testkit.Redis(t)
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
}

// TestSmokeStorage 验证测试基座可访问 MinIO 测试 bucket。
func TestSmokeStorage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store := testkit.Storage(t)
	if err := storage.Ready(ctx, store); err != nil {
		t.Fatalf("storage ready: %v", err)
	}
}
