package sms

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// CodeSender 把验证码渲染为模板短信并交给 pkg/sms 驱动。
type CodeSender struct {
	sender     pkgsms.Sender
	templateID string
	signName   string
	locale     string
}

// 编译期断言。
var _ app.CodeSender = (*CodeSender)(nil)

// NewCodeSender 构造验证码发送器；driver 与模板编码必填。
func NewCodeSender(sender pkgsms.Sender, templateID, signName, locale string) (*CodeSender, error) {
	if sender == nil {
		return nil, fmt.Errorf("sms sender is required")
	}
	if strings.TrimSpace(templateID) == "" {
		return nil, fmt.Errorf("sms template id is required")
	}
	return &CodeSender{
		sender:     sender,
		templateID: templateID,
		signName:   signName,
		locale:     locale,
	}, nil
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
	if _, err := s.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("identity sms code send: %w", err)
	}
	return nil
}
