package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
)

// failingGroups 包装组仓储，按需注入列表 / 计数故障。
type failingGroups struct {
	domain.ScimGroupRepository
	listErr    error
	listAllErr error
	countErr   error
}

// ListByAccount 注入故障时返回错误，否则委托给内存仓储。
func (f *failingGroups) ListByAccount(ctx context.Context, accountID string) ([]*domain.ScimGroup, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.ScimGroupRepository.ListByAccount(ctx, accountID)
}

// List 注入故障时返回错误，否则委托给内存仓储。
func (f *failingGroups) List(
	ctx context.Context,
	filter domain.ScimGroupFilter,
) ([]*domain.ScimGroup, int, error) {
	if f.listAllErr != nil {
		return nil, 0, f.listAllErr
	}
	return f.ScimGroupRepository.List(ctx, filter)
}

// CountMembers 注入故障时返回错误，否则委托给内存仓储。
func (f *failingGroups) CountMembers(ctx context.Context, groupIDs []string) (map[string]int, error) {
	if f.countErr != nil {
		return nil, f.countErr
	}
	return f.ScimGroupRepository.CountMembers(ctx, groupIDs)
}

// stringPtr 返回字符串指针。
func stringPtr(value string) *string { return &value }

// mustCreateScimGroup 创建 SCIM Group，失败即终止用例。
func mustCreateScimGroup(t *testing.T, svc *app.ScimService, in app.ScimGroupInput) *app.ScimGroup {
	t.Helper()
	group, err := svc.CreateGroup(context.Background(), in, "trace-create-group")
	if err != nil {
		t.Fatalf("create scim group: %v", err)
	}
	return group
}

// memberIDs 提取组成员账号 ID 集合。
func memberIDs(t *testing.T, group *app.ScimGroup) map[string]struct{} {
	t.Helper()
	out := make(map[string]struct{}, len(group.Members))
	for _, member := range group.Members {
		out[member.Value] = struct{}{}
	}
	return out
}

// TestScimGetGroupReturnsMembers 验证按主键返回组与成员投影。
func TestScimGetGroupReturnsMembers(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-1", UserName: "alice@corp.example", DisplayName: "Alice",
	})
	created := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	})

	view, err := fixture.svc.GetGroup(ctx, created.ID)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if view.ID != created.ID || view.ExternalID != "grp-1" || view.DisplayName != "ops" {
		t.Fatalf("view = %+v, want grp-1/ops", view)
	}
	if view.MappedRole != "ops_admin" || view.Version < 1 {
		t.Fatalf("view = %+v, want mapped role and version", view)
	}
	if len(view.Members) != 1 || view.Members[0].Value != alice.ID || view.Members[0].Display != alice.UserName {
		t.Fatalf("members = %+v, want alice", view.Members)
	}

	if _, err := fixture.svc.GetGroup(ctx, "  "); err == nil {
		t.Fatal("empty id must be 404")
	} else {
		requireScimError(t, err, 404)
	}
	if _, err := fixture.svc.GetGroup(ctx, "999"); err == nil {
		t.Fatal("unknown id must be 404")
	} else {
		requireScimError(t, err, 404)
	}
}

