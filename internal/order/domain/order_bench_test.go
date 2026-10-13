package domain_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/order/domain"
)

func BenchmarkNewOrder(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, err := domain.NewOrder("id", "m1", "u1", 100, "CNY")
		if err != nil {
			b.Fatal(err)
		}
	}
}
