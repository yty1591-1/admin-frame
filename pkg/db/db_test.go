package db_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/db"
)

func TestMain(m *testing.M) {
	sql.Register("dbtest", &testDriver{})
	m.Run()
}

func TestOpenValidate(t *testing.T) {
	ctx := context.Background()
	if _, err := db.Open(ctx, db.Config{}); !errors.Is(err, db.ErrInvalidConfig) {
		t.Fatalf("empty config err=%v", err)
	}
	if _, err := db.Open(ctx, db.Config{Driver: "memory", DSN: "x"}); !errors.Is(err, db.ErrInvalidConfig) {
		t.Fatalf("memory driver err=%v", err)
	}
	if _, err := db.Open(ctx, db.Config{Driver: "dbtest", DSN: ""}); !errors.Is(err, db.ErrInvalidConfig) {
		t.Fatalf("empty dsn err=%v", err)
	}
}

// TestNormalizeDSN 验证 MySQL 连接 collation 对齐表级 DDL，且不覆盖显式配置。
func TestNormalizeDSN(t *testing.T) {
	cases := []struct {
		name         string
		driver       string
		dsn          string
		wantContains string
		wantCharset  bool
	}{
		{"mysql_default", "mysql", "user:p@tcp(127.0.0.1:3306)/d?parseTime=true", "collation=utf8mb4_0900_ai_ci", true},
		{"mysql_explicit", "mysql", "user:p@tcp(127.0.0.1:3306)/d?collation=utf8mb4_bin", "collation=utf8mb4_bin", false},
		{"mariadb_alias", "mariadb", "user:p@tcp(127.0.0.1:3306)/d", "collation=utf8mb4_0900_ai_ci", true},
		{"other_driver", "pgx", "postgres://u:p@localhost/db", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.NormalizeDSN(tc.driver, tc.dsn)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if tc.wantContains == "" {
				if got != tc.dsn {
					t.Fatalf("dsn changed: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantContains) {
				t.Fatalf("dsn = %q, want contains %q", got, tc.wantContains)
			}
			// 默认路径必须带 charset：驱动只有在 charset 存在时才下发 SET NAMES ... COLLATE ...。
			if tc.wantCharset && !strings.Contains(got, "charset=utf8mb4") {
				t.Fatalf("dsn = %q, want charset=utf8mb4", got)
			}
		})
	}
	if _, err := db.NormalizeDSN("mysql", "not-a-dsn"); err == nil {
		t.Fatal("invalid mysql dsn should fail")
	}
}

// TestForceMySQLCollation 验证迁移入口会覆盖 DSN 中遗留的旧 collation。
func TestForceMySQLCollation(t *testing.T) {
	dsn := "user:p@tcp(127.0.0.1:3306)/d?parseTime=true&collation=utf8mb4_general_ci"
	got, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if !strings.Contains(got, "collation=utf8mb4_0900_ai_ci") || strings.Contains(got, "utf8mb4_general_ci") {
		t.Fatalf("dsn = %q", got)
	}
	// 只覆盖 collation 不够：缺少 charset 时驱动不会下发 SET NAMES，1267 仍会复现。
	if !strings.Contains(got, "charset=utf8mb4") {
		t.Fatalf("dsn = %q, want charset=utf8mb4", got)
	}
	if strings.Count(got, "charset=") != 1 {
		t.Fatalf("dsn = %q, charset param must appear once", got)
	}
	if withCharset, err := db.ForceMySQLCollation("mysql", got, db.MySQLDefaultCollation); err != nil ||
		strings.Count(withCharset, "charset=") != 1 {
		t.Fatalf("idempotent force = %q, %v", withCharset, err)
	}
	if passthrough, err := db.ForceMySQLCollation("pgx", dsn, db.MySQLDefaultCollation); err != nil || passthrough != dsn {
		t.Fatalf("pgx passthrough = %q, %v", passthrough, err)
	}
}

func TestOpenPingClose(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, db.Config{
		Driver:       "dbtest",
		DSN:          "ok",
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	if d.Driver() != "dbtest" {
		t.Fatalf("driver=%s", d.Driver())
	}
	if d.SQL() == nil {
		t.Fatal("nil sql")
	}
	if err := d.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Checker()(); err != nil {
		t.Fatal(err)
	}
	stats := d.Stats()
	if stats.MaxOpenConnections != 4 {
		t.Fatalf("MaxOpenConnections=%d", stats.MaxOpenConnections)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Ping(ctx); !errors.Is(err, db.ErrClosed) {
		t.Fatalf("after close ping=%v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func TestOpenPingFailure(t *testing.T) {
	_, err := db.Open(context.Background(), db.Config{
		Driver: "dbtest",
		DSN:    "fail-ping",
	})
	if err == nil {
		t.Fatal("expected ping error")
	}
}

func TestWithinTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, db.Config{Driver: "dbtest", DSN: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	if err := db.WithinTxDB(ctx, d, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT 1")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err = db.WithinTx(ctx, d.SQL(), nil, func(_ *sql.Tx) error {
		return boom
	})
	if !errors.Is(err, db.ErrTxFailed) || !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}

	err = db.WithinTx(ctx, d.SQL(), nil, func(_ *sql.Tx) error {
		panic("explode")
	})
	if !errors.Is(err, db.ErrTxFailed) {
		t.Fatalf("panic err=%v", err)
	}
}

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		in, wantSub string
	}{
		// url.UserPassword 会把 * 编码为 %2A
		{"postgres://alice:s3cret@localhost:5432/app?sslmode=disable", "%2A%2A%2A"},
		{"user=alice password=s3cret host=localhost dbname=app", "password=***"},
		{"Server=localhost;Database=app;Uid=alice;Pwd=s3cret;", "Pwd=***"},
	}
	for _, tc := range cases {
		got := db.RedactDSN(tc.in)
		if got == tc.in {
			t.Fatalf("not redacted: %q", got)
		}
		if tc.wantSub != "" && !strings.Contains(got, tc.wantSub) {
			t.Fatalf("got=%q want substring %q", got, tc.wantSub)
		}
		if strings.Contains(got, "s3cret") {
			t.Fatalf("secret leaked: %q", got)
		}
	}
}

// --- minimal database/sql driver for tests ---

type testDriver struct{}

func (d *testDriver) Open(name string) (driver.Conn, error) {
	return &testConn{dsn: name}, nil
}

type testConn struct {
	dsn    string
	mu     sync.Mutex
	closed bool
}

func (c *testConn) Prepare(query string) (driver.Stmt, error) {
	return &testStmt{query: query}, nil
}

func (c *testConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *testConn) Begin() (driver.Tx, error) {
	return &testTx{}, nil
}

func (c *testConn) Ping(ctx context.Context) error {
	if c.dsn == "fail-ping" {
		return errors.New("ping refused")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// driver.Pinger
var _ driver.Pinger = (*testConn)(nil)

type testTx struct{}

func (t *testTx) Commit() error   { return nil }
func (t *testTx) Rollback() error { return nil }

type testStmt struct {
	query string
}

func (s *testStmt) Close() error  { return nil }
func (s *testStmt) NumInput() int { return -1 }

func (s *testStmt) Exec(_ []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

func (s *testStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return &testRows{}, nil
}

type testRows struct{ closed bool }

func (r *testRows) Columns() []string { return nil }
func (r *testRows) Close() error      { r.closed = true; return nil }
func (r *testRows) Next(_ []driver.Value) error {
	return io.EOF
}

// Ensure ExecContext path works via Conn.ExecerContext if needed — database/sql
// falls back to Prepare+Exec. Keep Conn without Execer; Stmt path is enough.

func TestConfigIdleClamp(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, db.Config{
		Driver:       "dbtest",
		DSN:          "ok",
		MaxOpenConns: 3,
		// MaxIdleConns 0 → default min(5,3)=3
		PingTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if d.Stats().MaxOpenConnections != 3 {
		t.Fatalf("max open=%d", d.Stats().MaxOpenConnections)
	}
}
