package rest

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// denyingLimiter 始终拒绝并返回建议等待时长。
type denyingLimiter struct {
	retry time.Duration
	calls int
}

func (l *denyingLimiter) Allow(string) (bool, time.Duration) {
	l.calls++
	return false, l.retry
}

// TestCheckEndpointLimitRejectsRefreshAndLogout 验证 refresh / logout 端点限流（SEC-04）。
func TestCheckEndpointLimitRejectsRefreshAndLogout(t *testing.T) {
	limiter := &denyingLimiter{retry: 2 * time.Second}
	h := &Handler{channel: domain.ChannelAdmin, limiter: limiter}
	request := httptest.NewRequest("POST", "/admin/v1/auth/refresh", nil)

	retryAfter, err := h.checkEndpointLimit(request, "refresh", "refresh-token-value")
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeTooManyRequests {
		t.Fatalf("err = %v, want 42900", err)
	}
	if retryAfter != 2*time.Second {
		t.Fatalf("retryAfter = %v, want 2s", retryAfter)
	}
	if limiter.calls == 0 {
		t.Fatal("limiter must be consulted")
	}

	// 未注入限流器时直接放行。
	plain := &Handler{channel: domain.ChannelAdmin}
	if retry, err := plain.checkEndpointLimit(request, "logout", "acct-1"); err != nil || retry != 0 {
		t.Fatalf("nil limiter must pass through, retry=%v err=%v", retry, err)
	}
}

// TestSetRetryAfterRoundsUp 验证 Retry-After 以秒向上取整。
func TestSetRetryAfterRoundsUp(t *testing.T) {
	recorder := httptest.NewRecorder()
	setRetryAfter(recorder, 1500*time.Millisecond)
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
}