// TestScimListGroupsPaginationAndFilters 验证组列表分页、eq 过滤与已删除组排除。
func TestScimListGroupsPaginationAndFilters(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	ops := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})
	readonly := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-2", DisplayName: "readonly"})
	unmapped := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-3", DisplayName: "unmapped"})

	first, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{StartIndex: 1, Count: 2})
	if err != nil {
		t.Fatalf("list group first page: %v", err)
	}
	if first.Total != 3 || len(first.Groups) != 2 || first.ItemsPerPage != 2 || first.StartIndex != 1 {
		t.Fatalf("first page = %+v, want total 3 / 2 items", first)
	}

	second, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{StartIndex: 2, Count: 2})
	if err != nil {
		t.Fatalf("list group second page: %v", err)
	}
	if second.Total != 3 || len(second.Groups) != 2 {
		t.Fatalf("second page = %+v, want total 3 / 2 items", second)
	}

	beyond, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{StartIndex: 4, Count: 2})
	if err != nil {
		t.Fatalf("list group beyond page: %v", err)
	}
	if beyond.Total != 3 || len(beyond.Groups) != 0 {
		t.Fatalf("beyond page = %+v, want no items but total 3", beyond)
	}

	// count=0 回落到 pageSize；startIndex 非法值收敛到 1。
	defaulted, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{StartIndex: 0, Count: 0})
	if err != nil {
		t.Fatalf("list group default paging: %v", err)
	}
	if defaulted.Total != 3 || len(defaulted.Groups) != 3 || defaulted.StartIndex != 1 {
		t.Fatalf("default paging = %+v, want all 3 groups", defaulted)
	}

	byName, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{DisplayName: "ops"})
	if err != nil {
		t.Fatalf("list group by displayName: %v", err)
	}
	if byName.Total != 1 || len(byName.Groups) != 1 || byName.Groups[0].ID != ops.ID {
		t.Fatalf("displayName filter = %+v, want ops", byName)
	}
	nameMiss, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{DisplayName: "missing"})
	if err != nil {
		t.Fatalf("list group missing displayName: %v", err)
	}
	if nameMiss.Total != 0 || len(nameMiss.Groups) != 0 {
		t.Fatalf("displayName miss = %+v, want empty", nameMiss)
	}

	byExternal, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ExternalID: "grp-2"})
	if err != nil {
		t.Fatalf("list group by externalId: %v", err)
	}
	if byExternal.Total != 1 || len(byExternal.Groups) != 1 || byExternal.Groups[0].ID != readonly.ID {
		t.Fatalf("externalId filter = %+v, want readonly", byExternal)
	}
	externalMiss, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ExternalID: "grp-missing"})
	if err != nil {
		t.Fatalf("list group missing externalId: %v", err)
	}
	if externalMiss.Total != 0 || len(externalMiss.Groups) != 0 {
		t.Fatalf("externalId miss = %+v, want empty", externalMiss)
	}

	// 主键查询仍需应用剩余过滤条件。
	byID, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ID: ops.ID})
	if err != nil {
		t.Fatalf("list group by id: %v", err)
	}
	if byID.Total != 1 || len(byID.Groups) != 1 || byID.Groups[0].ID != ops.ID {
		t.Fatalf("id lookup = %+v, want ops", byID)
	}
	idNameMismatch, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ID: ops.ID, DisplayName: "readonly"})
	if err != nil {
		t.Fatalf("list group id + displayName mismatch: %v", err)
	}
	if idNameMismatch.Total != 0 || len(idNameMismatch.Groups) != 0 {
		t.Fatalf("id + displayName mismatch = %+v, want empty", idNameMismatch)
	}
	idExternalMismatch, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ID: ops.ID, ExternalID: "grp-2"})
	if err != nil {
		t.Fatalf("list group id + externalId mismatch: %v", err)
	}
	if idExternalMismatch.Total != 0 || len(idExternalMismatch.Groups) != 0 {
		t.Fatalf("id + externalId mismatch = %+v, want empty", idExternalMismatch)
	}
	idMiss, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ID: "999"})
	if err != nil {
		t.Fatalf("list group unknown id: %v", err)
	}
	if idMiss.Total != 0 || len(idMiss.Groups) != 0 {
		t.Fatalf("unknown id = %+v, want empty", idMiss)
	}

	// 已删除组不再出现在列表与主键查询中。
	if err := fixture.svc.DeleteGroup(ctx, unmapped.ID, "trace-delete-group"); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	deletedByID, err := fixture.svc.ListGroups(ctx, app.ScimGroupQuery{ID: unmapped.ID})
	if err != nil {
		t.Fatalf("list deleted group by id: %v", err)
	}
	if deletedByID.Total != 0 || len(deletedByID.Groups) != 0 {
		t.Fatalf("deleted group = %+v, want empty", deletedByID)
	}
}

// TestScimListGroupSummaries 验证组摘要的成员计数与分页。
func TestScimListGroupSummaries(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-1", UserName: "alice@corp.example"})
	mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-2", UserName: "bob@corp.example"})
	ops := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1", "ext-2"},
	})
	readonly := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-2", DisplayName: "readonly",
	})

	summaries, total, err := fixture.svc.ListGroupSummaries(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list group summaries: %v", err)
	}
	if total != 2 || len(summaries) != 2 {
		t.Fatalf("summaries = %+v total = %d, want 2 groups", summaries, total)
	}
	byID := make(map[string]app.ScimGroupSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	if got := byID[ops.ID]; got.MemberCount != 2 || got.MappedRole != "ops_admin" {
		t.Fatalf("ops summary = %+v, want 2 members / ops_admin", got)
	}
	if got := byID[readonly.ID]; got.MemberCount != 0 || got.MappedRole != "auditor" {
		t.Fatalf("readonly summary = %+v, want 0 members / auditor", got)
	}

	paged, pagedTotal, err := fixture.svc.ListGroupSummaries(ctx, 2, 1)
	if err != nil {
		t.Fatalf("list paged group summaries: %v", err)
	}
	if pagedTotal != 2 || len(paged) != 1 {
		t.Fatalf("paged summaries = %+v total = %d, want 1 of 2", paged, pagedTotal)
	}
}

