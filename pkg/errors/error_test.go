package errors_test

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

func TestNewAndCode(t *testing.T) {
	err := errors.NotFound("order missing").
		WithOp("order.Get").
		WithField("order_id", "o1")

	if err.Code() != errors.CodeNotFound {
		t.Fatalf("code=%s", err.Code())
	}
	if err.Message() != "order missing" {
		t.Fatalf("msg=%s", err.Message())
	}
	if err.Op() != "order.Get" {
		t.Fatalf("op=%s", err.Op())
	}
	fs := err.Fields()
	if len(fs) != 1 || fs[0].Key != "order_id" {
		t.Fatalf("fields=%v", fs)
	}
	if !errors.Is(err, errors.ErrNotFound) {
		t.Fatal("expected Is ErrNotFound")
	}
	if !errors.IsCode(err, errors.CodeNotFound) {
		t.Fatal("expected IsCode")
	}
}

func TestWrapPreservesCauseAndStack(t *testing.T) {
	root := errors.New("root").WithCode(errors.CodeInternal)
	wrapped := errors.Wrap(root, "layer").WithOp("svc.Do")

	if !stderrors.Is(wrapped, root) && wrapped.Unwrap() != root {
		t.Fatal("unwrap mismatch")
	}
	if Cause := errors.Cause(wrapped); Cause.Error() != "root" {
		t.Fatalf("cause=%v", Cause)
	}
	if wrapped.StackTrace() == nil {
		t.Fatal("expected stack")
	}
	// 包装 *Error 应复用栈，不丢 code 可重试信息
	if len(wrapped.StackTrace()) == 0 {
		t.Fatal("empty stack")
	}
}

func TestWrapNil(t *testing.T) {
	if errors.Wrap(nil, "x") != nil {
		t.Fatal("Wrap(nil) should be nil")
	}
}

func TestPublicMessageHidesInternal(t *testing.T) {
	err := errors.Internal("db connection reset").WithDetail("dsn=secret")
	if got := err.PublicMessage(); got != "internal error" {
		t.Fatalf("public=%q", got)
	}
	safe := errors.NotFound("order not found")
	if got := safe.PublicMessage(); got != "order not found" {
		t.Fatalf("public=%q", got)
	}
}

func TestRetryableDefaults(t *testing.T) {
	if !errors.Unavailable("down").Retryable() {
		t.Fatal("unavailable should be retryable")
	}
	if errors.NotFound("x").Retryable() {
		t.Fatal("not found should not be retryable")
	}
	if !errors.NotFound("x").WithRetryable(true).Retryable() {
		t.Fatal("explicit retryable")
	}
}

func TestIsAcrossFmtWrap(t *testing.T) {
	base := errors.Unauthenticated("no token")
	wrapped := fmt.Errorf("auth: %w", base)
	if !errors.Is(wrapped, errors.ErrUnauthenticated) {
		t.Fatal("Is through fmt.Errorf %w")
	}
	if errors.CodeOf(wrapped) != errors.CodeUnauthenticated {
		t.Fatalf("code=%s", errors.CodeOf(wrapped))
	}
}

func TestAsError(t *testing.T) {
	std := stderrors.New("plain")
	e := errors.AsError(std)
	if e == nil || e.Code() != errors.CodeUnknown {
		t.Fatalf("got=%v", e)
	}
	if e.Unwrap() != std {
		t.Fatal("cause not preserved")
	}
}

func TestJoinAndCombine(t *testing.T) {
	e1 := errors.New("a")
	e2 := errors.New("b")
	joined := errors.Join(e1, e2)
	if joined == nil {
		t.Fatal("join nil")
	}
	if !errors.Is(joined, e1) || !errors.Is(joined, e2) {
		t.Fatal("join Is")
	}
	c := errors.Combine("batch", e1, e2)
	if c == nil || c.Code() != errors.CodeInternal {
		t.Fatalf("combine=%v", c)
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := map[errors.Code]int{
		errors.CodeNotFound:          http.StatusNotFound,
		errors.CodeUnauthenticated:   http.StatusUnauthorized,
		errors.CodePermissionDenied:  http.StatusForbidden,
		errors.CodeAlreadyExists:     http.StatusConflict,
		errors.CodeResourceExhausted: http.StatusTooManyRequests,
		errors.CodeInternal:          http.StatusInternalServerError,
		errors.CodeInvalidArgument:   http.StatusBadRequest,
	}
	for code, want := range cases {
		if got := errors.HTTPStatus(code); got != want {
			t.Fatalf("%s: got %d want %d", code, got, want)
		}
	}
	err := errors.NotFound("x")
	if errors.HTTPStatusOf(err) != http.StatusNotFound {
		t.Fatal("HTTPStatusOf")
	}
}

func TestFormatVerboseContainsStack(t *testing.T) {
	err := errors.Internal("boom").WithOp("test.Op").WithField("k", 1)
	s := fmt.Sprintf("%+v", err)
	if !strings.Contains(s, "code: internal") {
		t.Fatalf("missing code: %s", s)
	}
	if !strings.Contains(s, "op: test.Op") {
		t.Fatalf("missing op: %s", s)
	}
	if !strings.Contains(s, "stack:") {
		t.Fatalf("missing stack: %s", s)
	}
}

func TestCaptureStackToggle(t *testing.T) {
	prev := errors.CaptureStackEnabled()
	defer errors.SetCaptureStack(prev)

	errors.SetCaptureStack(false)
	err := errors.New("no stack")
	if err.StackTrace() != nil {
		t.Fatal("expected no stack when disabled")
	}
	errors.SetCaptureStack(true)
	err2 := errors.New("with stack")
	if err2.StackTrace() == nil {
		t.Fatal("expected stack when enabled")
	}
}

func TestCodeHelpers(t *testing.T) {
	if !errors.CodeInvalidArgument.IsValid() {
		t.Fatal("valid")
	}
	if errors.Code("custom_biz").IsValid() {
		t.Fatal("custom should be invalid predefined")
	}
	if errors.Code("").String() != "unknown" {
		t.Fatal("empty string")
	}
}
