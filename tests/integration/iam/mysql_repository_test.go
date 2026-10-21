package iam_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/migrate"
)

// envDSN 是 MySQL 集成测试的连接串开关。
//
// CI 通过 mysql:8 service 注入；本地开发者可用 docker 启动同版本实例后手动设置。
// 未设置时显式跳过并打印原因，避免「静默通过」造成 SQL 未被验证的错觉。
const envDSN = "IAM_TEST_MYSQL_DSN"

var (
	setupOnce sync.Once
	setupDB   *sql.DB
	setupErr  error
)

// testDB 返回已完成迁移的测试数据库；未配置 DSN 时跳过当前用例。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	if dsn == "" {
		t.Skipf("%s is not set: MySQL integration test skipped (CI provides mysql:8 service)", envDSN)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("%s must enable parseTime=true (and loc=UTC) so DATETIME(3) scans into time.Time", envDSN)
	}

	setupOnce.Do(func() {
		setupDB, setupErr = openAndMigrate(dsn)
	})
	if setupErr != nil {
		t.Fatalf("prepare integration database: %v", setupErr)
	}
	reset(t, setupDB)
	return setupDB
}

// openAndMigrate 打开连接并应用 migrations/ 下的全部脚本。
func openAndMigrate(dsn string) (*sql.DB, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	root, err := repositoryRoot()
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	migrations, err := migrate.LoadDir(filepath.Join(root, "migrations"), migrate.LoadOptions{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	runner, err := migrate.New(sqlDB, migrate.Config{
		Migrations: migrations,
		Dialect:    migrate.DialectMySQL,
		Table:      "schema_migrations",
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if _, err := runner.Up(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// repositoryRoot 从测试文件位置回溯到仓库根目录。
func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve test file path")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// reset 清空业务表（保留种子权限）。
//
// 顺序遵循外键 RESTRICT：先删引用方，再删角色。
func reset(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		"DELETE FROM iam_role_binding",
		"DELETE FROM iam_role_data_scope",
		"DELETE FROM iam_role_permission",
		"DELETE FROM iam_role",
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("reset %q: %v", stmt, err)
		}
	}
}

// newRole 构造带授权与数据范围的 A 端角色并落库。
func newRole(t *testing.T, repo *persistence.SQLRoleRepository, id, code string, perms []iam.PermissionCode, scopes []domain.DataScope) *domain.Role {
	t.Helper()
	role, err := domain.NewRole(domain.NewRoleParams{
		ID:      id,
		Channel: domain.ChannelAdmin,
		Scope:   domain.RoleScopePlatform,
		Code:    code,
		Name:    "测试角色 " + code,
		Type:    domain.RoleTypeCustom,
		Actor:   "actor-1",
	})
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	if perms != nil {
		set, err := domain.NewPermissionSet(perms)
		if err != nil {
			t.Fatalf("NewPermissionSet: %v", err)
		}
		if _, err := role.ReplacePermissions(set, "actor-1", role.Version); err != nil {
			t.Fatalf("ReplacePermissions: %v", err)
		}
	}
	if scopes != nil {
		if err := role.ReplaceDataScopes(scopes, "actor-1", role.Version); err != nil {
			t.Fatalf("ReplaceDataScopes: %v", err)
		}
	}
	if err := repo.Save(context.Background(), role, 0); err != nil {
		t.Fatalf("save role: %v", err)
	}
	return role
}

func TestSQLRoleRepositoryRoundTrip(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLRoleRepository(sqlDB)

	scope, err := domain.NewDataScope("order", domain.DataScopeMerchantSet, []string{"m-1", "m-2"})
	if err != nil {
		t.Fatal(err)
	}
	created := newRole(t, repo, "1001", "ops_readonly",
		[]iam.PermissionCode{iam.PermRoleRead, iam.PermOrderRead}, []domain.DataScope{scope})

	loaded, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Code != "ops_readonly" || loaded.Status != domain.RoleStatusActive {
		t.Fatalf("role=%+v", loaded)
	}
	if loaded.Version != created.Version {
		t.Fatalf("version=%d, want %d", loaded.Version, created.Version)
	}
	if !loaded.HasPermission(iam.PermOrderRead) || loaded.Permissions.Len() != 2 {
		t.Fatalf("permissions=%v", loaded.PermissionCodes())
	}
	gotScope, ok := loaded.DataScopeFor("order")
	if !ok || gotScope.Type != domain.DataScopeMerchantSet || len(gotScope.MerchantIDs) != 2 {
		t.Fatalf("scope=%+v ok=%v", gotScope, ok)
	}

	byCode, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "ops_readonly")
	if err != nil {
		t.Fatal(err)
	}
	if byCode.ID != created.ID {
		t.Fatalf("id=%s", byCode.ID)
	}

	if _, err := repo.FindByID(ctx, "990001"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	if _, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestSQLRoleRepositoryUniqueCode(t *testing.T) {
	sqlDB := testDB(t)
	repo := persistence.NewSQLRoleRepository(sqlDB)
	newRole(t, repo, "1001", "ops_readonly", nil, nil)

	dup, err := domain.NewRole(domain.NewRoleParams{
		ID: "1002", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops_readonly", Name: "重复", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), dup, 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}

	// B 端同名 code 属于不同唯一键。
	biz, err := domain.NewRole(domain.NewRoleParams{
		ID: "1003", Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant,
		MerchantID: "m-1", Code: "ops_readonly", Name: "B 端", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), biz, 0); err != nil {
		t.Fatalf("biz role must be allowed: %v", err)
	}
}

func TestSQLRoleRepositoryOptimisticLock(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLRoleRepository(sqlDB)
	role := newRole(t, repo, "1001", "ops_readonly", nil, nil)
	storedVersion := role.Version

	if err := role.UpdateDetails("新名字", "说明", "actor-2", role.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, role, storedVersion); err != nil {
		t.Fatalf("valid update: %v", err)
	}

	// 用过期版本再次写入必须冲突。
	stale := *role
	stale.Name = "过期写入"
	if err := repo.Save(ctx, &stale, storedVersion); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}

	loaded, err := repo.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name == "过期写入" {
		t.Fatal("stale write must not be applied")
	}

	// 更新不存在的角色返回 NotFound。
	missing, err := domain.NewRole(domain.NewRoleParams{
		ID: "1099", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "missing_role", Name: "不存在", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, missing, 1); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestSQLRoleRepositoryImmutableFields(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLRoleRepository(sqlDB)
	role := newRole(t, repo, "1001", "ops_readonly", nil, nil)

	tampered := *role
	tampered.Code = "renamed_code"
	tampered.Version = role.Version + 1
	if err := repo.Save(ctx, &tampered, role.Version); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}
}

func TestSQLRoleRepositoryListAndSoftDelete(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLRoleRepository(sqlDB)

	newRole(t, repo, "1001", "alpha", nil, nil)
	beta := newRole(t, repo, "1002", "beta", nil, nil)
	if err := beta.Disable("actor-1", beta.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, beta, beta.Version-1); err != nil {
		t.Fatal(err)
	}

	// 软删 alpha。
	alpha, err := repo.FindByID(ctx, "1001")
	if err != nil {
		t.Fatal(err)
	}
	if err := alpha.Delete("actor-1", alpha.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, alpha, alpha.Version-1); err != nil {
		t.Fatal(err)
	}

	all, err := repo.List(ctx, domain.RoleFilter{Channel: domain.ChannelAdmin, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Code != "beta" {
		t.Fatalf("list=%d", len(all))
	}

	// 软删角色仍可按 code 查到（code 不可复用），但不再出现在列表。
	deleted, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "alpha")
	if err != nil {
		t.Fatalf("deleted role must be findable: %v", err)
	}
	if !deleted.IsDeleted() {
		t.Fatal("expected deleted role")
	}

	// 可复用性由唯一索引保证：同 code 新建必须冲突。
	reused, err := domain.NewRole(domain.NewRoleParams{
		ID: "1003", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "alpha", Name: "重建", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, reused, 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err=%v, want conflict (code must not be reusable)", err)
	}

	// 状态过滤 + 关键字过滤。
	disabled, err := repo.List(ctx, domain.RoleFilter{Status: domain.RoleStatusDisabled, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 1 || disabled[0].Code != "beta" {
		t.Fatalf("disabled=%d", len(disabled))
	}
	keyword, err := repo.List(ctx, domain.RoleFilter{Keyword: "bet", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 {
		t.Fatalf("keyword=%d", len(keyword))
	}
}

func TestSQLBindingRepositoryLifecycle(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roles := persistence.NewSQLRoleRepository(sqlDB)
	bindings := persistence.NewSQLBindingRepository(sqlDB)

	newRole(t, roles, "1001", "ops_readonly", nil, nil)
	now := time.Now().UTC()
	expires := now.Add(time.Hour)

	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2001", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", RoleID: "1001", ExpiresAt: &expires, GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}

	// 过期过滤（在刷新前）：首次绑定带 1 小时有效期。
	expired, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", At: expires.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired=%d, want 0", len(expired))
	}

	// 重复绑定不新增行：刷新有效期 / 授权人并保留原主键（幂等 + 可重新生效）。
	duplicate, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2002", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", RoleID: "1001", GrantedBy: "actor-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, duplicate); err != nil {
		t.Fatalf("duplicate binding must be idempotent: %v", err)
	}
	if _, err := bindings.FindByID(ctx, "2002"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("duplicate must not be inserted, err=%v", err)
	}
	persisted, err := bindings.FindByID(ctx, "2001")
	if err != nil {
		t.Fatalf("original binding must remain: %v", err)
	}
	if persisted.ExpiresAt != nil || persisted.GrantedBy != "actor-2" {
		t.Fatalf("duplicate must refresh expiry/granter, got %+v", persisted)
	}

	// 刷新后为长期有效，未来时刻仍应命中。
	active, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("active=%d", len(active))
	}
	refreshed, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", At: expires.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed) != 1 {
		t.Fatalf("refreshed=%d, want 1", len(refreshed))
	}

	byRole, err := bindings.ListByRole(ctx, "1001")
	if err != nil {
		t.Fatal(err)
	}
	if len(byRole) != 1 {
		t.Fatalf("byRole=%d", len(byRole))
	}

	if err := bindings.Delete(ctx, "2001"); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Delete(ctx, "2001"); err != nil {
		t.Fatalf("second delete must be idempotent: %v", err)
	}
	if _, err := bindings.FindByID(ctx, "2001"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestSQLBindingRepositoryTenantIsolation(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roles := persistence.NewSQLRoleRepository(sqlDB)
	bindings := persistence.NewSQLBindingRepository(sqlDB)

	bizRole, err := domain.NewRole(domain.NewRoleParams{
		ID: "1010", Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant,
		MerchantID: "m-1", Code: "merchant_owner", Name: "商户所有者", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := roles.Save(ctx, bizRole, 0); err != nil {
		t.Fatal(err)
	}
	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2001", Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "u-1", MerchantID: "m-1", RoleID: "1010", GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}

	// B 端空商户必须报错，而不是跨租户返回。
	if _, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff, SubjectID: "u-1",
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}

	other, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "u-1", MerchantID: "m-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("cross tenant leak: %d", len(other))
	}

	own, err := bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "u-1", MerchantID: "m-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 {
		t.Fatalf("own=%d, want 1", len(own))
	}
}

func TestSQLSnapshotRepository(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roles := persistence.NewSQLRoleRepository(sqlDB)
	bindings := persistence.NewSQLBindingRepository(sqlDB)
	snapshots := persistence.NewSQLSnapshotRepository(sqlDB)

	orderScope, err := domain.NewDataScope("order", domain.DataScopeAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	role := newRole(t, roles, "1001", "ops_readonly",
		[]iam.PermissionCode{iam.PermRoleRead, iam.PermOrderRead}, []domain.DataScope{orderScope})

	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2001", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", RoleID: role.ID, GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}

	query := domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-1",
	}
	snapshot, err := snapshots.Load(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Roles) != 1 || snapshot.Roles[0] != "ops_readonly" {
		t.Fatalf("roles=%v", snapshot.Roles)
	}
	if !snapshot.HasPermission(iam.PermRoleRead) || !snapshot.HasPermission(iam.PermOrderRead) {
		t.Fatalf("permissions=%v", snapshot.Permissions)
	}
	scope, ok := snapshot.ScopeFor("order")
	if !ok || scope.Type != domain.DataScopeAll {
		t.Fatalf("scope=%+v ok=%v", scope, ok)
	}
	if snapshot.Version != role.Version {
		t.Fatalf("version=%d, want %d", snapshot.Version, role.Version)
	}

	// 停用角色后不再进入快照。
	if err := role.Disable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if err := roles.Save(ctx, role, role.Version-1); err != nil {
		t.Fatal(err)
	}
	disabled, err := snapshots.Load(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Permissions) != 0 || len(disabled.Roles) != 0 {
		t.Fatalf("disabled role leaked into snapshot: %+v", disabled)
	}

	// 无绑定的主体返回空快照而非错误（默认拒绝，不产生 503）。
	empty, err := snapshots.Load(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Permissions) != 0 || empty.Version != 0 {
		t.Fatalf("empty snapshot=%+v", empty)
	}

	// 过期绑定不进入快照。
	expires := time.Now().UTC().Add(time.Minute)
	if err := role.Enable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if err := roles.Save(ctx, role, role.Version-1); err != nil {
		t.Fatal(err)
	}
	limited, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2002", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-2", RoleID: role.ID, ExpiresAt: &expires, GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, limited); err != nil {
		t.Fatal(err)
	}
	later, err := snapshots.Load(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-2",
		At: expires.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(later.Permissions) != 0 {
		t.Fatalf("expired binding leaked into snapshot: %v", later.Permissions)
	}
}

func TestSQLSnapshotRepositoryEmptyOnMissingPermissions(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roles := persistence.NewSQLRoleRepository(sqlDB)
	bindings := persistence.NewSQLBindingRepository(sqlDB)
	snapshots := persistence.NewSQLSnapshotRepository(sqlDB)

	// 角色未授予任何权限：快照为空但不算错误。
	role := newRole(t, roles, "1001", "empty_role", nil, nil)
	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2001", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", RoleID: role.ID, GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}

	snapshot, err := snapshots.Load(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Roles) != 0 || len(snapshot.Permissions) != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Scopes == nil {
		t.Fatal("scopes must be empty non-nil slice")
	}
}

// TestSQLRepositoriesRejectUnconfigured 验证 nil *sql.DB 时返回 50300 而不是 panic。
func TestSQLRepositoriesRejectUnconfigured(t *testing.T) {
	ctx := context.Background()
	roleRepo := persistence.NewSQLRoleRepository(nil)
	role, err := domain.NewRole(domain.NewRoleParams{
		ID: "1001", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops_readonly", Name: "测试", Actor: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := roleRepo.Save(ctx, role, 0); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err=%v, want unavailable", err)
	}

	bindingRepo := persistence.NewSQLBindingRepository(nil)
	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "2001", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-1", RoleID: "1001", GrantedBy: "actor-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindingRepo.Save(ctx, binding); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err=%v, want unavailable", err)
	}

	snapshotRepo := persistence.NewSQLSnapshotRepository(nil)
	if _, err := snapshotRepo.Load(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-1",
	}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err=%v, want unavailable", err)
	}
}