// TestScimPatchGroupMembershipReprojectsRoles 验证增删成员即时投影角色。
func TestScimPatchGroupMembershipReprojectsRoles(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-1", UserName: "alice@corp.example"})
	bob := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-2", UserName: "bob@corp.example"})
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})

	patched, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		AddMembers: []string{" ext-1 ", bob.ID},
	}, nil, "trace-add")
	if err != nil {
		t.Fatalf("patch group add members: %v", err)
	}
	ids := memberIDs(t, patched)
	if len(patched.Members) != 2 {
		t.Fatalf("members = %+v, want 2", patched.Members)
	}
	if _, ok := ids[alice.ID]; !ok {
		t.Fatalf("members = %+v, want alice", patched.Members)
	}
	if _, ok := ids[bob.ID]; !ok {
		t.Fatalf("members = %+v, want bob", patched.Members)
	}
	for _, accountID := range []string{alice.ID, bob.ID} {
		if got := fixture.provisioner.grantedRoles(accountID); len(got) != 1 || got[0] != "ops_admin" {
			t.Fatalf("granted[%s] = %v, want [ops_admin]", accountID, got)
		}
	}

	patched, err = fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		RemoveMembers: []string{alice.ID},
	}, nil, "trace-remove")
	if err != nil {
		t.Fatalf("patch group remove member: %v", err)
	}
	if len(patched.Members) != 1 || patched.Members[0].Value != bob.ID {
		t.Fatalf("members = %+v, want bob only", patched.Members)
	}
	if got := fixture.provisioner.revokedRoles(alice.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked[alice] = %v, want [ops_admin]", got)
	}
}

// TestScimPatchGroupRenameReprojectsRoles 验证改名后角色从旧映射迁移到新映射。
func TestScimPatchGroupRenameReprojectsRoles(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-1", UserName: "alice@corp.example"})
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	})

	patched, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		DisplayName: stringPtr("readonly"),
	}, nil, "trace-rename")
	if err != nil {
		t.Fatalf("rename group: %v", err)
	}
	if patched.DisplayName != "readonly" || patched.MappedRole != "auditor" {
		t.Fatalf("patched = %+v, want readonly/auditor", patched)
	}
	if got := fixture.provisioner.grantedRoles(alice.ID); len(got) != 2 || got[1] != "auditor" {
		t.Fatalf("granted = %v, want [ops_admin auditor]", got)
	}
	if got := fixture.provisioner.revokedRoles(alice.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked = %v, want [ops_admin]", got)
	}

	// 改成未映射名称后旧映射角色被回收。
	patched, err = fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		DisplayName: stringPtr("unmapped"),
	}, nil, "trace-rename-unmapped")
	if err != nil {
		t.Fatalf("rename unmapped group: %v", err)
	}
	if patched.MappedRole != "" {
		t.Fatalf("mapped role = %q, want empty", patched.MappedRole)
	}
	if got := fixture.provisioner.revokedRoles(alice.ID); len(got) != 2 || got[1] != "auditor" {
		t.Fatalf("revoked = %v, want [ops_admin auditor]", got)
	}
}

// TestScimPatchGroupValidation 验证改名 / 外部 ID 校验、If-Match 与 404。
func TestScimPatchGroupValidation(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})
	other := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-2", DisplayName: "readonly"})

	cases := []struct {
		name   string
		id     string
		patch  app.ScimGroupPatch
		status int
	}{
		{name: "duplicate displayName", id: group.ID, patch: app.ScimGroupPatch{
			DisplayName: stringPtr("readonly")}, status: 409},
		{name: "empty displayName", id: group.ID, patch: app.ScimGroupPatch{
			DisplayName: stringPtr("  ")}, status: 400},
		{name: "duplicate externalId", id: group.ID, patch: app.ScimGroupPatch{
			ExternalID: stringPtr(other.ExternalID)}, status: 409},
		{name: "empty externalId", id: group.ID, patch: app.ScimGroupPatch{
			ExternalID: stringPtr(" ")}, status: 400},
		{name: "unknown id", id: "999", patch: app.ScimGroupPatch{
			DisplayName: stringPtr("ops")}, status: 404},
		{name: "empty id", id: "  ", patch: app.ScimGroupPatch{
			DisplayName: stringPtr("ops")}, status: 404},
		{name: "stale if-match", id: group.ID, patch: app.ScimGroupPatch{
			DisplayName: stringPtr("ops")}, status: 412},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ifMatch := int64Ptr(group.Version + 1)
			if tc.status != 412 {
				ifMatch = nil
			}
			if _, err := fixture.svc.PatchGroup(ctx, tc.id, tc.patch, ifMatch, "trace-invalid"); err == nil {
				t.Fatalf("expected %d", tc.status)
			} else {
				requireScimError(t, err, tc.status)
			}
		})
	}

	// 外部 ID 重绑成功。
	rebound, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		ExternalID: stringPtr("grp-9"),
	}, nil, "trace-rebind")
	if err != nil {
		t.Fatalf("rebind group externalId: %v", err)
	}
	if rebound.ExternalID != "grp-9" {
		t.Fatalf("externalId = %q, want grp-9", rebound.ExternalID)
	}

	// 正确 If-Match 允许更新。
	current, err := fixture.svc.GetGroup(ctx, group.ID)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	updated, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		DisplayName: stringPtr("ops-renamed"),
	}, int64Ptr(current.Version), "trace-rename")
	if err != nil {
		t.Fatalf("patch group with if-match: %v", err)
	}
	if updated.DisplayName != "ops-renamed" || updated.Version <= current.Version {
		t.Fatalf("updated = %+v, want renamed with bumped version", updated)
	}
}

