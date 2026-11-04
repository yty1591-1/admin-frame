package dependency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// recordObserver 记录观测信号（测试断言用）。
type recordObserver struct {
	calls []string
}

func (o *recordObserver) CallObserved(name, result string) {
	o.calls = append(o.calls, name+"="+result)
}

func (o *recordObserver) RetryObserved(name string) {
	o.calls = append(o.calls, name+"=retry")
}

// TestRegisterDuplicate 防重复登记（指标标签基数守卫）。
func TestRegisterDuplicate(t *testing.T) {
	registry := dependency.NewRegistry(nil)
	if _, err := registry.Register(dependency.Spec{Name: "oauth:github"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := registry.Register(dependency.Spec{Name: "oauth:github"}); err == nil {
		t.Fatal("duplicate register must fail")
	}
	if _, err := registry.Register(dependency.Spec{Name: "  "}); err == nil {
		t.Fatal("empty name must fail")
	}
	if names := registry.Names(); len(names) != 1 {
		t.Fatalf("names = %v", names)
	}
}

// TestGetUnknownDependency 未登记依赖快速失败（装配缺陷即暴露）。
func TestGetUnknownDependency(t *testing.T) {
	registry := dependency.NewRegistry(nil)
	if _, err := registry.Get("ghost"); !errors.Is(err, dependency.ErrUnknownDependency) {
		t.Fatalf("want ErrUnknownDependency, got %v", err)
	}
}

// TestDoRecordsOutcome 调用结果三分类观测：ok / error / breaker_open。
func TestDoRecordsOutcome(t *testing.T) {
	obs := &recordObserver{}
	registry := dependency.NewRegistry(obs)
	caller, err := registry.Register(dependency.Spec{
		Name:    "downstream",
		Timeout: time.Second,
		Breaker: &dependency.BreakerSpec{ConsecutiveFailures: 2, OpenTimeout: time.Minute},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx := context.Background()

	// ok。
	if err := caller.Do(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("ok call: %v", err)
	}
	// error（连续两次触发熔断打开）。
	for i := 0; i < 2; i++ {
		_ = caller.Do(ctx, func(context.Context) error { return errors.New("boom") })
	}
	// breaker_open（第三次被熔断拦截）。
	err = caller.Do(ctx, func(context.Context) error { return nil })
	if !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("third call must be breaker-open, got %v", err)
	}
	want := []string{"downstream=ok", "downstream=error", "downstream=error", "downstream=breaker_open"}
	if len(obs.calls) != len(want) {
		t.Fatalf("observations = %v, want %v", obs.calls, want)
	}
	for i := range want {
		if obs.calls[i] != want[i] {
			t.Fatalf("observation[%d] = %s, want %s", i, obs.calls[i], want[i])
		}
	}
}

// TestDoBusinessErrorsNotTripping 业务拒绝类错误不触发熔断（IsSuccessful 定制）。
func TestDoBusinessErrorsNotTripping(t *testing.T) {
	registry := dependency.NewRegistry(nil)
	caller, err := registry.Register(dependency.Spec{
		Name: "provider",
		Breaker: &dependency.BreakerSpec{
			ConsecutiveFailures: 2,
			OpenTimeout:         time.Minute,
			IsSuccessful: func(err error) bool {
				// 业务拒绝（如 invalid_grant）视为成功——不该打开熔断。
				return err == nil || errors.Is(err, errBusiness)
			},
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := caller.Do(ctx, func(context.Context) error { return errBusiness }); err != nil {
			if errors.Is(err, circuitbreaker.ErrOpen) {
				t.Fatalf("business error must not open breaker (call %d)", i)
			}
		}
	}
}

// errBusiness 是测试用业务拒绝哨兵。
var errBusiness = errors.New("business rejection")

// TestRetryUntilPermanent 可重试错误按策略重试，Permanent 错误立即终止。
//
// 重试语义对齐 REL-03：只有显式声明 Retryable 的错误（pkg/errors）才触发
// 重试——普通错误保守视为不可重试（注册表不猜语义）。
func TestRetryUntilPermanent(t *testing.T) {
	obs := &recordObserver{}
	registry := dependency.NewRegistry(obs)
	caller, err := registry.Register(dependency.Spec{
		Name: "retryable",
		Retry: retry.Policy{
			MaxAttempts:    3,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     2 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx := context.Background()

	// 可重试错误（声明 Retryable）：两次失败后成功。
	transient := pkgerrors.New("transient downstream failure").WithRetryable(true)
	calls := 0
	if err := caller.Do(ctx, func(context.Context) error {
		calls++
		if calls < 3 {
			return transient
		}
		return nil
	}); err != nil {
		t.Fatalf("retry-until-success: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}

	// 普通错误（未声明 Retryable）：不重试（保守语义）。
	plainCalls := 0
	_ = caller.Do(ctx, func(context.Context) error {
		plainCalls++
		return errors.New("plain failure")
	})
	if plainCalls != 1 {
		t.Fatalf("plain error must not retry: calls=%d", plainCalls)
	}

	// Permanent 包装：即使可重试声明也立即终止（非幂等调用显式终止）。
	permCalls := 0
	err = caller.Do(ctx, func(context.Context) error {
		permCalls++
		return retry.Permanent(transient)
	})
	if err == nil || permCalls != 1 {
		t.Fatalf("permanent error must not retry: calls=%d err=%v", permCalls, err)
	}
}

// TestTimeoutEnforced 单次尝试超时强制（快速失败）。
func TestTimeoutEnforced(t *testing.T) {
	registry := dependency.NewRegistry(nil)
	caller, err := registry.Register(dependency.Spec{
		Name:    "slow",
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	start := time.Now()
	err = caller.Do(context.Background(), func(callCtx context.Context) error {
		<-callCtx.Done()
		return callCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout not enforced promptly: %v", elapsed)
	}
}
