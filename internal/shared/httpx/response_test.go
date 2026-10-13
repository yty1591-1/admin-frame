package httpx_test

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	xerr "github.com/admin-frame/admin-frame/pkg/errors"
)

func TestFromErrorAppError(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, apperr.NotFound("order missing"), "tid")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusNotFound || env.Code != apperr.CodeNotFound {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
	if env.Message != "order missing" || env.TraceID != "tid" {
		t.Fatalf("env=%+v", env)
	}
}

func TestFromErrorSentinel(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, apperr.ErrUnauthorized, "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusUnauthorized || env.Code != apperr.CodeUnauthorized {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
}

func TestFromErrorParsePageStyle(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, xerr.InvalidArgument("page_size exceeds maximum of 100"), "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
}

func TestFromErrorInternalHidesDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.FromError(rr, apperr.Internal("db dsn=secret", stderrors.New("boom")), "t")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != apperr.CodeInternal || env.Message != "internal error" {
		t.Fatalf("env=%+v", env)
	}
}
