package migrate_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

func baseMigrationFiles() map[string]string {
	return map[string]string{
		"000001_create_orders.up.sql":     "CREATE TABLE orders (id BIGINT PRIMARY KEY);\nCREATE INDEX idx_orders_id ON orders (id);",
		"000001_create_orders.down.sql":   "DROP TABLE orders;",
		"000002_add_orders_name.up.sql":   "ALTER TABLE orders ADD COLUMN name VARCHAR(32);",
		"000002_add_orders_name.down.sql": "ALTER TABLE orders DROP COLUMN name;",
	}
}

func loadFiles(t *testing.T, files map[string]string, opts migrate.LoadOptions) []migrate.Migration {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	migs, err := migrate.LoadFS(fsys, "migrations", opts)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	return migs
}

func newTestRunner(t *testing.T, db *sql.DB, migs []migrate.Migration, tune ...func(*migrate.Config)) *migrate.Runner {
	t.Helper()
	cfg := migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric}
	for _, fn := range tune {
		fn(&cfg)
	}
	r, err := migrate.New(db, cfg)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return r
}

func TestUpStatusAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	r := newTestRunner(t, db, migs)

	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Current != 0 || len(status.Pending) != 2 || status.Dirty {
		t.Fatalf("initial status=%+v", status)
	}

	res, err := r.Up(ctx)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(res.Steps) != 2 || res.Steps[0].Version != 1 || res.Steps[1].Version != 2 {
		t.Fatalf("steps=%+v", res.Steps)
	}
	if res.Steps[0].Statements != 2 || res.Steps[1].Statements != 1 {
		t.Fatalf("statement counts=%d,%d", res.Steps[0].Statements, res.Steps[1].Statements)
	}

	stmts := state.migrationStatements()
	if len(stmts) != 3 {
		t.Fatalf("executed %d statements: %q", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TABLE orders") || !strings.HasPrefix(stmts[1], "CREATE INDEX") {
		t.Fatalf("unexpected order: %q", stmts)
	}

	rec, ok := state.record(1)
	if !ok || rec.dirty || rec.checksum != migs[0].Checksum || rec.description != "create_orders" {
		t.Fatalf("record 1=%+v ok=%v", rec, ok)
	}

	status, err = r.Status(ctx)
	if err != nil {
		t.Fatalf("status after up: %v", err)
	}
	if status.Current != 2 || len(status.Pending) != 0 || status.Dirty || len(status.Drift) != 0 || len(status.Missing) != 0 {
		t.Fatalf("status after up=%+v", status)
	}

	res, err = r.Up(ctx)
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("second up applied %+v", res.Steps)
	}
	if got := len(state.migrationStatements()); got != 3 {
		t.Fatalf("second up executed additional statements: %d", got)
	}
}

func TestUpDryRun(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	r := newTestRunner(t, db, migs, func(c *migrate.Config) { c.DryRun = true })

	res, err := r.Up(ctx)
	if err != nil {
		t.Fatalf("dry-run up: %v", err)
	}
	if !res.DryRun || len(res.Steps) != 2 || res.Steps[0].Duration != 0 {
		t.Fatalf("result=%+v", res)
	}
	if got := state.migrationStatements(); len(got) != 0 {
		t.Fatalf("dry-run executed statements: %q", got)
	}
	if _, ok := state.record(1); ok {
		t.Fatal("dry-run wrote history")
	}
}

func TestUpChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	state.seedRecord(fakeRecord{
		version: 1, description: "create_orders", checksum: "deadbeef",
		appliedAt: time.Now().UTC(),
	})
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("err=%v, want ErrChecksumMismatch", err)
	}
	if got := state.migrationStatements(); len(got) != 0 {
		t.Fatalf("migrations ran despite drift: %q", got)
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Drift) != 1 || status.Drift[0] != 1 {
		t.Fatalf("drift=%v", status.Drift)
	}
}

func TestUpDirty(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	state.seedRecord(fakeRecord{
		version: 1, description: "create_orders", checksum: migs[0].Checksum,
		appliedAt: time.Now().UTC(), dirty: true,
	})
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrDirty) {
		t.Fatalf("err=%v, want ErrDirty", err)
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Dirty {
		t.Fatal("status should report dirty")
	}
}

func TestUpOutOfOrder(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	state.seedRecord(fakeRecord{
		version: 2, description: "add_orders_name", checksum: migs[1].Checksum,
		appliedAt: time.Now().UTC(),
	})
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrOutOfOrder) {
		t.Fatalf("err=%v, want ErrOutOfOrder", err)
	}

	r = newTestRunner(t, db, migs, func(c *migrate.Config) { c.AllowOutOfOrder = true })
	res, err := r.Up(ctx)
	if err != nil {
		t.Fatalf("up with AllowOutOfOrder: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Version != 1 {
		t.Fatalf("steps=%+v", res.Steps)
	}
}

