package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// stubDriverName 是测试用驱动名；同一进程重复注册会 panic，故只注册一次。
const stubDriverName = "db-stub-tracing"

var registerStub sync.Once

func registerStubDriver() {
	registerStub.Do(func() {
		sql.Register(stubDriverName, stubDriver{})
	})
}

// stubDriver / stubConnector / stubConn / stubRows 构成最小的 database/sql 驱动，
// 用于在无真实数据库的情况下验证 OpenTraced 是否真的产出 SQL span。
type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return &stubConn{}, nil }

func (stubDriver) OpenConnector(string) (driver.Connector, error) { return stubConnector{}, nil }

type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) { return &stubConn{}, nil }

func (stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubConn struct{}

func (*stubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }

func (*stubConn) Close() error { return nil }

func (*stubConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

func (*stubConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &stubRows{}, nil
}

type stubRows struct{ read bool }

func (*stubRows) Columns() []string { return []string{"v"} }

func (*stubRows) Close() error { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = int64(1)
	return nil
}

func TestDBSystem(t *testing.T) {
	tests := map[string]string{
		"mysql":      "mysql",
		"mariadb":    "mysql",
		"pgx":        "postgresql",
		"postgres":   "postgresql",
		"postgresql": "postgresql",
		"unknown":    "unknown",
	}
	for driver, want := range tests {
		if got := dbSystem(driver); got != want {
			t.Errorf("dbSystem(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestDatabaseName(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		dsn    string
		want   string
	}{
		{"mysql", "mysql", "u:p@tcp(127.0.0.1:3306)/admin_frame?parseTime=true", "admin_frame"},
		{"pgx url", "pgx", "postgres://u:p@127.0.0.1:5432/admin_frame?sslmode=disable", "admin_frame"},
		{"pgx keyvalue", "pgx", "host=127.0.0.1 port=5432 dbname=admin_frame sslmode=disable", "admin_frame"},
		{"mysql 无法解析", "mysql", "://bad", ""},
		{"未支持驱动", "unknown", "whatever", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := databaseName(tc.driver, tc.dsn); got != tc.want {
				t.Fatalf("databaseName(%q, %q) = %q, want %q", tc.driver, tc.dsn, got, tc.want)
			}
		})
	}
}

// TestOpenTracedCreatesSQLSpan 是 AC-ENT-06-4 的最小证据：一次带父 span 的查询
// 必须产出 Client 类 SQL span，且父节点为调用方 span。
func TestOpenTracedCreatesSQLSpan(t *testing.T) {
	registerStubDriver()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	database, err := OpenTraced(context.Background(), Config{
		Driver:   stubDriverName,
		DSN:      "stub://tracing",
		SkipPing: true,
	}, tp)
	if err != nil {
		t.Fatalf("OpenTraced: %v", err)
	}
	defer func() { _ = database.Close() }()

	ctx, parent := tp.Tracer("test").Start(context.Background(), "GET /admin/v1/audit/events")
	rows, err := database.SQL().QueryContext(ctx, "SELECT v FROM probe WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close: %v", err)
	}
	parent.End()

	var sqlSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		// 一次查询会产生多个 Client span（sql.conn.query / sql.rows），
		// 取真正承载查询的那一个，避免误判到 Rows 迭代 span。
		if span.SpanKind() == trace.SpanKindClient && span.Name() == "sql.conn.query" {
			sqlSpan = span
		}
	}
	if sqlSpan == nil {
		t.Fatalf("未产出 sql.conn.query span，已记录 %d 个 span（AC-ENT-06-4 未达成）", len(recorder.Ended()))
	}
	if got := sqlSpan.Parent().SpanID(); got != parent.SpanContext().SpanID() {
		t.Fatalf("SQL span 父节点 = %s, want %s（链路未串到入口 span）", got, parent.SpanContext().SpanID())
	}
	if got := attrValue(sqlSpan.Attributes(), "db.system"); got != stubDriverName {
		t.Errorf("db.system = %q, want %q", got, stubDriverName)
	}
	if !hasStatement(sqlSpan.Attributes()) {
		t.Errorf("SQL span 缺少语句属性，attributes=%v", sqlSpan.Attributes())
	}
}

// TestOpenWithoutTracerDoesNotCreateSpan 保证未接入 tracing 的进程（worker、迁移）
// 不被隐式采集：全局 TracerProvider 已设置时，Open 仍不产生 span。
func TestOpenWithoutTracerDoesNotCreateSpan(t *testing.T) {
	registerStubDriver()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(previous)

	database, err := Open(context.Background(), Config{
		Driver:   stubDriverName,
		DSN:      "stub://tracing",
		SkipPing: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	rows, err := database.SQL().QueryContext(context.Background(), "SELECT 1", nil)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	_ = rows.Close()

	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("Open 不应采集 SQL span，实际记录 %d 个", len(spans))
	}
}

func attrValue(attrs []attribute.KeyValue, key string) string {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// hasStatement 不锁定具体键名：稳定语义约定用 db.query.text，旧版用 db.statement。
func hasStatement(attrs []attribute.KeyValue) bool {
	for _, kv := range attrs {
		key := string(kv.Key)
		if (key == "db.statement" || key == "db.query.text") &&
			strings.Contains(kv.Value.AsString(), "SELECT") {
			return true
		}
	}
	return false
}
