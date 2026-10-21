package httpx_test

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/httpx"
)

type codedErr struct {
	code int
	msg  string
	err  error
}

func (e *codedErr) Error() string    { return e.msg }
func (e *codedErr) Unwrap() error    { return e.err }
func (e *codedErr) NumericCode() int { return e.code }

func TestFromErrorNumericCoder(t *testing.T) {
	rr := httptest.NewRecorder()
	err := &codedErr{
		code: httpx.BizCodeNotFound,
		msg:  "order gone",
		err:  errors.NotFound("order gone"),
	}
	httpx.FromError(rr, err, "tid")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	if env.Code != httpx.BizCodeNotFound || env.Message != "order gone" || env.TraceID != "tid" {
		t.Fatalf("env=%+v", env)
	}
}

func TestFromErrorSemanticDefault(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, errors.InvalidArgument("bad page_size"), "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusBadRequest || env.Code != httpx.BizCodeBadRequest {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
	if env.Message != "bad page_size" {
		t.Fatalf("message=%q", env.Message)
	}
}

func TestFromErrorHidesInternalDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, errors.Internal("db password=secret"), "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusInternalServerError || env.Code != httpx.BizCodeInternal {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
	if env.Message != "internal error" {
		t.Fatalf("leaked message=%q", env.Message)
	}
}

func TestFromErrorUnknownIsSafe(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, stderrors.New("sql: connection refused"), "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != httpx.BizCodeInternal || env.Message != "internal error" {
		t.Fatalf("env=%+v", env)
	}
}

func TestFromErrorWithMessageOverride(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, errors.NotFound("order 42"), "t",
		httpx.WithMessage(func(error) string { return "resource not found" }),
	)
	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Message != "resource not found" {
		t.Fatalf("message=%q", env.Message)
	}
}

func TestHTTPStatusFromBizCode(t *testing.T) {
	cases := map[int]int{
		httpx.CodeOK:                    http.StatusOK,
		httpx.BizCodeBadRequest:         http.StatusBadRequest,
		httpx.BizCodeUnauthorized:       http.StatusUnauthorized,
		httpx.BizCodeForbidden:          http.StatusForbidden,
		httpx.BizCodeNotFound:           http.StatusNotFound,
		httpx.BizCodeConflict:           http.StatusConflict,
		httpx.BizCodeTooManyRequests:    http.StatusTooManyRequests,
		httpx.BizCodeServiceUnavailable: http.StatusServiceUnavailable,
		httpx.BizCodeInternal:           http.StatusInternalServerError,
		49999:                           http.StatusBadRequest,
		60000:                           http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := httpx.HTTPStatusFromBizCode(code); got != want {
			t.Fatalf("code=%d got=%d want=%d", code, got, want)
		}
	}
}

func TestDefaultBizCodeAlignment(t *testing.T) {
	cases := []struct {
		sem  errors.Code
		want int
	}{
		{errors.CodeInvalidArgument, httpx.BizCodeBadRequest},
		{errors.CodeUnauthenticated, httpx.BizCodeUnauthorized},
		{errors.CodePermissionDenied, httpx.BizCodeForbidden},
		{errors.CodeNotFound, httpx.BizCodeNotFound},
		{errors.CodeAlreadyExists, httpx.BizCodeConflict},
		{errors.CodeResourceExhausted, httpx.BizCodeTooManyRequests},
		{errors.CodeUnavailable, httpx.BizCodeServiceUnavailable},
		{errors.CodeInternal, httpx.BizCodeInternal},
	}
	for _, tc := range cases {
		if got := httpx.DefaultBizCode(tc.sem); got != tc.want {
			t.Fatalf("sem=%s got=%d want=%d", tc.sem, got, tc.want)
		}
	}
}