func TestUpMissingApplied(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	state.seedRecord(fakeRecord{
		version: 9, description: "removed", checksum: "x", appliedAt: time.Now().UTC(),
	})
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrMissingMigration) {
		t.Fatalf("err=%v, want ErrMissingMigration", err)
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Missing) != 1 || status.Missing[0] != 9 {
		t.Fatalf("missing=%v", status.Missing)
	}
}

func TestDownAndDownTo(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	r := newTestRunner(t, db, migs)
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	res, err := r.Down(ctx)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Version != 2 || res.Steps[0].Direction != migrate.DirectionDown {
		t.Fatalf("down steps=%+v", res.Steps)
	}
	if _, ok := state.record(2); ok {
		t.Fatal("history for version 2 not deleted")
	}
	if _, ok := state.record(1); !ok {
		t.Fatal("history for version 1 should remain")
	}
	if !containsStatement(state.migrationStatements(), "DROP COLUMN name") {
		t.Fatalf("down sql not executed: %q", state.migrationStatements())
	}

	res, err = r.DownTo(ctx, 0)
	if err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Version != 1 {
		t.Fatalf("down to 0 steps=%+v", res.Steps)
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Current != 0 || len(status.Pending) != 2 {
		t.Fatalf("status=%+v", status)
	}

	res, err = r.Down(ctx)
	if err != nil {
		t.Fatalf("down on empty: %v", err)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("down on empty steps=%+v", res.Steps)
	}
	if _, err := r.DownSteps(ctx, 0); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("down steps 0 err=%v", err)
	}
}

func TestDownToKeepsBoundary(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	r := newTestRunner(t, db, migs)
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	res, err := r.DownTo(ctx, 1)
	if err != nil {
		t.Fatalf("down to 1: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Version != 2 {
		t.Fatalf("steps=%+v", res.Steps)
	}
	if _, ok := state.record(1); !ok {
		t.Fatal("version 1 should be kept")
	}

	res, err = r.DownTo(ctx, 1)
	if err != nil {
		t.Fatalf("second down to 1: %v", err)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("second down steps=%+v", res.Steps)
	}
}

func TestDownMissingDown(t *testing.T) {
	ctx := context.Background()
	db, _ := newFakeDB(t)
	migs := loadFiles(t, map[string]string{
		"000001_create.up.sql": "CREATE TABLE t (id INT);",
	}, migrate.LoadOptions{AllowMissingDown: true})
	r := newTestRunner(t, db, migs)
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := r.Down(ctx); !errors.Is(err, migrate.ErrMissingDown) {
		t.Fatalf("err=%v, want ErrMissingDown", err)
	}
}

func TestUpFailureTransactional(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, map[string]string{
		"000001_broken.up.sql":   "CREATE TABLE t (id INT);\nFAIL_SQL;",
		"000001_broken.down.sql": "DROP TABLE t;",
	}, migrate.LoadOptions{})
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrStatementFailed) {
		t.Fatalf("err=%v, want ErrStatementFailed", err)
	}
	if _, ok := state.record(1); ok {
		t.Fatal("failed transactional migration must roll back history")
	}
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Dirty || len(status.Pending) != 1 {
		t.Fatalf("status=%+v", status)
	}
}

func TestUpFailureNoTxLeavesDirty(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, map[string]string{
		"000001_broken.up.sql":   "-- +migrate no-transaction\nCREATE INDEX CONCURRENTLY idx ON t (id);\nFAIL_SQL;",
		"000001_broken.down.sql": "DROP INDEX idx;",
	}, migrate.LoadOptions{})
	if !migs[0].NoTx {
		t.Fatal("NoTx directive not detected")
	}
	r := newTestRunner(t, db, migs)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrStatementFailed) {
		t.Fatalf("err=%v, want ErrStatementFailed", err)
	}
	rec, ok := state.record(1)
	if !ok || !rec.dirty {
		t.Fatalf("dirty record=%+v ok=%v", rec, ok)
	}
	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrDirty) {
		t.Fatalf("second up err=%v, want ErrDirty", err)
	}
}

func TestUpNoTxSuccess(t *testing.T) {
	ctx := context.Background()
	db, state := newFakeDB(t)
	migs := loadFiles(t, baseMigrationFiles(), migrate.LoadOptions{})
	r := newTestRunner(t, db, migs, func(c *migrate.Config) { c.NoSplit = true })
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	rec, ok := state.record(1)
	if !ok || rec.dirty {
		t.Fatalf("record=%+v ok=%v", rec, ok)
	}
	if got := state.migrationStatements(); len(got) != 2 {
		t.Fatalf("NoSplit should execute one statement per migration, got %q", got)
	}
}

