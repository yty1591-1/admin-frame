package platform

import (
	"testing"
	"time"
)

// TestDBConfigFromCarriesPoolParams 锁定「配置字段 → pkg/db 参数」的搬运：
// 漏搬任一字段都会让 db.max_open_conns 静默失效，而 db_pool_max_open_connections
// 指标回落到 pkg/db 默认值，DBPoolExhaustion 告警的分母随之失真。
func TestDBConfigFromCarriesPoolParams(t *testing.T) {
	got, err := dbConfigFrom(DBConfig{
		Driver:          "mysql",
		DSN:             "root:root@tcp(localhost:3306)/admin_frame?parseTime=true",
		MaxOpenConns:    6,
		MaxIdleConns:    3,
		ConnMaxLifetime: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Driver != "mysql" {
		t.Fatalf("driver=%q, want mysql", got.Driver)
	}
	if got.MaxOpenConns != 6 || got.MaxIdleConns != 3 {
		t.Fatalf("pool=%d/%d, want 6/3", got.MaxOpenConns, got.MaxIdleConns)
	}
	if got.ConnMaxLifetime != 10*time.Minute {
		t.Fatalf("lifetime=%s, want 10m", got.ConnMaxLifetime)
	}
}

// TestDBConfigFromMapsPostgresDriver 覆盖 postgres → pgx 的驱动名转换。
func TestDBConfigFromMapsPostgresDriver(t *testing.T) {
	got, err := dbConfigFrom(DBConfig{Driver: "postgres", DSN: "postgres://u:p@localhost:5432/db"}) // #nosec G101 -- 测试用假 DSN，非真实凭据
	if err != nil {
		t.Fatal(err)
	}
	if got.Driver != "pgx" {
		t.Fatalf("driver=%q, want pgx", got.Driver)
	}
}

// TestDBConfigFromRejectsUnknownDriver 确认未知引擎不静默回落。
func TestDBConfigFromRejectsUnknownDriver(t *testing.T) {
	if _, err := dbConfigFrom(DBConfig{Driver: "sqlite", DSN: "x"}); err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}
