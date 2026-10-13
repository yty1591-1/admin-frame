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

// TestMerchantIsolation 覆盖 B 端商户隔离（TEST-03）。
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

	_, err = svc.GetOrderForMerchant(ctx, o.ID, "m2")
	assertNotFound(t, err)

	got, err := svc.GetOrderForMerchant(ctx, o.ID, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != o.ID {
		t.Fatalf("id=%s", got.ID)
	}

	_, err = svc.CancelOrder(ctx, app.CancelOrderCommand{OrderID: o.ID, MerchantID: "m2"})
	assertNotFound(t, err)
}

// TestUserOwnership 覆盖 C 端越权表现为不存在（TEST-03）。
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

	_, err = svc.GetOrderForUser(ctx, o.ID, "u2")
	assertNotFound(t, err)

	_, err = svc.CancelOrder(ctx, app.CancelOrderCommand{OrderID: o.ID, UserID: "u2"})
	assertNotFound(t, err)
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

func assertNotFound(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected not found")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	var ae *apperr.AppError
	if errors.As(err, &ae) && ae.Code != apperr.CodeNotFound {
		t.Fatalf("code=%d", ae.Code)
	}
}
