package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

func TestDirectSenderSendsSync(t *testing.T) {
	sender := &fakeSender{provider: "aliyun"}
	observer := &captureObserver{}
	svc, err := NewDirectSender(map[domain.Channel][]Sender{domain.ChannelSms: {sender}}, observer)
	if err != nil {
		t.Fatalf("NewDirectSender: %v", err)
	}
	result, err := svc.Send(context.Background(), DirectRequest{
		Channel: domain.ChannelSms, Address: "+8613800138000",
		Rendered: domain.Rendered{Body: "code 123456"}, Reference: "auth_code",
		RequestID: "auth-1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Provider != "aliyun" || result.MessageID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if sender.calls != 1 {
		t.Fatalf("single attempt expected, got %d", sender.calls)
	}
	// 观测埋点必须到达。
	found := false
	for _, entry := range observer.delivered {
		if entry == "sms|accepted|" {
			found = true
		}
	}
	if !found {
		t.Fatalf("accepted must be observed, got %v", observer.delivered)
	}
}

func TestDirectSenderGuards(t *testing.T) {
	sender := &fakeSender{provider: "aliyun"}
	svc, err := NewDirectSender(map[domain.Channel][]Sender{domain.ChannelSms: {sender}}, &captureObserver{})
	if err != nil {
		t.Fatalf("NewDirectSender: %v", err)
	}
	// 空地址必须拒绝。
	if _, err := svc.Send(context.Background(), DirectRequest{
		Channel: domain.ChannelSms, Address: " ", Rendered: domain.Rendered{Body: "x"},
	}); err == nil {
		t.Fatal("empty address must be rejected")
	}
	// 不支持渠道必须拒绝。
	if _, err := svc.Send(context.Background(), DirectRequest{
		Channel: domain.ChannelInbox, Address: "x", Rendered: domain.Rendered{Body: "x"},
	}); err == nil {
		t.Fatal("inbox must be rejected")
	}
	// 空路由构造必须失败。
	if _, err := NewDirectSender(nil, nil); err == nil {
		t.Fatal("empty route must be rejected")
	}
}

func TestDirectSenderRateLimited(t *testing.T) {
	sender := &fakeSender{provider: "aliyun"}
	observer := &captureObserver{}
	// 零配额滑动窗口：所有请求被限流。
	limiter, err := ratelimit.NewKeyed(func() (ratelimit.Limiter, error) {
		return ratelimit.NewSlidingWindow(0, time.Minute)
	})
	if err != nil {
		t.Fatalf("NewKeyed: %v", err)
	}
	svc, err := NewDirectSender(map[domain.Channel][]Sender{domain.ChannelSms: {sender}},
		observer, WithDirectLimiter(limiter))
	if err != nil {
		t.Fatalf("NewDirectSender: %v", err)
	}
	_, err = svc.Send(context.Background(), DirectRequest{
		Channel: domain.ChannelSms, Address: "+8613800138000", Rendered: domain.Rendered{Body: "x"},
	})
	if err == nil {
		t.Fatal("rate limited must fail")
	}
	if !errors.Is(err, err) || observer.rateHits == 0 {
		t.Fatalf("rate limit must be observed, hits=%d", observer.rateHits)
	}
	// 限流路径不触网。
	if sender.calls != 0 {
		t.Fatalf("rate limited must not reach provider, calls=%d", sender.calls)
	}
}

func TestDirectSenderNoSilentFailover(t *testing.T) {
	// 刻意差异：主备降级不进同步旁路——验证码场景宁可失败让用户重发。
	primary := &fakeSender{provider: "aliyun", failN: 9,
		err: &SendError{Semantic: SemanticProviderError, Retryable: true, Summary: "5xx"}}
	backup := &fakeSender{provider: "backup"}
	svc, err := NewDirectSender(map[domain.Channel][]Sender{
		domain.ChannelSms: {primary, backup},
	}, &captureObserver{})
	if err != nil {
		t.Fatalf("NewDirectSender: %v", err)
	}
	if _, err := svc.Send(context.Background(), DirectRequest{
		Channel: domain.ChannelSms, Address: "+8613800138000", Rendered: domain.Rendered{Body: "x"},
	}); err == nil {
		t.Fatal("primary failure must surface (no silent failover)")
	}
	if backup.calls != 0 {
		t.Fatalf("backup must not be used on sync path, calls=%d", backup.calls)
	}
}
