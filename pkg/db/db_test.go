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
	err = db.WithinTx(ctx, d.SQL(), nil, func(tx *sql.Tx) error {
		return boom
	})
	if !errors.Is(err, db.ErrTxFailed) || !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}

	err = db.WithinTx(ctx, d.SQL(), nil, func(tx *sql.Tx) error {
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

func (s *testStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

func (s *testStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &testRows{}, nil
}

type testRows struct{ closed bool }

func (r *testRows) Columns() []string { return nil }
func (r *testRows) Close() error      { r.closed = true; return nil }
func (r *testRows) Next(dest []driver.Value) error {
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
