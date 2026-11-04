package domain

import (
	"errors"
	"strings"
	"testing"
)

// TestNotificationErrorMessages 结构化错误必须携带定位信息（字段 / 取值 / 上限），
// 让 HTTP 层翻译 4xx 与排障都无需再读实现。
func TestNotificationErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{"invalid scene", &InvalidSceneError{Scene: "Order.Paid!"}, []string{`"Order.Paid!"`, "a-z 0-9"}},
		{"invalid template key", &InvalidTemplateKeyError{Key: "User Invite"}, []string{`"User Invite"`, "a-z 0-9"}},
		{"invalid channel", &InvalidChannelError{Channel: "fax"}, []string{`"fax"`, "channel"}},
		{"reserved channel", &ChannelNotImplementedError{Channel: ChannelInbox}, []string{`"inbox"`, "not implemented"}},
		{"invalid priority", &InvalidPriorityError{Priority: "urgent"}, []string{`"urgent"`, "priority"}},
		{"invalid locale", &InvalidLocaleError{Locale: "zh_CN"}, []string{`"zh_CN"`, "locale"}},
		{"invalid transition", &InvalidTransitionError{From: RequestPending, To: RequestSent}, []string{"pending", "sent"}},
		{"invalid delivery transition", &InvalidDeliveryTransitionError{From: DeliveryPending, To: DeliveryDelivered},
			[]string{"pending", "delivered"}},
		{"invalid subject type", &InvalidSubjectTypeError{Value: "robot"}, []string{`"robot"`, "subject type"}},
		{"invalid scene class", &InvalidSceneClassError{Value: "promo"}, []string{`"promo"`, "scene class"}},
		{"invalid blacklist source", &InvalidBlacklistSourceError{Value: "manual"}, []string{`"manual"`, "blacklist source"}},
		{"missing param", &TemplateParamMissingError{Key: "tenant_name"}, []string{`"tenant_name"`, "missing"}},
		{"unknown param", &TemplateParamUnknownError{Key: "raw_html"}, []string{`"raw_html"`, "not declared"}},
		{"long param", &TemplateParamTooLongError{Key: "intro"}, []string{`"intro"`, "exceeds"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Fatalf("Error() = %q, want substring %q", msg, want)
				}
			}
		})
	}
}

// TestNotificationSentinelsAreDistinct 哨兵不得互相 Is：
// 42900（ErrRateLimited）/ 503（ErrChannelUnavailable）/ 409（ErrDuplicateRequest）
// 分别对应不同处置动作，串味会把「可重试」误判成「重复投递」。
func TestNotificationSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrNotFound,
		ErrDuplicateRequest,
		ErrRequestTerminal,
		ErrDeliveryTerminal,
		ErrRateLimited,
		ErrChannelUnavailable,
		ErrExpired,
		ErrBlacklisted,
		ErrNoRoute,
		ErrTransactionalOptOut,
	}
	for i, left := range sentinels {
		for j, right := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(left, right) {
				t.Fatalf("sentinel %d must not match sentinel %d (%v / %v)", i, j, left, right)
			}
		}
	}
}
