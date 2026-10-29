// Package sms 把 C 端验证码交给 pkg/sms 驱动发送。
package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// AlertKindInsufficientBalance 是厂商余额不足告警类型（低基数）。
const AlertKindInsufficientBalance = "insufficient_balance"

// insufficientBalanceMarkers 是各厂商余额不足错误码的稳定子串。
var insufficientBalanceMarkers = []string{
	"AMOUNT_NOT_ENOUGH",
	"INSUFFICIENT_BALANCE",
	"INSUFFICIENTBALANCE",
	"NOT_ENOUGH_BALANCE",
}

// Alert 是一次短信供应商告警；禁止包含手机号、正文等 PII。
type Alert struct {
	// Kind 低基数告警类型，如 insufficient_balance。
	Kind string
	// Provider 驱动名。
	Provider string
	// Code 厂商原始码，仅排障用，禁止作为指标标签。
	Code string
}

// AlertSink 接收告警；实现必须并发安全且快速返回。
type AlertSink func(Alert)

// CodeSenderOption 配置验证码发送器的可选能力。
type CodeSenderOption func(*CodeSender)

// WithBreaker 注入供应商熔断器；nil 时直连厂商。
func WithBreaker(breaker *circuitbreaker.Breaker) CodeSenderOption {
	return func(s *CodeSender) {
		s.breaker = breaker
	}
}

// WithAlertSink 注入告警出口；nil 时仅返回错误。
func WithAlertSink(sink AlertSink) CodeSenderOption {
	return func(s *CodeSender) {
		s.alert = sink
	}
}

// CodeSender 把验证码渲染为模板短信并交给 pkg/sms 驱动。
type CodeSender struct {
	sender     pkgsms.Sender
	templateID string
	signName   string
	locale     string
	provider   string
	breaker    *circuitbreaker.Breaker
	alert      AlertSink
}

// 编译期断言。
var _ app.CodeSender = (*CodeSender)(nil)

// NewCodeSender 构造验证码发送器；driver 与模板编码必填。
func NewCodeSender(sender pkgsms.Sender, templateID, signName, locale string, opts ...CodeSenderOption) (*CodeSender, error) {
	if sender == nil {
		return nil, fmt.Errorf("sms sender is required")
	}
	if strings.TrimSpace(templateID) == "" {
		return nil, fmt.Errorf("sms template id is required")
	}
	codeSender := &CodeSender{
		sender:     sender,
		templateID: templateID,
		signName:   signName,
		locale:     locale,
		provider:   sender.Provider(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(codeSender)
		}
	}
	return codeSender, nil
}

// SendCode 发送验证码；消息不落日志，Reference 仅使用低基数常量。
func (s *CodeSender) SendCode(ctx context.Context, phone, code string) error {
	msg := pkgsms.Message{
		Phone:      phone,
		SignName:   s.signName,
		TemplateID: s.templateID,
		Params:     map[string]string{"code": code},
		Locale:     s.locale,
		Reference:  "auth_code",
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("identity sms code message: %w", err)
	}
	if _, err := s.send(ctx, msg); err != nil {
		s.reportAlert(err)
		return fmt.Errorf("identity sms code send: %w", err)
	}
	return nil
}

// send 经熔断器调用厂商；熔断打开时返回 circuitbreaker.ErrOpen，不触网。
func (s *CodeSender) send(ctx context.Context, msg pkgsms.Message) (pkgsms.Receipt, error) {
	if s.breaker == nil {
		return s.sender.Send(ctx, msg)
	}
	return circuitbreaker.DoValue(s.breaker, func() (pkgsms.Receipt, error) {
		return s.sender.Send(ctx, msg)
	})
}

// reportAlert 识别余额不足等供应商告警；其他错误仅作为失败返回。
func (s *CodeSender) reportAlert(err error) {
	if s.alert == nil {
		return
	}
	var providerErr *pkgsms.ProviderError
	if !errors.As(err, &providerErr) {
		return
	}
	if !isInsufficientBalanceCode(providerErr.Code) {
		return
	}
	provider := strings.TrimSpace(providerErr.Provider)
	if provider == "" {
		provider = s.provider
	}
	s.alert(Alert{Kind: AlertKindInsufficientBalance, Provider: provider, Code: providerErr.Code})
}

// BreakerIsSuccessful 报告发送错误是否应视为「调用成功」（用于熔断判定）。
//
// 永久拒绝（模板 / 签名 / 号码非法）属于调用方问题，不应触发供应商熔断；
// 仅可重试的供应商故障计入连续失败。
func BreakerIsSuccessful(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, pkgsms.ErrRejected) ||
		errors.Is(err, pkgsms.ErrInvalidMessage) ||
		errors.Is(err, pkgsms.ErrInvalidRecipient) {
		return true
	}
	var providerErr *pkgsms.ProviderError
	if errors.As(err, &providerErr) && !providerErr.Retryable() {
		return true
	}
	return false
}

// isInsufficientBalanceCode 按厂商原始码识别余额不足；大小写不敏感。
func isInsufficientBalanceCode(code string) bool {
	upper := strings.ToUpper(strings.TrimSpace(code))
	if upper == "" {
		return false
	}
	for _, marker := range insufficientBalanceMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
