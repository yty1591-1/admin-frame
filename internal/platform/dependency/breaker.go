package dependency

import (
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
)

// newBreaker 按 Spec 构造熔断器；未登记熔断参数时返回 nil。
func newBreaker(spec Spec) (*circuitbreaker.Breaker, error) {
	if spec.Breaker == nil {
		return nil, nil
	}
	breaker, err := circuitbreaker.New(circuitbreaker.Config{
		Name:    spec.Name,
		Timeout: orDefault(spec.Breaker.OpenTimeout, 30*time.Second),
		ReadyToTrip: func(counts circuitbreaker.Counts) bool {
			threshold := int64(spec.Breaker.ConsecutiveFailures)
			if threshold <= 0 {
				threshold = 5
			}
			return int64(counts.ConsecutiveFailures) >= threshold
		},
		IsSuccessful: spec.Breaker.IsSuccessful,
	})
	if err != nil {
		return nil, fmt.Errorf("dependency: build breaker for %q: %w", spec.Name, err)
	}
	return breaker, nil
}

func orDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