func TestObserverAndTextReporter(t *testing.T) {
	ctx := context.Background()
	db, _ := newFakeDB(t)
	migs := loadFiles(t, map[string]string{
		"000001_create.up.sql":   "CREATE TABLE t (id INT);",
		"000001_create.down.sql": "DROP TABLE t;",
	}, migrate.LoadOptions{})

	var kinds []migrate.EventKind
	var buf bytes.Buffer
	r := newTestRunner(t, db, migs, func(c *migrate.Config) {
		c.Observer = func(e migrate.Event) {
			kinds = append(kinds, e.Kind)
			migrate.TextReporter(&buf)(e)
		}
	})
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(kinds) != 2 || kinds[0] != migrate.EventStart || kinds[1] != migrate.EventDone {
		t.Fatalf("kinds=%v", kinds)
	}
	if !strings.Contains(buf.String(), "applying 000001_create") || !strings.Contains(buf.String(), "OK 000001_create") {
		t.Fatalf("reporter output=%q", buf.String())
	}
}

func TestLocks(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		dialect     migrate.Dialect
		wantQueries []string
	}{
		{name: "postgres", dialect: migrate.DialectPostgres, wantQueries: []string{"pg_advisory_lock", "pg_advisory_unlock"}},
		{name: "mysql", dialect: migrate.DialectMySQL, wantQueries: []string{"GET_LOCK", "RELEASE_LOCK"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db, state := newFakeDB(t)
			migs := loadFiles(t, map[string]string{
				"000001_create.up.sql":   "CREATE TABLE t (id INT);",
				"000001_create.down.sql": "DROP TABLE t;",
			}, migrate.LoadOptions{})
			r := newTestRunner(t, db, migs, func(c *migrate.Config) { c.Dialect = tc.dialect })
			if _, err := r.Up(ctx); err != nil {
				t.Fatalf("up: %v", err)
			}
			queries := strings.Join(state.allQueries(), "\n")
			for _, want := range tc.wantQueries {
				if !strings.Contains(queries, want) {
					t.Fatalf("missing %s in queries:\n%s", want, queries)
				}
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	migs := []migrate.Migration{{Version: 1, Description: "x", Up: "SELECT 1;"}}
	if _, err := migrate.New(nil, migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("nil db err=%v", err)
	}
	db, _ := newFakeDB(t)
	if _, err := migrate.New(db, migrate.Config{Migrations: migs}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("empty dialect err=%v", err)
	}
	if _, err := migrate.New(db, migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric, Table: "bad name"}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("bad table err=%v", err)
	}
	if _, err := migrate.New(db, migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric, Schema: "bad-schema"}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("bad schema err=%v", err)
	}
	if _, err := migrate.New(db, migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric, LockTimeout: -time.Second}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("negative lock timeout err=%v", err)
	}
}

func TestNewNormalizesManualMigrations(t *testing.T) {
	db, _ := newFakeDB(t)
	in := []migrate.Migration{{
		Version:     1,
		Description: "manual",
		Up:          "-- +migrate no-transaction\nSELECT 1;",
	}}
	r, err := migrate.New(db, migrate.Config{Migrations: in, Dialect: migrate.DialectGeneric})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got := r.Config().Migrations[0]
	if got.Checksum == "" || !got.NoTx {
		t.Fatalf("normalized migration=%+v", got)
	}
	if in[0].Checksum != "" || in[0].NoTx {
		t.Fatalf("caller slice must not be mutated: %+v", in[0])
	}
}

func TestNewRequiresTwoConnectionsForAdvisoryLock(t *testing.T) {
	db, _ := newFakeDB(t)
	db.SetMaxOpenConns(1)
	migs := []migrate.Migration{{Version: 1, Description: "x", Up: "SELECT 1;"}}
	if _, err := migrate.New(db, migrate.Config{Migrations: migs, Dialect: migrate.DialectPostgres}); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("postgres err=%v, want ErrInvalidConfig", err)
	}
	if _, err := migrate.New(db, migrate.Config{Migrations: migs, Dialect: migrate.DialectGeneric}); err != nil {
		t.Fatalf("generic should allow single connection: %v", err)
	}
}

func TestParseDialect(t *testing.T) {
	cases := map[string]migrate.Dialect{
		"postgres":   migrate.DialectPostgres,
		"PostgreSQL": migrate.DialectPostgres,
		"pgx":        migrate.DialectPostgres,
		"mysql":      migrate.DialectMySQL,
		"mariadb":    migrate.DialectMySQL,
		"sqlite3":    migrate.DialectSQLite,
		"generic":    migrate.DialectGeneric,
	}
	for in, want := range cases {
		got, err := migrate.ParseDialect(in)
		if err != nil || got != want {
			t.Fatalf("ParseDialect(%q)=(%q,%v), want %q", in, got, err, want)
		}
	}
	if _, err := migrate.ParseDialect("oracle"); !errors.Is(err, migrate.ErrInvalidConfig) {
		t.Fatalf("oracle err=%v", err)
	}
}

func TestTextReporterNil(t *testing.T) {
	if migrate.TextReporter(nil) != nil {
		t.Fatal("nil writer should return nil observer")
	}
}

func containsStatement(stmts []string, sub string) bool {
	for _, s := range stmts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
