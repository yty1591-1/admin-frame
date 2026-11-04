package tenant_test

// T-18 集成测试（P1-8 验收）：成员变更 → IAM 模板角色绑定在真实 MySQL 中的
// 生效与失效。覆盖点：
//  1. Create 事务内物化三个模板角色（ADR-0007）；
//  2. Add 成员 → 绑定落库（生效行）；
//  3. SetRole → 旧模板解绑 + 新模板生效；
//  4. 停用成员 → 解绑；复活 → 重绑；
//  5. 事务复用：绑定写入经租户域 Mutate 的同一 UoW（同 *sql.DB）。

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	iamapp "github.com/admin-frame/admin-frame/internal/iam/app"
	iampersistence "github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// snowflakeSeq 生成纯数字递增 ID：MySQL 角色仓储要求 ID 为正雪花整数
// （parseID 校验），字符串前缀形式的测试生成器会被拒绝。
type snowflakeSeq struct {
	n int64
}

// Next 返回自增整数 ID（1.7e18 段，避开保留 ID）。
func (g *snowflakeSeq) Next() (int64, error) {
	g.n++
	return int64(1700000000000000000) + g.n, nil
}

// NextString 返回十进制字符串形式。
func (g *snowflakeSeq) NextString() (string, error) {
	v, err := g.Next()
	return strconv.FormatInt(v, 10), err
}

// effectiveBindings 查询主体在租户内当前生效的模板绑定角色编码集合。
func effectiveBindings(t *testing.T, sqlDB *sql.DB, tenantID, subjectID string) map[string]bool {
	t.Helper()
	rows, err := sqlDB.Query(`
		SELECT ro.code
		  FROM iam_role_binding b
		  JOIN iam_role ro ON ro.id = b.role_id
		 WHERE b.merchant_id = ? AND b.subject_id = ? AND b.revoked_at IS NULL
		   AND b.expires_at IS NULL OR (b.expires_at IS NOT NULL AND b.expires_at > UTC_TIMESTAMP(3))
		 LIMIT 10`,
		tenantID, subjectID)
	if err != nil {
		t.Fatalf("query bindings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("scan binding code: %v", err)
		}
		out[code] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate bindings: %v", err)
	}
	return out
}

// TestMemberRoleBindingSyncOnMySQL 验证成员生命周期的绑定联动（T-18 / P1-8）。
func TestMemberRoleBindingSyncOnMySQL(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()

	tenantRepo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	roles := iampersistence.NewSQLRoleRepository(sqlDB)
	bindings := iampersistence.NewSQLBindingRepository(sqlDB)
	syncer, err := iamapp.NewRoleSyncer(roles, bindings, &snowflakeSeq{})
	if err != nil {
		t.Fatalf("NewRoleSyncerForTest() error = %v", err)
	}

	emitter := quietEmitter{}
	service, err := app.NewService(tenantRepo, &snowflakeSeq{}, emitter, app.Options{
		Service: "admin-api", Plans: tenantRepo, Members: tenantRepo, Quotas: tenantRepo, RoleSyncer: syncer,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(tenantRepo, tenantRepo, emitter, app.Options{
		Service: "admin-api", Quotas: tenantRepo, RoleSyncer: syncer,
	})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}

	// 1. Create：模板角色随租户物化（同事务）。
	created, err := service.Create(ctx, app.CreateInput{
		Name: "绑定联动集成", OwnerSubjectID: "u-owner", Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DELETE FROM iam_role_binding WHERE merchant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM iam_role_permission WHERE role_id IN (SELECT id FROM (SELECT id FROM iam_role WHERE merchant_id = ?) x)", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM iam_role WHERE merchant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant_member WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant_quota WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID)
	})

	roleRows, err := sqlDB.Query(
		`SELECT code FROM iam_role WHERE channel = 'biz' AND merchant_id = ? AND deleted_at IS NULL`,
		created.ID)
	if err != nil {
		t.Fatalf("query template roles: %v", err)
	}
	defer func() { _ = roleRows.Close() }()
	templates := map[string]bool{}
	for roleRows.Next() {
		var code string
		if err := roleRows.Scan(&code); err != nil {
			t.Fatalf("scan role code: %v", err)
		}
		templates[code] = true
	}
	for _, want := range []string{"tenant_owner", "tenant_admin", "tenant_member"} {
		if !templates[want] {
			t.Fatalf("Create 后缺少模板角色 %s, got %v", want, templates)
		}
	}
	// 首名 owner 经 seedOwner 同步绑定。
	if got := effectiveBindings(t, sqlDB, created.ID, "u-owner"); !got["tenant_owner"] {
		t.Fatalf("owner 绑定应生效, got %v", got)
	}

	// 2. Add 成员 → 绑定生效。
	if _, err := members.Add(ctx, app.AddMemberInput{
		TenantID: created.ID, SubjectID: "u-sync", Role: domain.MemberRoleAdmin, Actor: itActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got := effectiveBindings(t, sqlDB, created.ID, "u-sync"); !got["tenant_admin"] || got["tenant_owner"] {
		t.Fatalf("Add 后绑定 = %v, want [tenant_admin]", got)
	}

	// 3. SetRole → 旧解绑 + 新生效。
	if _, err := members.SetRole(ctx, app.SetMemberRoleInput{
		TenantID: created.ID, SubjectID: "u-sync", Role: domain.MemberRoleMember, Actor: itActor(),
	}); err != nil {
		t.Fatalf("SetRole() error = %v", err)
	}
	got := effectiveBindings(t, sqlDB, created.ID, "u-sync")
	if got["tenant_admin"] || !got["tenant_member"] {
		t.Fatalf("SetRole 后绑定 = %v, want only [tenant_member]", got)
	}
	// 旧行保留历史（软撤销而非物理删除）。
	var revokedCount int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*) FROM iam_role_binding b JOIN iam_role ro ON ro.id = b.role_id
		 WHERE b.merchant_id = ? AND b.subject_id = 'u-sync' AND b.revoked_at IS NOT NULL AND ro.code = 'tenant_admin'`,
		created.ID).Scan(&revokedCount); err != nil {
		t.Fatalf("query revoked: %v", err)
	}
	if revokedCount != 1 {
		t.Fatalf("旧绑定应保留一条撤销历史, got %d", revokedCount)
	}

	// 4. 停用 → 解绑；复活 → 重绑。
	if _, err := members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: created.ID, SubjectID: "u-sync", Status: domain.MemberStatusDisabled,
		Reason: "集成验证", Actor: itActor(),
	}); err != nil {
		t.Fatalf("SetStatus(disable) error = %v", err)
	}
	if got := effectiveBindings(t, sqlDB, created.ID, "u-sync"); len(got) != 0 {
		t.Fatalf("停用后绑定应全部失效, got %v", got)
	}
	if _, err := members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: created.ID, SubjectID: "u-sync", Status: domain.MemberStatusActive, Actor: itActor(),
	}); err != nil {
		t.Fatalf("SetStatus(enable) error = %v", err)
	}
	if got := effectiveBindings(t, sqlDB, created.ID, "u-sync"); !got["tenant_member"] {
		t.Fatalf("复活后绑定应重新生效, got %v", got)
	}
}
