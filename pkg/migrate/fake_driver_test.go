package migrate_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain 注册内存伪驱动；所有迁移执行路径均由该驱动记录 SQL 并模拟历史表。
func TestMain(m *testing.M) {
	sql.Register(fakeDriverName, &fakeDriver{})
	os.Exit(m.Run())
}

const fakeDriverName = "migratetest"

type fakeRecord struct {
	version     int64
	description string
	checksum    string
	appliedAt   time.Time
	executionMS int64
	dirty       bool
}

type fakeState struct {
	mu           sync.Mutex
	history      map[int64]fakeRecord
	queries      []string
	migrationSQL []string
}

type fakeDriver struct{}

var fakeRegistry = struct {
	sync.Mutex
	m map[string]*fakeState
}{m: make(map[string]*fakeState)}

func (d *fakeDriver) Open(name string) (driver.Conn, error) {
	fakeRegistry.Lock()
	st := fakeRegistry.m[name]
	fakeRegistry.Unlock()
	if st == nil {
		return nil, fmt.Errorf("fake: unknown dsn %q", name)
	}
	return &fakeConn{st: st}, nil
}

type fakeConn struct{ st *fakeState }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake: Prepare not supported")
}

func (c *fakeConn) Close() error { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.st.mu.Lock()
	snapshot := make(map[int64]fakeRecord, len(c.st.history))
	for k, v := range c.st.history {
		snapshot[k] = v
	}
	c.st.mu.Unlock()
	return &fakeTx{st: c.st, snapshot: snapshot}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.st.exec(query, args)
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.st.query(query, args)
}

type fakeTx struct {
	st       *fakeState
	snapshot map[int64]fakeRecord
	done     bool
}

func (t *fakeTx) Commit() error { t.done = true; return nil }

func (t *fakeTx) Rollback() error {
	if t.done {
		return nil
	}
	t.st.mu.Lock()
	t.st.history = t.snapshot
	t.st.mu.Unlock()
	t.done = true
	return nil
}

func (s *fakeState) exec(query string, args []driver.NamedValue) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := strings.TrimSpace(query)
	s.queries = append(s.queries, q)

	switch {
	case strings.HasPrefix(q, "CREATE TABLE IF NOT EXISTS") && strings.Contains(q, "schema_migrations"):
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(q, "INSERT INTO") && strings.Contains(q, "schema_migrations"):
		if len(args) != 6 {
			return nil, fmt.Errorf("fake: history insert expects 6 args, got %d", len(args))
		}
		rec := fakeRecord{
			version:     asInt64(args[0].Value),
			description: asString(args[1].Value),
			checksum:    asString(args[2].Value),
			appliedAt:   asTime(args[3].Value),
			executionMS: asInt64(args[4].Value),
			dirty:       asBool(args[5].Value),
		}
		s.history[rec.version] = rec
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE") && strings.Contains(q, "schema_migrations"):
		if strings.Contains(q, "execution_ms") {
			if len(args) != 3 {
				return nil, fmt.Errorf("fake: finish apply expects 3 args, got %d", len(args))
			}
			version := asInt64(args[2].Value)
			rec, ok := s.history[version]
			if !ok {
				return driver.RowsAffected(0), nil
			}
			rec.dirty = asBool(args[0].Value)
			rec.executionMS = asInt64(args[1].Value)
			s.history[version] = rec
			return driver.RowsAffected(1), nil
		}
		if len(args) != 2 {
			return nil, fmt.Errorf("fake: mark dirty expects 2 args, got %d", len(args))
		}
		version := asInt64(args[1].Value)
		rec, ok := s.history[version]
		if !ok {
			return driver.RowsAffected(0), nil
		}
		rec.dirty = asBool(args[0].Value)
		s.history[version] = rec
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "DELETE FROM") && strings.Contains(q, "schema_migrations"):
		if len(args) != 1 {
			return nil, fmt.Errorf("fake: delete expects 1 arg, got %d", len(args))
		}
		delete(s.history, asInt64(args[0].Value))
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "pg_advisory_lock"),
		strings.Contains(q, "pg_advisory_unlock"),
		strings.Contains(q, "GET_LOCK"),
		strings.Contains(q, "RELEASE_LOCK"):
		return driver.RowsAffected(0), nil
	}

	s.migrationSQL = append(s.migrationSQL, q)
	if strings.Contains(q, "FAIL_SQL") {
		return nil, errors.New("fake: forced failure")
	}
	return driver.RowsAffected(1), nil
}

func (s *fakeState) query(query string, _ []driver.NamedValue) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := strings.TrimSpace(query)
	s.queries = append(s.queries, q)

	switch {
	case strings.HasPrefix(q, "SELECT version") && strings.Contains(q, "schema_migrations"):
		keys := make([]int64, 0, len(s.history))
		for version := range s.history {
			keys = append(keys, version)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		data := make([][]driver.Value, 0, len(keys))
		for _, version := range keys {
			rec := s.history[version]
			data = append(data, []driver.Value{
				rec.version, rec.description, rec.checksum, rec.appliedAt, rec.executionMS, rec.dirty,
			})
		}
		return &fakeRows{
			cols: []string{"version", "description", "checksum", "applied_at", "execution_ms", "dirty"},
			data: data,
		}, nil
	case strings.HasPrefix(q, "SELECT GET_LOCK"):
		return &fakeRows{cols: []string{"GET_LOCK"}, data: [][]driver.Value{{int64(1)}}}, nil
	case strings.HasPrefix(q, "SELECT RELEASE_LOCK"):
		return &fakeRows{cols: []string{"RELEASE_LOCK"}, data: [][]driver.Value{{int64(1)}}}, nil
	default:
		return nil, fmt.Errorf("fake: unsupported query %q", q)
	}
}

type fakeRows struct {
	cols []string
	data [][]driver.Value
	idx  int
}

func (r *fakeRows) Columns() []string { return r.cols }

func (r *fakeRows) Close() error { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

// newFakeDB 创建一个隔离的内存库（DSN 为测试名）并返回其底层状态。
func newFakeDB(t *testing.T) (*sql.DB, *fakeState) {
	t.Helper()
	dsn := t.Name()
	st := &fakeState{history: make(map[int64]fakeRecord)}
	fakeRegistry.Lock()
	fakeRegistry.m[dsn] = st
	fakeRegistry.Unlock()

	db, err := sql.Open(fakeDriverName, dsn)
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() {
		// 忽略：测试清理阶段关闭连接池失败不影响断言结果。
		_ = db.Close()
		fakeRegistry.Lock()
		delete(fakeRegistry.m, dsn)
		fakeRegistry.Unlock()
	})
	return db, st
}

// seedRecord 直接写入历史记录，用于构造漂移 / dirty / 缺失场景。
func (s *fakeState) seedRecord(rec fakeRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[rec.version] = rec
}

func (s *fakeState) record(version int64) (fakeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.history[version]
	return rec, ok
}

func (s *fakeState) migrationStatements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.migrationSQL...)
}

func (s *fakeState) allQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func asInt64(v driver.Value) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func asString(v driver.Value) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func asBool(v driver.Value) bool {
	switch b := v.(type) {
	case bool:
		return b
	case int64:
		return b != 0
	default:
		return false
	}
}

func asTime(v driver.Value) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}