// TestScimPatchGroupMemberErrors 验证成员解析失败的 400 分支与空白成员忽略。
func TestScimPatchGroupMemberErrors(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})
	foreign := seedChannelAccount(t, fixture.accounts, domain.ChannelC)
	deleted := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{
		ExternalID: "ext-9", UserName: "gone@corp.example",
	})
	if err := fixture.svc.DeleteUser(ctx, deleted.ID, "trace-delete"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	cases := []struct {
		name    string
		members []string
	}{
		{name: "unknown externalId", members: []string{"ext-ghost"}},
		{name: "non admin channel", members: []string{foreign.ID}},
		{name: "deleted account", members: []string{deleted.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
				AddMembers: tc.members,
			}, nil, "trace-member"); err == nil {
				t.Fatal("expected 400 invalidValue")
			} else {
				requireScimError(t, err, 400)
			}
		})
	}

	// 空白成员被忽略，不产生成员关系。
	patched, err := fixture.svc.PatchGroup(ctx, group.ID, app.ScimGroupPatch{
		AddMembers: []string{"  ", ""},
	}, nil, "trace-blank")
	if err != nil {
		t.Fatalf("patch blank members: %v", err)
	}
	if len(patched.Members) != 0 {
		t.Fatalf("members = %+v, want none", patched.Members)
	}
}

// TestScimCreateGroupValidation 验证建组的必填、唯一性与成员解析分支。
func TestScimCreateGroupValidation(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})

	cases := []struct {
		name   string
		in     app.ScimGroupInput
		status int
	}{
		{name: "missing externalId", in: app.ScimGroupInput{DisplayName: "ops2"}, status: 400},
		{name: "missing displayName", in: app.ScimGroupInput{ExternalID: "grp-2"}, status: 400},
		{name: "duplicate externalId", in: app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops2"}, status: 409},
		{name: "duplicate displayName", in: app.ScimGroupInput{ExternalID: "grp-2", DisplayName: "ops"}, status: 409},
		{name: "unknown member", in: app.ScimGroupInput{
			ExternalID: "grp-3", DisplayName: "ops3", Members: []string{"ext-ghost"},
		}, status: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fixture.svc.CreateGroup(ctx, tc.in, "trace-invalid"); err == nil {
				t.Fatalf("expected %d", tc.status)
			} else {
				requireScimError(t, err, tc.status)
			}
		})
	}
}

// TestScimDeleteGroupIdempotent 验证删除组回收角色并支持重复删除。
func TestScimDeleteGroupIdempotent(t *testing.T) {
	fixture := newScimFixture(t, true)
	ctx := context.Background()
	alice := mustCreateScimUser(t, fixture.svc, app.ScimUserInput{ExternalID: "ext-1", UserName: "alice@corp.example"})
	group := mustCreateScimGroup(t, fixture.svc, app.ScimGroupInput{
		ExternalID: "grp-1", DisplayName: "ops", Members: []string{"ext-1"},
	})

	if err := fixture.svc.DeleteGroup(ctx, group.ID, "trace-delete"); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if got := fixture.provisioner.revokedRoles(alice.ID); len(got) != 1 || got[0] != "ops_admin" {
		t.Fatalf("revoked = %v, want [ops_admin]", got)
	}
	if err := fixture.svc.DeleteGroup(ctx, group.ID, "trace-delete-again"); err != nil {
		t.Fatalf("repeat delete must be idempotent: %v", err)
	}
	if err := fixture.svc.DeleteGroup(ctx, "999", "trace-delete-missing"); err != nil {
		t.Fatalf("delete missing group must be idempotent: %v", err)
	}
	if err := fixture.svc.DeleteGroup(ctx, "  ", "trace-delete-empty"); err != nil {
		t.Fatalf("delete group with empty id must be idempotent: %v", err)
	}
	if _, err := fixture.svc.GetGroup(ctx, group.ID); err == nil {
		t.Fatal("deleted group must be 404")
	} else {
		requireScimError(t, err, 404)
	}
}

