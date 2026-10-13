package domain

import "testing"

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
