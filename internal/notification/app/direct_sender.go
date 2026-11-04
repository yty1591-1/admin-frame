package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	pkgratelimit "github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// DirectSender 是同步发送受控门面（ENT-10 NT-10）：不走 Outbox / 模板 /
// 偏好 / DLQ 的同步旁路，供「用户正盯着输入框」的强交互场景使用。
//
// 例外边界（ADR-0008 §3）：**仅验证码类场景允许**；业务通知禁止走旁路——
// 通知必达语义只能由 Outbox 编排保证。门面收敛了旁路的横切关注点：
// 频控（供应商 / 收件人）、熔断、脱敏错误与指标埋点，调用方不再各自为政。
type DirectSender struct {
	route    map[domain.Channel][]Sender
	breakers map[string]*circuitbreaker.Breaker
	limiter  *pkgratelimit.Keyed
	observer Observer
	now      func() time.Time
}

// DirectSenderOption 配置受控门面。
type DirectSenderOption func(*DirectSender)

// WithDirectLimiter 注入频控（供应商 / 收件人分键）；nil 表示不限流
// （仅允许出现在单测，生产装配必须提供）。
func WithDirectLimiter(limiter *pkgratelimit.Keyed) DirectSenderOption {
	return func(s *DirectSender) { s.limiter = limiter }
}

// WithDirectBreakers 注入供应商熔断器；缺省自动创建。
func WithDirectBreakers(breakers map[string]*circuitbreaker.Breaker) DirectSenderOption {
	return func(s *DirectSender) { s.breakers = breakers }
}

// NewDirectSender 构造同步旁路门面；路由必填。
func NewDirectSender(route map[domain.Channel][]Sender, observer Observer, opts ...DirectSenderOption) (*DirectSender, error) {
	if len(route) == 0 {
		return nil, apperr.Internal("notification direct sender requires a channel route", nil)
	}
	s := &DirectSender{
		route:    route,
		breakers: make(map[string]*circuitbreaker.Breaker, len(route)),
		observer: observerOrNoop(observer),
		now:      time.Now,
	}
	for channel, senders := range route {
		if !channel.Implemented() || len(senders) == 0 {
			return nil, apperr.Internal("notification direct route for channel "+string(channel)+" is invalid", nil)
		}
		for _, sender := range senders {
			if sender == nil || strings.TrimSpace(sender.Provider()) == "" {
				return nil, apperr.Internal("notification direct route sender requires a provider name", nil)
			}
			if _, exists := s.breakers[sender.Provider()]; !exists {
				s.breakers[sender.Provider()] = circuitbreaker.MustNew(circuitbreaker.Config{
					Name: "notify-direct-" + sender.Provider(),
				})
			}
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
}

// DirectRequest 是一次同步发送请求。
type DirectRequest struct {
	// Channel 渠道。
	Channel domain.Channel
	// Address 渠道地址（明文，仅瞬时内存）。
	Address string
	// Rendered 渲染产物（验证码场景由调用方组装，不经模板中心）。
	Rendered domain.Rendered
	// Reference 低基数业务引用（如 "auth_code"）。
	Reference string
	// RequestID 幂等 / 排障关联键（透传渠道驱动）。
	RequestID string
}

// Send 同步发送单条消息：频控 → 熔断包住单次尝试。
//
// 与编排管道的差异（刻意收窄）：无重试（调用方决定重试语义）、无主备
// 降级（验证码场景宁可失败让用户重发，也不静默换供应商）、无状态回写。
// 错误已脱敏：SendError.Summary 不含地址与正文。
func (s *DirectSender) Send(ctx context.Context, req DirectRequest) (SendResult, error) {
	if !req.Channel.Implemented() {
		return SendResult{}, &SendError{Semantic: SemanticInternal, Summary: "unsupported channel"}
	}
	if strings.TrimSpace(req.Address) == "" {
		return SendResult{}, &SendError{Semantic: SemanticInvalidAddress, Summary: "address is required", Retryable: false}
	}
	if s.limiter != nil {
		if result := s.limiter.Allow(providerKey(domain.PriorityCritical, req.Channel)); !result.Allowed {
			s.observer.RateLimited()
			return SendResult{}, &SendError{
				Semantic: SemanticRateLimited, Summary: "provider quota exhausted",
				Retryable: true, RetryAfter: result.RetryAfter,
			}
		}
		if result := s.limiter.Allow(recipientKey("direct", req.Address)); !result.Allowed {
			s.observer.RateLimited()
			return SendResult{}, &SendError{
				Semantic: SemanticRateLimited, Summary: "recipient rate limited",
				Retryable: true, RetryAfter: result.RetryAfter,
			}
		}
	}
	// 刻意不遍历主备：同步旁路不做静默降级（验证码场景宁可失败让用户
	// 重发，也不能悄悄换供应商），只使用主供应商。
	primary := s.route[req.Channel][0]
	breaker := s.breakers[primary.Provider()]
	result, err := circuitbreaker.DoValue(breaker, func() (SendResult, error) {
		return primary.Send(ctx, SendRequest{
			Channel:   req.Channel,
			Address:   req.Address,
			Rendered:  req.Rendered,
			Reference: req.Reference,
			RequestID: req.RequestID,
		})
	})
	if err == nil {
		s.observer.DeliveryObserved(req.Channel, result.Provider, "accepted", "")
		return result, nil
	}
	classified := classifySendError(err)
	if !classified.Retryable && breaker.State() == circuitbreaker.StateOpen {
		classified = &SendError{
			Semantic:  SemanticChannelUnavailable,
			Summary:   "provider " + primary.Provider() + " breaker open",
			Retryable: true,
			Cause:     err,
		}
	}
	s.observer.DeliveryObserved(req.Channel, primary.Provider(), "failed", classified.Semantic)
	return SendResult{}, fmt.Errorf("notification direct send failed: %w", classified)
}
