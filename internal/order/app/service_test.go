package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestMerchantIsolation(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryRepository()
	svc := app.NewOrderService(repo, nil)

	o, err := svc.CreateOrder(ctx, app.CreateOrderCommand{
		MerchantID: "m1",
		UserID:     "u1",
		AmountCent: 199,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.GetOrderForMerchant(ctx, o.ID, "m2"); err == nil {
		t.Fatal("expected not found for other merchant")
	}
	got, err := svc.GetOrderForMerchant(ctx, o.ID, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != o.ID {
		t.Fatalf("id=%s", got.ID)
	}
}

func TestUserOwnership(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryRepository()
	svc := app.NewOrderService(repo, nil)

	o, err := svc.CreateOrder(ctx, app.CreateOrderCommand{
		MerchantID: "m1",
		UserID:     "u1",
		AmountCent: 199,
		IdemKey:    "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateOrder(ctx, app.CreateOrderCommand{
		MerchantID: "m1",
		UserID:     "u1",
		AmountCent: 199,
		IdemKey:    "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != o.ID {
		t.Fatalf("idempotency broken: %s vs %s", again.ID, o.ID)
	}
	if _, err := svc.GetOrderForUser(ctx, o.ID, "u2"); err == nil {
		t.Fatal("expected not found for other user")
	}
}

func TestListOrdersPageSizeCap(t *testing.T) {
	ctx := context.Background()
	svc := app.NewOrderService(persistence.NewMemoryRepository(), nil)
	_, err := svc.ListOrders(ctx, domain.ListFilter{Limit: 101})
	if err == nil {
		t.Fatal("expected page_size error")
	}
	var ae *apperr.AppError
	if !errors.As(err, &ae) || ae.Code != apperr.CodeBadRequest {
		t.Fatalf("err=%v", err)
	}
}
