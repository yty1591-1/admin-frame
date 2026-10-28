package domain

import (
	"errors"
	"fmt"
	"testing"
)

// mustNewSodGroup 构造一个合法的 A 端互斥组，可用 mutate 覆盖字段。
func mustNewSodGroup(t *testing.T, mutate func(*NewSodGroupParams)) *SodGroup {
	t.Helper()
	params := NewSodGroupParams{
		ID:      "group-1",
		Channel: ChannelAdmin,
		Code:    "finance_dual_control",
		Name:    "财务双人复核",
		RoleIDs: []string{"role-1", "role-2"},
		Actor:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	group, err := NewSodGroup(params)
	if err != nil {
		t.Fatalf("NewSodGroup: %v", err)
	}
	return group
}

func TestNewSodGroupDefaults(t *testing.T) {
	group := mustNewSodGroup(t, nil)
	if group.Status != SodGroupActive || !group.IsActive() {
		t.Fatalf("status=%s", group.Status)
	}
	if group.Version != 1 {
		t.Fatalf("version=%d", group.Version)
	}
	if group.MaxActiveRoles != 1 {
		t.Fatalf("max_active_roles=%d, want default 1", group.MaxActiveRoles)
	}
	if len(group.RoleIDs) != 2 || group.RoleIDs[0] != "role-1" {
		t.Fatalf("roles=%v", group.RoleIDs)
	}
	if group.CreatedBy != "actor-1" || group.CreatedAt.IsZero() || group.UpdatedAt.IsZero() {
		t.Fatalf("audit fields=%+v", group)
	}
	if group.IsDeleted() {
		t.Fatal("new sod group must not be deleted")
	}
}

func TestNewSodGroupValidation(t *testing.T) {
	tooManyRoles := make([]string, 0, MaxSodGroupRoles+1)
	for index := 0; index <= MaxSodGroupRoles; index++ {
		tooManyRoles = append(tooManyRoles, fmt.Sprintf("role-%03d", index))
	}

	cases := []struct {
		name   string
		mutate func(*NewSodGroupParams)
	}{
		{name: "admin with merchant", mutate: func(p *NewSodGroupParams) { p.MerchantID = "m-1" }},
		{name: "biz without merchant", mutate: func(p *NewSodGroupParams) {
			p.Channel = ChannelBiz
		}},
		{name: "single role", mutate: func(p *NewSodGroupParams) { p.RoleIDs = []string{"role-1"} }},
		{name: "duplicate role", mutate: func(p *NewSodGroupParams) {
			p.RoleIDs = []string{"role-1", "role-1"}
		}},
		{name: "too many roles", mutate: func(p *NewSodGroupParams) { p.RoleIDs = tooManyRoles }},
		{name: "invalid code", mutate: func(p *NewSodGroupParams) { p.Code = "Bad-Code" }},
		{name: "empty name", mutate: func(p *NewSodGroupParams) { p.Name = " " }},
		{name: "max active equals role count", mutate: func(p *NewSodGroupParams) { p.MaxActiveRoles = 2 }},
		{name: "max active out of range", mutate: func(p *NewSodGroupParams) { p.MaxActiveRoles = MaxSodGroupRoles + 1 }},
		{name: "missing actor", mutate: func(p *NewSodGroupParams) { p.Actor = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := NewSodGroupParams{
				ID: "group-1", Channel: ChannelAdmin, Code: "finance_dual_control",
				Name: "财务双人复核", RoleIDs: []string{"role-1", "role-2"}, Actor: "actor-1",
			}
			tc.mutate(&params)
			_, err := NewSodGroup(params)
			if !errors.Is(err, ErrInvalidSodGroup) && !errors.Is(err, ErrMissingActor) {
				t.Fatalf("err = %v, want invalid sod group", err)
			}
		})
	}
}

func TestSodGroupMutations(t *testing.T) {
	group := mustNewSodGroup(t, nil)

	if err := group.UpdateDetails("新名称", "新说明", 1, "actor-2", group.Version); err != nil {
		t.Fatalf("UpdateDetails: %v", err)
	}
	if group.Version != 2 || group.Name != "新名称" {
		t.Fatalf("version=%d name=%q", group.Version, group.Name)
	}

	if err := group.ReplaceRoles([]string{"role-1", "role-2", "role-3"}, "actor-2", group.Version); err != nil {
		t.Fatalf("ReplaceRoles: %v", err)
	}
	if group.Version != 3 || len(group.RoleIDs) != 3 {
		t.Fatalf("version=%d roles=%v", group.Version, group.RoleIDs)
	}

	if err := group.UpdateDetails("新名称", "新说明", 2, "actor-2", group.Version); err != nil {
		t.Fatalf("UpdateDetails max active: %v", err)
	}
	if group.MaxActiveRoles != 2 {
		t.Fatalf("max_active_roles=%d", group.MaxActiveRoles)
	}

	// 成员数降到 2 后，阈值 2 不再产生约束，必须拒绝。
	if err := group.ReplaceRoles([]string{"role-1", "role-2"}, "actor-2", group.Version); !errors.Is(err, ErrInvalidSodGroup) {
		t.Fatalf("err = %v, want invalid sod group", err)
	}

	// 过期版本必须冲突。
	if err := group.ReplaceRoles([]string{"role-1", "role-2"}, "actor-2", 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version err = %v, want conflict", err)
	}

	if err := group.Disable("actor-2", group.Version); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	disabledVersion := group.Version
	// 幂等：重复停用不递增版本。
	if err := group.Disable("actor-2", disabledVersion); err != nil || group.Version != disabledVersion {
		t.Fatalf("idempotent disable err=%v version=%d", err, group.Version)
	}
	if err := group.Enable("actor-2", group.Version); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	enabledVersion := group.Version
	if err := group.Enable("actor-2", enabledVersion); err != nil || group.Version != enabledVersion {
		t.Fatalf("idempotent enable err=%v version=%d", err, group.Version)
	}

	if err := group.Delete("actor-2", group.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !group.IsDeleted() || group.Status != SodGroupDisabled {
		t.Fatalf("deleted=%v status=%s", group.IsDeleted(), group.Status)
	}
	if err := group.Delete("actor-2", group.Version); !errors.Is(err, ErrInvalidSodGroup) {
		t.Fatalf("second delete err = %v, want conflict", err)
	}
}

func TestSodGroupSortedRoleIDs(t *testing.T) {
	group := mustNewSodGroup(t, func(p *NewSodGroupParams) {
		p.RoleIDs = []string{"role-3", "role-1", "role-2"}
	})
	sorted := group.SortedRoleIDs()
	if sorted[0] != "role-1" || sorted[1] != "role-2" || sorted[2] != "role-3" {
		t.Fatalf("sorted roles = %v", sorted)
	}
	// 返回值是副本：修改不影响聚合。
	sorted[0] = "mutated"
	if group.RoleIDs[0] == "mutated" {
		t.Fatal("SortedRoleIDs must return a copy")
	}
}