// TestScimGroupViewHandlesMissingAndFailingAccounts 验证成员账号缺失时跳过、故障时上抛。
func TestScimGroupViewHandlesMissingAndFailingAccounts(t *testing.T) {
	ctx := context.Background()
	tokens, groups := persistence.NewMemoryScimRepositories()
	accounts := persistence.NewMemoryAccountRepository()
	svc, err := app.NewScimService(app.ScimServiceConfig{
		Accounts: accounts, Identities: persistence.NewMemoryOAuthIdentityRepository(),
		Groups: groups, Tokens: tokens, Sessions: persistence.NewMemorySessionRepository(),
		IDs: &seqIDs{}, Roles: newFakeProvisioner("ops_admin"), Clock: &scimClock{now: scimTestNow},
		Service: "identity-test", Provider: "okta", AutoProvision: true,
		GroupRoleMap: map[string]string{"ops": "ops_admin"}, PageSize: 50,
	})
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	group := mustCreateScimGroup(t, svc, app.ScimGroupInput{ExternalID: "grp-1", DisplayName: "ops"})
	if err := groups.AddMembers(ctx, []*domain.ScimGroupMember{
		{GroupID: group.ID, AccountID: "ghost", CreatedAt: scimTestNow},
	}); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// 成员账号已不存在：视图跳过该成员而不是整体失败。
	view, err := svc.GetGroup(ctx, group.ID)
	if err != nil {
		t.Fatalf("get group with missing member: %v", err)
	}
	if len(view.Members) != 0 {
		t.Fatalf("members = %+v, want none", view.Members)
	}

	// 账号仓储故障必须上抛，避免返回残缺视图。
	storeErr := errors.New("account store down")
	broken, err := app.NewScimService(app.ScimServiceConfig{
		Accounts:   &failingAccounts{AccountRepository: accounts, findErr: storeErr},
		Identities: persistence.NewMemoryOAuthIdentityRepository(),
		Groups:     groups, Tokens: tokens, Sessions: persistence.NewMemorySessionRepository(),
		IDs: &seqIDs{}, Roles: newFakeProvisioner("ops_admin"), Clock: &scimClock{now: scimTestNow},
		Service: "identity-test", Provider: "okta", AutoProvision: true,
		GroupRoleMap: map[string]string{"ops": "ops_admin"}, PageSize: 50,
	})
	if err != nil {
		t.Fatalf("new broken scim service: %v", err)
	}
	if _, err := broken.GetGroup(ctx, group.ID); !errors.Is(err, storeErr) {
		t.Fatalf("group view error = %v, want %v", err, storeErr)
	}
}

// TestScimGroupListRepositoryFailures 验证组列表与摘要依赖故障时上抛错误。
func TestScimGroupListRepositoryFailures(t *testing.T) {
	ctx := context.Background()
	tokens, realGroups := persistence.NewMemoryScimRepositories()
	listErr := errors.New("group store down")

	listSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Tokens = tokens
		cfg.Groups = &failingGroups{ScimGroupRepository: realGroups, listAllErr: listErr}
	})
	if _, err := listSvc.ListGroups(ctx, app.ScimGroupQuery{}); !errors.Is(err, listErr) {
		t.Fatalf("list groups error = %v, want %v", err, listErr)
	}
	if _, _, err := listSvc.ListGroupSummaries(ctx, 1, 10); !errors.Is(err, listErr) {
		t.Fatalf("list summaries error = %v, want %v", err, listErr)
	}

	// 组列表可读但成员计数失败时摘要查询失败关闭。
	countErr := errors.New("group count down")
	countSvc := newScimTestService(t, func(cfg *app.ScimServiceConfig) {
		cfg.Tokens = tokens
		cfg.Groups = &failingGroups{ScimGroupRepository: realGroups, countErr: countErr}
	})
	if _, _, err := countSvc.ListGroupSummaries(ctx, 1, 10); !errors.Is(err, countErr) {
		t.Fatalf("list summaries count error = %v, want %v", err, countErr)
	}
}
