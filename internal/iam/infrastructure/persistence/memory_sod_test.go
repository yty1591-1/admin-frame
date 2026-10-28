package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func newTestSodGroup(t *testing.T, id, code string, mutate func(*domain.NewSodGroupParams)) *domain.SodGroup {
	t.Helper()
	params := domain.NewSodGroupParams{
		ID:      id,
		Channel: domain.ChannelAdmin,
		Code:    code,
		Name:    "测试互斥组 " + code,
		RoleIDs: []string{"role-1", "role-2"},
		Actor:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	group, err := domain.NewSodGroup(params)
	if err != nil {
		t.Fatalf("NewSodGroup(%s): %v", code, err)
	}
	return group
}

func TestMemorySodGroupRepositoryCreateAndFind(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySodGroupRepository()
	group := newTestSodGroup(t, "group-1", "finance_dual", nil)

	if err := repo.Save(ctx, group, 0); err != nil {
		t.Fatal(err)
	}

	byID, err := repo.FindByID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.Code != group.Code || len(byID.RoleIDs) != 2 {
		t.Fatalf("group=%+v", byID)
	}

	byCode, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", group.Code)
	if err != nil {
		t.Fatal(err)
	}
	if byCode.ID != group.ID {
		t.Fatalf("id=%s", byCode.ID)
	}

	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	if _, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestMemorySodGroupRepositoryUniquenessAndOptimisticLock(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySodGroupRepository()
	group := newTestSodGroup(t, "group-1", "finance_dual", nil)
	if err := repo.Save(ctx, group, 0); err != nil {
		t.Fatal(err)
	}

	if err := repo.Save(ctx, newTestSodGroup(t, "group-1", "other_code", nil), 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate id err=%v, want conflict", err)
	}
	if err := repo.Save(ctx, newTestSodGroup(t, "group-2", "finance_dual", nil), 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate code err=%v, want conflict", err)
	}

	if err := group.UpdateDetails("新名称", "新说明", 1, "actor-2", group.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, group, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := repo.Save(ctx, group, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale update err=%v, want conflict", err)
	}

	// code / channel / merchant 不可变。
	immutable := *group
	immutable.Code = "changed_code"
	if err := repo.Save(ctx, &immutable, group.Version); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("immutable err=%v, want bad request", err)
	}
}

func TestMemorySodGroupRepositoryListAndCount(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySodGroupRepository()
	admin := newTestSodGroup(t, "group-1", "finance_dual", nil)
	biz := newTestSodGroup(t, "group-2", "finance_dual", func(p *domain.NewSodGroupParams) {
		p.Channel = domain.ChannelBiz
		p.MerchantID = "m-1"
		p.RoleIDs = []string{"role-7", "role-8"}
	})
	disabled := newTestSodGroup(t, "group-3", "audit_conflict", func(p *domain.NewSodGroupParams) {
		p.RoleIDs = []string{"role-9", "role-10"}
	})
	if err := disabled.Disable("actor-1", disabled.Version); err != nil {
		t.Fatal(err)
	}
	for _, group := range []*domain.SodGroup{admin, biz, disabled} {
		if err := repo.Save(ctx, group, 0); err != nil {
			t.Fatalf("save %s: %v", group.ID, err)
		}
	}

	cases := []struct {
		name   string
		filter domain.SodGroupFilter
		want   int
	}{
		{name: "all", filter: domain.SodGroupFilter{}, want: 3},
		{name: "channel biz", filter: domain.SodGroupFilter{Channel: domain.ChannelBiz}, want: 1},
		{name: "merchant", filter: domain.SodGroupFilter{MerchantID: "m-1"}, want: 1},
		{name: "status disabled", filter: domain.SodGroupFilter{Status: domain.SodGroupDisabled}, want: 1},
		{name: "keyword", filter: domain.SodGroupFilter{Keyword: "AUDIT"}, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groups, err := repo.List(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			total, err := repo.Count(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(groups) != tc.want || total != tc.want {
				t.Fatalf("list=%d count=%d, want %d", len(groups), total, tc.want)
			}
		})
	}
}

func TestMemorySodGroupRepositoryListByRole(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySodGroupRepository()
	admin := newTestSodGroup(t, "group-1", "finance_dual", nil)
	biz := newTestSodGroup(t, "group-2", "merchant_dual", func(p *domain.NewSodGroupParams) {
		p.Channel = domain.ChannelBiz
		p.MerchantID = "m-1"
		p.RoleIDs = []string{"role-9", "role-10"}
	})
	for _, group := range []*domain.SodGroup{admin, biz} {
		if err := repo.Save(ctx, group, 0); err != nil {
			t.Fatal(err)
		}
	}

	groups, err := repo.ListByRole(ctx, domain.ChannelAdmin, "", "role-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ID != admin.ID {
		t.Fatalf("groups=%v", groups)
	}

	// 跨租户查询不得命中。
	groups, err = repo.ListByRole(ctx, domain.ChannelAdmin, "", "role-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("cross tenant groups=%v", groups)
	}
}

func TestMemorySodGroupRepositorySoftDelete(t *testing.T) {
	ctx := context.Background()
	repo := NewMemorySodGroupRepository()
	group := newTestSodGroup(t, "group-1", "finance_dual", nil)
	if err := repo.Save(ctx, group, 0); err != nil {
		t.Fatal(err)
	}
	if err := group.Delete("actor-1", group.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, group, 1); err != nil {
		t.Fatal(err)
	}

	groups, err := repo.List(ctx, domain.SodGroupFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("deleted group must not appear in list: %v", groups)
	}
	// 软删后 FindByCode 仍返回，用于阻止 code 复用。
	if _, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", group.Code); err != nil {
		t.Fatalf("soft-deleted group must remain addressable by code: %v", err)
	}
}
