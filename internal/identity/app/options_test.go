package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// noopObservers 覆盖撤销检查器的观测端口。
type noopObservers struct{}

func (noopObservers) OnAccountStateWriteFailure(context.Context, string, error) {}
func (noopObservers) OnRevocationStoreFailure(context.Context, string, error)   {}
func (noopObservers) OnRevocationCacheHit(context.Context, string)              {}
func (noopObservers) OnRevocationRedisCall(context.Context, string)             {}
func (noopObservers) OnRevocationDBRefetch(context.Context, string)             {}

// allowLimiter 是永远放行的登录限流器。
type allowLimiter struct{}

func (allowLimiter) Allow(string) (bool, time.Duration) { return true, 0 }

// countingFailures 是空实现的跨副本失败计数器。
type countingFailures struct{}

func (countingFailures) Increment(context.Context, string, time.Duration) (int64, error) {
	return 1, nil
}

func (countingFailures) Reset(context.Context, string) error { return nil }

// TestOptionInjection 覆盖观测 / 限流 / 时钟选项注入路径。
func TestOptionInjection(t *testing.T) {
	base := newAuthFixture(t, domain.ChannelAdmin)
	observers := noopObservers{}
	checker, err := app.NewRevocationChecker(
		base.accounts, base.store, "fail_closed", 30*time.Second,
		app.WithRevocationClock(base.clock),
		app.WithRevocationObserver(observers),
		app.WithRevocationCacheObserver(observers),
		app.WithRevocationSessions(base.sessions),
	)
	if err != nil {
		t.Fatalf("NewRevocationChecker() error = %v", err)
	}
	if checker == nil {
		t.Fatal("checker must not be nil")
	}
	guard, err := app.NewLoginGuard(
		base.accounts, persistence.NewMemoryLoginAttemptRepository(), base.ids, app.DefaultLockoutPolicy(),
		app.WithHasher(nil),
		app.WithLoginLimiter(allowLimiter{}),
		app.WithIPRateLimiter(allowLimiter{}),
		app.WithLoginFailureCounter(countingFailures{}),
	)
	if err != nil {
		t.Fatalf("NewLoginGuard() error = %v", err)
	}
	if guard == nil {
		t.Fatal("guard must not be nil")
	}
	tokens, err := app.NewTokenService(
		base.signer, domain.ChannelAdmin, 15*time.Minute, 168*time.Hour, base.ids,
		app.WithTokenClock(base.clock), app.WithTokenParser(base.signer),
	)
	if err != nil {
		t.Fatalf("NewTokenService() error = %v", err)
	}
	if tokens.Channel() != domain.ChannelAdmin {
		t.Fatalf("token channel = %s", tokens.Channel())
	}
	// nil 选项必须保持既有默认（不覆盖已注入能力）。
	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": []byte("01234567890123456789012345678901")})
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	if _, err := app.NewLoginGuard(
		base.accounts, persistence.NewMemoryLoginAttemptRepository(), base.ids, app.DefaultLockoutPolicy(),
		app.WithHasher(hasher),
		app.WithLoginLimiter(nil),
		app.WithIPRateLimiter(nil),
		app.WithLoginFailureCounter(nil),
	); err != nil {
		t.Fatalf("NewLoginGuard(nil options) error = %v", err)
	}
	if _, err := app.NewRevocationChecker(
		base.accounts, base.store, "fail_closed", 30*time.Second,
		app.WithRevocationObserver(nil),
		app.WithRevocationCacheObserver(nil),
	); err != nil {
		t.Fatalf("NewRevocationChecker(nil observers) error = %v", err)
	}
	if got := app.HashClientRef(" "); got != "" {
		t.Fatalf("HashClientRef(blank) = %q, want empty", got)
	}
	if got := app.HashClientRef("1.2.3.4"); len(got) != 64 {
		t.Fatalf("HashClientRef len = %d", len(got))
	}
}
