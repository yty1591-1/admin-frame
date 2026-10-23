package iam_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// newSQLOutboxEvent 构造一条 IAM 审计事件（topic=iam.audit）。
func newSQLOutboxEvent(t *testing.T, eventID string, now time.Time) *outbox.Event {
	t.Helper()
	event, err := outbox.NewEvent(outbox.NewEventParams{
		Topic: app.TopicAudit,
		Envelope: mq.Envelope{
			EventID:    eventID,
			EventType:  app.EventTypeAudit,
			Version:    "v1",
			OccurredAt: now,
			Payload:    []byte(`{"action":"rbac.role.change"}`),
		},
		AggregateType: "iam_role",
		AggregateID:   "2001",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return event
}

// newUnsavedRole 构造一个尚未落库的 A 端角色（Write + Read 两个权限点）。
//
// 不复用 newRole：那个 helper 会立刻 Save，无法验证「事务回滚后角色不存在」。
func newUnsavedRole(t *testing.T, id, code string) *domain.Role {
	t.Helper()
	role, err := domain.NewRole(domain.NewRoleParams{
		ID:      id,
		Channel: domain.ChannelAdmin,
		Scope:   domain.RoleScopePlatform,
		Code:    code,
		Name:    "事务测试角色 " + code,
		Type:    domain.RoleTypeCustom,
		Actor:   "actor-1",
	})
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	set, err := domain.NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead, iam.PermOrderRead})
	if err != nil {
		t.Fatalf("NewPermissionSet: %v", err)
	}
	if _, err := role.ReplacePermissions(set, "actor-1", role.Version); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
	if err := role.ReplaceDataScopes([]domain.DataScope{
		{Resource: "role", Type: domain.DataScopeAll},
		{Resource: "order", Type: domain.DataScopeAll},
	}, "actor-1", role.Version); err != nil {
		t.Fatalf("ReplaceDataScopes: %v", err)
	}
	return role
}

// TestSQLUnitOfWorkCommitsRoleAndOutboxTogether 验证一次提交同时写入角色与审计事件。
//
// 这是 D9 的正面用例：仓储在 ctx 中拿到事务，业务写入与 outbox 同批可见。
func TestSQLUnitOfWorkCommitsRoleAndOutboxTogether(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roleRepo := persistence.NewSQLRoleRepository(sqlDB)
	outboxStore := outbox.NewMySQLStore(sqlDB)
	uow := persistence.NewSQLUnitOfWork(sqlDB)

	role := newUnsavedRole(t, "3101", "uow_commit")
	event := newSQLOutboxEvent(t, "3102", time.Now().UTC())

	if err := uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := roleRepo.Save(ctx, role, 0); err != nil {
			return err
		}
		return outboxStore.Append(ctx, event)
	}); err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	loaded, err := roleRepo.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("role must be committed: %v", err)
	}
	if len(loaded.PermissionCodes()) != 2 {
		t.Fatalf("permissions = %v, want 2", loaded.PermissionCodes())
	}
	due, err := outboxStore.ClaimBatch(ctx, time.Now().UTC().Add(time.Minute), time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(due) != 1 || due[0].EventID != event.EventID {
		t.Fatalf("outbox must be committed with role: %+v", due)
	}
}

// TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails 验证 outbox 写失败时角色一并回滚。
//
// 该用例是本阶段的核心断言：改造前 Save 与 Append 分两次提交，进程在两次提交
// 之间崩溃会留下「变更生效但无审计」；改造后两者必须同生共死。
func TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roleRepo := persistence.NewSQLRoleRepository(sqlDB)
	outboxStore := outbox.NewMySQLStore(sqlDB)
	uow := persistence.NewSQLUnitOfWork(sqlDB)

	now := time.Now().UTC()
	role := newUnsavedRole(t, "3201", "uow_rollback")
	// 先独立落一条同 ID 事件，使事务内的 Append 命中唯一键冲突而失败。
	if err := outboxStore.Append(ctx, newSQLOutboxEvent(t, "3202", now)); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	err := uow.WithinTx(ctx, func(ctx context.Context) error {
		if saveErr := roleRepo.Save(ctx, role, 0); saveErr != nil {
			return saveErr
		}
		return outboxStore.Append(ctx, newSQLOutboxEvent(t, "3202", now))
	})
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		t.Fatalf("err = %v, want duplicate outbox id", err)
	}

	if _, findErr := roleRepo.FindByID(ctx, role.ID); !errors.Is(findErr, apperr.ErrNotFound) {
		t.Fatalf("role must be rolled back, got %v", findErr)
	}
	var orphans int
	if scanErr := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM iam_role_permission WHERE role_id = ?`, 3201).Scan(&orphans); scanErr != nil {
		t.Fatalf("count permission rows: %v", scanErr)
	}
	if orphans != 0 {
		t.Fatalf("permission rows leaked after rollback: %d", orphans)
	}
	due, err := outboxStore.ClaimBatch(ctx, now.Add(time.Minute), time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(due) != 1 || due[0].Attempts != 0 {
		t.Fatalf("seeded outbox row must stay untouched: %+v", due)
	}
}

// TestSQLUnitOfWorkNestedApplyReusesTransaction 验证嵌套事务复用：
// 内层 Save 不新开事务，外层回滚后内层写入同样消失。
func TestSQLUnitOfWorkNestedApplyReusesTransaction(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roleRepo := persistence.NewSQLRoleRepository(sqlDB)
	uow := persistence.NewSQLUnitOfWork(sqlDB)

	role := newUnsavedRole(t, "3301", "uow_nested")
	sentinel := errors.New("outer failed after nested write")

	err := uow.WithinTx(ctx, func(ctx context.Context) error {
		if saveErr := roleRepo.Save(ctx, role, 0); saveErr != nil {
			return saveErr
		}
		// 仓储内部再包一层（与 RoleService 的 uow 调用等价）：必须复用外层事务。
		if nestedErr := uow.WithinTx(ctx, func(nested context.Context) error {
			if tx, ok := db.TxFromContext(nested); !ok || tx == nil {
				t.Fatal("nested callback must inherit the outer transaction")
			}
			return nil
		}); nestedErr != nil {
			return nestedErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if _, findErr := roleRepo.FindByID(ctx, role.ID); !errors.Is(findErr, apperr.ErrNotFound) {
		t.Fatalf("nested write must be rolled back with outer tx, got %v", findErr)
	}
}

// 编译期断言：SQLUnitOfWork 满足用例层端口。
var _ app.UnitOfWork = (*persistence.SQLUnitOfWork)(nil)
