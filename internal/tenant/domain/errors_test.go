package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestDomainErrorMessages 结构化错误必须自带定位信息：应用层把它们翻译成
// 4xx，运维靠消息直接判断是哪一个字段 / 取值出错，不需要再看代码。
func TestDomainErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{"version mismatch", &domain.VersionMismatchError{Expected: 3, Current: 5}, []string{"expected 3", "current 5"}},
		{"invalid role", &domain.InvalidRoleError{Role: "root"}, []string{`"root"`, "member role"}},
		{"invalid resource", &domain.InvalidResourceError{Resource: "API.RPS"}, []string{`"API.RPS"`, "a-z 0-9"}},
		{"invalid status", &domain.InvalidStatusError{Status: "gone"}, []string{`"gone"`, "member status"}},
		{"invalid transition", &domain.InvalidTransitionError{From: domain.StatusTerminated, To: domain.StatusActive},
			[]string{"terminated", "active"}},
		{"invalid code", &domain.InvalidCodeError{Code: "Bad Code"}, []string{`"Bad Code"`, "a-z 0-9"}},
		{"field too long", &domain.FieldTooLongError{Field: "description", Max: 512}, []string{"description", "512"}},
		{"invalid email", &domain.InvalidEmailError{Email: "nope"}, []string{`"nope"`, "email"}},
		{"invalid field", &domain.InvalidFieldError{Field: "timezone", Value: "Mars/Base"}, []string{"timezone", "Mars/Base"}},
		{"seat limit", &domain.SeatLimitError{Active: 10, Limit: 10}, []string{"active 10", "limit 10"}},
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

// TestSeatLimitErrorUnwrap 席位错误必须能 errors.Is 到哨兵：
// 应用层据此统一映射 409，而不用断言具体结构体。
func TestSeatLimitErrorUnwrap(t *testing.T) {
	err := &domain.SeatLimitError{Active: 30, Limit: 30}
	if !errors.Is(err, domain.ErrSeatLimitExceeded) {
		t.Fatalf("SeatLimitError must unwrap to ErrSeatLimitExceeded, got %v", err)
	}
	var seatErr *domain.SeatLimitError
	if !errors.As(err, &seatErr) || seatErr.Active != 30 || seatErr.Limit != 30 {
		t.Fatalf("errors.As() = %+v", seatErr)
	}
}

// TestSentinelErrorsAreDistinct 哨兵之间不得互相 Is：映射表按哨兵分流，
// 一旦串味（例如 ErrLastOwner 命中也算 ErrSeatLimitExceeded）就会返回错误的 HTTP 码。
func TestSentinelErrorsAreDistinct(t *testing.T) {
	sentinels := []error{
		domain.ErrNotFound,
		domain.ErrLastOwner,
		domain.ErrNoOwner,
		domain.ErrSeatLimitExceeded,
		domain.ErrInvitationPendingExists,
		domain.ErrInvitationAlreadyAccepted,
		domain.ErrMemberAlreadyExists,
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
