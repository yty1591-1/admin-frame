package platform_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	platformobservability "github.com/admin-frame/admin-frame/internal/platform/observability"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// stubConn 仅供连接池指标测试：不建立真实连接。
type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return stubConn{}, nil }

func TestRegisterDBMetrics(t *testing.T) {
	sql.Register("platform_db_metrics_test", stubDriver{})
	database, err := db.Open(context.Background(), db.Config{
		Driver:   "platform_db_metrics_test",
		DSN:      "stub",
		SkipPing: true,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	reg := prometheus.NewRegistry()
	if err := platformobservability.RegisterDBMetrics(reg, database); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	got := make(map[string]bool, len(families))
	for _, family := range families {
		got[family.GetName()] = true
	}
	for _, name := range []string{
		"db_pool_open_connections",
		"db_pool_in_use_connections",
		"db_pool_idle_connections",
		"db_pool_max_open_connections",
		"db_pool_wait_total",
		"db_pool_wait_duration_seconds_total",
	} {
		if !got[name] {
			t.Fatalf("missing metric %s", name)
		}
	}
}

func TestRegisterDBMetricsSkipsNil(t *testing.T) {
	if err := platformobservability.RegisterDBMetrics(prometheus.NewRegistry(), nil); err != nil {
		t.Fatalf("nil database: %v", err)
	}
	if err := platformobservability.RegisterDBMetrics(nil, &db.DB{}); err != nil {
		t.Fatalf("nil registerer: %v", err)
	}
}
