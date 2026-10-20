package circuitbreaker

import stderrors "errors"

var (
	// ErrOpen 表示熔断器处于 Open，请求被立即拒绝。
	ErrOpen = stderrors.New("circuitbreaker: circuit is open")
	// ErrTooManyRequests 表示 HalfOpen 探测配额已满。
	ErrTooManyRequests = stderrors.New("circuitbreaker: too many requests in half-open state")
	// ErrInvalidConfig 表示配置含非法的负值。
	ErrInvalidConfig = stderrors.New("circuitbreaker: invalid config")
	// ErrNilFunc 表示执行函数为 nil。
	ErrNilFunc = stderrors.New("circuitbreaker: nil function")
	// ErrNilBreaker 表示在 nil *Breaker 上调用执行方法。
	ErrNilBreaker = stderrors.New("circuitbreaker: nil breaker")
)
