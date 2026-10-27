package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func newTestChangeRequest(t *testing.T, id string, mutate func(*domain.NewChangeRequestParams)) *domain.ChangeRequest {
	t.Helper()
	params := domain.NewChangeRequestParams{
		ID:       id,
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRolePermissionReplace,
		TargetID: "role-1",
		Payload:  []byte(`{"permissions":["iam:role:read"]}`),
		Reason:   "授予只读权限",
		Actor:    "requester-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	request, err := domain.NewChangeRequest(params)
	if err != nil {
		t.Fatalf("NewChangeRequest(%s): %v", id, err)
	}
	return request
}

func TestMemoryChangeRequestRepositorySaveAndFind(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()
	request := newTestChangeRequest(t, "change-1", nil)

	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.FindByID(ctx, "change-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Action != domain.ChangeActionRolePermissionReplace ||
		loaded.TargetType != domain.ChangeTargetIAMRole {
		t.Fatalf("loaded=%+v", loaded)
	}
	if string(loaded.Payload) != `{"permissions":["iam:role:read"]}` {
		t.Fatalf("payload=%s", loaded.Payload)
	}

	if err := repo.Save(ctx, request, 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate save err=%v, want conflict", err)
	}
}

func TestMemoryChangeRequestRepositoryOptimisticLock(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()
	request := newTestChangeRequest(t, "change-1", nil)
	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stale := *request
	stale.Status = domain.ChangeStatusApproved
	stale.Version = 2
	if err := repo.Save(ctx, &stale, 0); err == nil {
		t.Fatal("expected create with existing id to fail")
	}
	if err := repo.Save(ctx, &stale, 1); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	third := stale
	third.Status = domain.ChangeStatusApplied
	third.Version = 3
	if err := repo.Save(ctx, &third, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale save err=%v, want conflict", err)
	}

	loaded, err := repo.FindByID(ctx, "change-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Status != domain.ChangeStatusApproved {
		t.Fatalf("status=%s", loaded.Status)
	}
}

func TestMemoryChangeRequestRepositoryRejectsImmutableFields(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()
	request := newTestChangeRequest(t, "change-1", nil)
	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	mutated := *request
	mutated.Action = domain.ChangeActionRoleDelete
	mutated.TargetType = domain.ChangeTargetIAMRole
	if err := repo.Save(ctx, &mutated, 1); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}
}

func TestMemoryChangeRequestRepositoryListFiltersAndOrder(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()

	first := newTestChangeRequest(t, "change-1", nil)
	first.RequestedAt = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	second := newTestChangeRequest(t, "change-2", func(p *domain.NewChangeRequestParams) {
		p.Actor = "requester-2"
		p.Action = domain.ChangeActionBindingCreate
		p.TargetID = ""
	})
	second.RequestedAt = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	approved := newTestChangeRequest(t, "change-3", nil)
	approved.RequestedAt = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	if err := approved.Approve("approver-1", "", 1); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	for _, request := range []*domain.ChangeRequest{first, second, approved} {
		if err := repo.Save(ctx, request, 0); err != nil {
			t.Fatalf("Save(%s): %v", request.ID, err)
		}
	}

	all, err := repo.List(ctx, domain.ChangeRequestFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 || all[0].ID != "change-2" || all[2].ID != "change-3" {
		t.Fatalf("order=%v", idsOf(all))
	}

	pending, err := repo.List(ctx, domain.ChangeRequestFilter{Status: domain.ChangeStatusPending})
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending=%v", idsOf(pending))
	}

	byRequester, err := repo.List(ctx, domain.ChangeRequestFilter{RequestedBy: "requester-2"})
	if err != nil {
		t.Fatalf("List requester: %v", err)
	}
	if len(byRequester) != 1 || byRequester[0].ID != "change-2" {
		t.Fatalf("by requester=%v", idsOf(byRequester))
	}

	paged, err := repo.List(ctx, domain.ChangeRequestFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("List paged: %v", err)
	}
	if len(paged) != 1 || paged[0].ID != "change-1" {
		t.Fatalf("paged=%v", idsOf(paged))
	}
}

func TestMemoryChangeRequestRepositoryReturnsDeepCopies(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()
	request := newTestChangeRequest(t, "change-1", nil)
	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	request.Payload[0] = '['
	request.Status = domain.ChangeStatusRejected

	loaded, err := repo.FindByID(ctx, "change-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Status != domain.ChangeStatusPending || string(loaded.Payload)[0] != '{' {
		t.Fatalf("stored state was mutated: status=%s payload=%s", loaded.Status, loaded.Payload)
	}

	loaded.Payload[0] = ' '
	again, err := repo.FindByID(ctx, "change-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if string(again.Payload)[0] != '{' {
		t.Fatalf("read leaked internal payload: %s", again.Payload)
	}
}

func TestMemoryChangeRequestRepositoryNotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()
	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	request := newTestChangeRequest(t, "change-1", nil)
	if err := repo.Save(ctx, request, 1); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

// TestMemoryChangeRequestRepositoryTenantScope 覆盖商户维度的写入、过滤与不可变。
func TestMemoryChangeRequestRepositoryTenantScope(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryChangeRequestRepository()

	tenant := newTestChangeRequest(t, "change-biz", func(p *domain.NewChangeRequestParams) {
		p.Channel = domain.ChannelBiz
		p.MerchantID = "m-1"
	})
	if err := repo.Save(ctx, tenant, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.FindByID(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.MerchantID != "m-1" {
		t.Fatalf("merchant=%q, want m-1", loaded.MerchantID)
	}

	own, err := repo.List(ctx, domain.ChangeRequestFilter{Channel: domain.ChannelBiz, MerchantID: "m-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(own) != 1 || own[0].ID != tenant.ID {
		t.Fatalf("own=%v", idsOf(own))
	}
	other, err := repo.List(ctx, domain.ChangeRequestFilter{Channel: domain.ChannelBiz, MerchantID: "m-2"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("cross tenant leaked %v", idsOf(other))
	}
	if total, err := repo.Count(ctx, domain.ChangeRequestFilter{MerchantID: "m-2"}); err != nil || total != 0 {
		t.Fatalf("count=%d err=%v", total, err)
	}

	// 商户是审批单的租户归属事实，更新时不得改写。
	mutated := *tenant
	mutated.MerchantID = "m-2"
	if err := repo.Save(ctx, &mutated, 1); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}
}

func idsOf(requests []*domain.ChangeRequest) []string {
	ids := make([]string, len(requests))
	for i, request := range requests {
		ids[i] = request.ID
	}
	return ids
}
