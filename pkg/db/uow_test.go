package db_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/db"
)

// uowDriver 记录 Begin / Commit / Rollback 次数，用于验证事务边界真的生效。
type uowDriver struct {
	mu       sync.Mutex
	begins   int
	commits  int
	rollback int
}

func (d *uowDriver) Open(string) (driver.Conn, error) { return &uowConn{driver: d}, nil }

type uowConn struct{ driver *uowDriver }

func (c *uowConn) Prepare(string) (driver.Stmt, error) { return &uowStmt{}, nil }
func (c *uowConn) Close() error                        { return nil }
func (c *uowConn) Begin() (driver.Tx, error) {
	c.driver.mu.Lock()
	c.driver.begins++
	c.driver.mu.Unlock()
	return &uowTx{driver: c.driver}, nil
}

type uowTx struct{ driver *uowDriver }

func (t *uowTx) Commit() error {
	t.driver.mu.Lock()
	t.driver.commits++
	t.driver.mu.Unlock()
	return nil
}

func (t *uowTx) Rollback() error {
	t.driver.mu.Lock()
	t.driver.rollback++
	t.driver.mu.Unlock()
	return nil
}

type uowStmt struct{}

func (s *uowStmt) Close() error  { return nil }
func (s *uowStmt) NumInput() int { return -1 }
func (s *uowStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *uowStmt) Query([]driver.Value) (driver.Rows, error) { return &testRows{}, nil }

// uowRecorder 是全局唯一的驱动实例：database/sql 对同一驱动名只允许注册一次，
// 因此计数器在每次新建连接时重置，而不是每个用例注册一个新驱动。
var (
	uowRegisterOnce sync.Once
	uowRecorder     = &uowDriver{}
)

// newUOWDB 打开计数驱动并把计数器归零。
func newUOWDB(t *testing.T) (*sql.DB, *uowDriver) {
	t.Helper()
	uowRegisterOnce.Do(func() { sql.Register("uowtest", uowRecorder) })
	uowRecorder.reset()
	sqlDB, err := sql.Open("uowtest", "uow")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB, uowRecorder
}

// reset 归零计数器（用例之间互不影响）。
func (d *uowDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.begins, d.commits, d.rollback = 0, 0, 0
}

// TestWithinUnitOfWorkCommitsAndExposesTx 验证正常路径提交一次且 ctx 可见事务。
func TestWithinUnitOfWorkCommitsAndExposesTx(t *testing.T) {
	sqlDB, recorder := newUOWDB(t)
	ctx := context.Background()

	if _, ok := db.TxFromContext(ctx); ok {
		t.Fatal("bare context must not carry a transaction")
	}

	called := 0
	err := db.WithinUnitOfWork(ctx, sqlDB, nil, func(ctx context.Context) error {
		called++
		tx, ok := db.TxFromContext(ctx)
		if !ok || tx == nil {
			t.Fatal("callback context must carry *sql.Tx")
		}
		_, execErr := tx.ExecContext(ctx, "INSERT INTO t VALUES (1)")
		return execErr
	})
	if err != nil {
		t.Fatalf("WithinUnitOfWork: %v", err)
	}
	if called != 1 {
		t.Fatalf("callback calls = %d, want 1", called)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.begins != 1 || recorder.commits != 1 || recorder.rollback != 0 {
		t.Fatalf("tx counters = begins %d commits %d rollbacks %d, want 1/1/0",
			recorder.begins, recorder.commits, recorder.rollback)
	}
}

// TestWithinUnitOfWorkRollsBackAndKeepsCause 验证错误回滚且保留原始错误链。
func TestWithinUnitOfWorkRollsBackAndKeepsCause(t *testing.T) {
	sqlDB, recorder := newUOWDB(t)
	sentinel := errors.New("outbox append failed")

	err := db.WithinUnitOfWork(context.Background(), sqlDB, nil, func(context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapping sentinel", err)
	}
	if !db.IsTxFailed(err) {
		t.Fatalf("err = %v, want ErrTxFailed chain", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.begins != 1 || recorder.commits != 0 || recorder.rollback != 1 {
		t.Fatalf("tx counters = begins %d commits %d rollbacks %d, want 1/0/1",
			recorder.begins, recorder.commits, recorder.rollback)
	}
}

// TestWithinUnitOfWorkJoinsExistingTx 验证嵌套调用复用同一事务，不会各自提交。
func TestWithinUnitOfWorkJoinsExistingTx(t *testing.T) {
	sqlDB, recorder := newUOWDB(t)

	var outer, inner *sql.Tx
	err := db.WithinUnitOfWork(context.Background(), sqlDB, nil, func(ctx context.Context) error {
		outer, _ = db.TxFromContext(ctx)
		return db.WithinUnitOfWork(ctx, sqlDB, nil, func(nested context.Context) error {
			inner, _ = db.TxFromContext(nested)
			return nil
		})
	})
	if err != nil {
		t.Fatalf("WithinUnitOfWork: %v", err)
	}
	if outer == nil || inner == nil || outer != inner {
		t.Fatalf("nested call must reuse outer tx: outer=%p inner=%p", outer, inner)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.begins != 1 || recorder.commits != 1 {
		t.Fatalf("nested call opened %d tx / %d commits, want 1/1", recorder.begins, recorder.commits)
	}
}

// TestWithinUnitOfWorkRejectsInvalidInput 验证 fail-closed 的入参校验。
func TestWithinUnitOfWorkRejectsInvalidInput(t *testing.T) {
	sqlDB, _ := newUOWDB(t)
	ctx := context.Background()

	if err := db.WithinUnitOfWork(ctx, nil, nil, func(context.Context) error { return nil }); !errors.Is(err, db.ErrInvalidConfig) {
		t.Fatalf("nil db err = %v, want ErrInvalidConfig", err)
	}
	if err := db.WithinUnitOfWork(ctx, sqlDB, nil, nil); !errors.Is(err, db.ErrInvalidConfig) {
		t.Fatalf("nil fn err = %v, want ErrInvalidConfig", err)
	}
}

// TestWithTxNilClearsTx 验证显式清空事务，避免脱离事务后误用旧句柄。
func TestWithTxNilClearsTx(t *testing.T) {
	sqlDB, _ := newUOWDB(t)
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	ctx := db.WithTx(context.Background(), tx)
	if _, ok := db.TxFromContext(ctx); !ok {
		t.Fatal("WithTx must expose transaction")
	}
	if _, ok := db.TxFromContext(db.WithTx(ctx, nil)); ok {
		t.Fatal("WithTx(ctx, nil) must clear transaction")
	}
}
