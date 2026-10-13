package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestNewOrderAndCancel(t *testing.T) {
	o, err := NewOrder("ord_1", "m1", "u1", 100, "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusCreated {
		t.Fatalf("status=%s", o.Status)
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusCancelled {
		t.Fatalf("status=%s", o.Status)
	}
}

func TestNewOrderValidation(t *testing.T) {
	cases := []struct {
		name       string
		merchantID string
		userID     string
		amount     int64
	}{
		{name: "empty merchant", userID: "u1", amount: 1},
		{name: "empty user", merchantID: "m1", amount: 1},
		{name: "non-positive amount", merchantID: "m1", userID: "u1", amount: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOrder("ord_x", tc.merchantID, tc.userID, tc.amount, "")
			if err == nil {
				t.Fatal("expected error")
			}
			var ae *apperr.AppError
			if !errors.As(err, &ae) || ae.Code != apperr.CodeBadRequest {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestNewOrderDefaultCurrency(t *testing.T) {
	o, err := NewOrder("ord_1", "m1", "u1", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if o.Currency != "CNY" {
		t.Fatalf("currency=%s", o.Currency)
	}
}

func TestCancelPaidOrder(t *testing.T) {
	o, err := NewOrder("ord_1", "m1", "u1", 100, "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.MarkPaid(); err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); err == nil {
		t.Fatal("expected error")
	}
}

func TestCancelAlreadyCancelled(t *testing.T) {
	o, err := NewOrder("ord_1", "m1", "u1", 100, "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestMarkPaidOnlyFromCreated(t *testing.T) {
	o, err := NewOrder("ord_1", "m1", "u1", 100, "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := o.MarkPaid(); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestDomainEvents(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	created := OrderCreated{OrderID: "o1", MerchantID: "m1", UserID: "u1", At: at}
	if created.EventName() != "order.created" {
		t.Fatalf("name=%s", created.EventName())
	}
	if !created.OccurredAt().Equal(at) {
		t.Fatalf("at=%v", created.OccurredAt())
	}
	cancelled := OrderCancelled{OrderID: "o1", MerchantID: "m1", UserID: "u1", At: at}
	if cancelled.EventName() != "order.cancelled" {
		t.Fatalf("name=%s", cancelled.EventName())
	}
	if !cancelled.OccurredAt().Equal(at) {
		t.Fatalf("at=%v", cancelled.OccurredAt())
	}
}
