// Package channel 把 pkg/email|sms|push 渠道驱动适配为通知编排层的
// app.Sender 端口（ENT-10 NT-04）。
//
// 职责只有两个：消息形态映射（Rendered → 各渠道 Message）与错误归一化
// （驱动哨兵 → app.SendError 语义码 / 可重试性）。适配器不做重试、
// 不做频控、不碰配置装配——那些属于编排层与 runtime。
package channel

import (
	"context"
	"errors"
	"strings"

	pkgemail "github.com/admin-frame/admin-frame/pkg/email"
	pkgpush "github.com/admin-frame/admin-frame/pkg/push"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"

	"github.com/admin-frame/admin-frame/internal/notification/app"
)

// EmailSender 适配 pkg/email.Sender。
type EmailSender struct {
	// Inner 邮件驱动。
	Inner pkgemail.Sender
	// From 默认发件人；空则要求驱动配置已带默认值。
	From pkgemail.Address
}

// NewEmailSender 构造邮件适配器。
func NewEmailSender(inner pkgemail.Sender, from pkgemail.Address) *EmailSender {
	return &EmailSender{Inner: inner, From: from}
}

// Provider 返回驱动名。
func (s *EmailSender) Provider() string { return s.Inner.Provider() }

// Send 映射消息并归一化错误。
func (s *EmailSender) Send(ctx context.Context, req app.SendRequest) (app.SendResult, error) {
	msg := pkgemail.Message{
		Subject:        req.Rendered.Subject,
		Text:           req.Rendered.Body,
		Reference:      req.Reference,
		IdempotencyKey: req.RequestID,
	}
	if s.From.Email != "" {
		msg.From = s.From
	}
	msg.To = []pkgemail.Address{{Email: strings.TrimSpace(req.Address)}}
	receipt, err := s.Inner.Send(ctx, msg)
	if err != nil {
		return app.SendResult{}, classify(err, map[error]sendClass{
			pkgemail.ErrInvalidRecipient: {semantic: app.SemanticInvalidAddress, retryable: false},
			pkgemail.ErrInvalidMessage:   {semantic: app.SemanticInternal, retryable: false},
			pkgemail.ErrRejected:         {semantic: app.SemanticProviderError, retryable: false},
		})
	}
	return app.SendResult{Provider: receipt.Provider, MessageID: receipt.MessageID}, nil
}

// SMSSender 适配 pkg/sms.Sender。
type SMSSender struct {
	// Inner 短信驱动。
	Inner pkgsms.Sender
	// SignName 默认签名；国内渠道必填（驱动配置也可带默认值）。
	SignName string
	// TemplateID 厂商模板编码；通知域模板优先（模板 key 由编排层渲染），这里
	// 传编排渲染后的参数包给厂商模板——短信渠道的 Body 即厂商模板 ID + 参数约定，
	// 与规划 §2.B「sms 正文是模板参数描述」对齐。
	TemplateID string
}

// NewSMSSender 构造短信适配器。
func NewSMSSender(inner pkgsms.Sender, signName, templateID string) *SMSSender {
	return &SMSSender{Inner: inner, SignName: signName, TemplateID: templateID}
}

// Provider 返回驱动名。
func (s *SMSSender) Provider() string { return s.Inner.Provider() }

// Send 映射消息并归一化错误；短信无回执渠道 MessageID 可为空。
func (s *SMSSender) Send(ctx context.Context, req app.SendRequest) (app.SendResult, error) {
	msg := pkgsms.Message{
		Phone:          req.Address,
		SignName:       s.SignName,
		TemplateID:     s.TemplateID,
		Content:        req.Rendered.Body,
		Locale:         "zh-CN",
		IdempotencyKey: req.RequestID,
		Reference:      req.Reference,
	}
	receipt, err := s.Inner.Send(ctx, msg)
	if err != nil {
		return app.SendResult{}, classify(err, map[error]sendClass{
			pkgsms.ErrInvalidRecipient: {semantic: app.SemanticInvalidAddress, retryable: false},
			pkgsms.ErrInvalidMessage:   {semantic: app.SemanticInternal, retryable: false},
			pkgsms.ErrRejected:         {semantic: app.SemanticProviderError, retryable: false},
		})
	}
	return app.SendResult{Provider: receipt.Provider, MessageID: receipt.MessageID}, nil
}

// PushSender 适配 pkg/push.Sender。
type PushSender struct {
	// Inner 推送驱动。
	Inner pkgpush.Sender
	// Platform 目标平台（apns / fcm 由驱动决定；消息体仍需指定载荷平台）。
	Platform pkgpush.Platform
	// TTLOffline 离线保留时长；0 取平台默认。
	TTLOffline int64
}

// NewPushSender 构造推送适配器。
func NewPushSender(inner pkgpush.Sender, platform pkgpush.Platform) *PushSender {
	return &PushSender{Inner: inner, Platform: platform}
}

// Provider 返回驱动名。
func (s *PushSender) Provider() string { return s.Inner.Provider() }

// Send 映射消息并归一化错误；Token 失效单独标记（编排层据此触发清理）。
func (s *PushSender) Send(ctx context.Context, req app.SendRequest) (app.SendResult, error) {
	msg := pkgpush.Message{
		Token:          req.Address,
		Platform:       s.Platform,
		Title:          req.Rendered.Subject,
		Body:           req.Rendered.Body,
		IdempotencyKey: req.RequestID,
		Reference:      req.Reference,
	}
	receipt, err := s.Inner.Send(ctx, msg)
	if err != nil {
		classes := map[error]sendClass{
			pkgpush.ErrInvalidMessage: {semantic: app.SemanticInternal, retryable: false},
			pkgpush.ErrRejected:       {semantic: app.SemanticProviderError, retryable: false},
		}
		if errors.Is(err, pkgpush.ErrTokenInvalid) {
			return app.SendResult{}, &app.SendError{
				Semantic: app.SemanticTokenInvalid, Summary: "push token invalid",
				Retryable: false, Cause: err,
			}
		}
		return app.SendResult{}, classify(err, classes)
	}
	return app.SendResult{Provider: receipt.Provider, MessageID: receipt.MessageID}, nil
}

type sendClass struct {
	semantic  string
	retryable bool
}

// classify 把驱动错误归一化为 SendError：命中哨兵映射按映射分类；
// 上下文取消不重试（上游已放弃）；其余默认可重试的 provider_error
// （网络抖动 / 5xx 是供应商故障的常态）。
func classify(err error, mapping map[error]sendClass) *app.SendError {
	if err == nil {
		return nil
	}
	for sentinel, class := range mapping {
		if errors.Is(err, sentinel) {
			return &app.SendError{
				Semantic: class.semantic, Summary: err.Error(),
				Retryable: class.retryable, Cause: err,
			}
		}
	}
	if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "context deadline exceeded") {
		return &app.SendError{
			Semantic: app.SemanticTimeout, Summary: err.Error(), Retryable: true, Cause: err,
		}
	}
	return &app.SendError{
		Semantic: app.SemanticProviderError, Summary: err.Error(), Retryable: true, Cause: err,
	}
}

// Compile-time：适配器必须满足编排端口。
var (
	_ app.Sender = (*EmailSender)(nil)
	_ app.Sender = (*SMSSender)(nil)
	_ app.Sender = (*PushSender)(nil)
)
