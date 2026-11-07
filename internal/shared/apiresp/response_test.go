package apiresp_test

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	xerr "github.com/admin-frame/admin-frame/pkg/errors"
)

func TestFromErrorAppError(t *testing.T) {
	rr := httptest.NewRecorder()
	apiresp.FromError(rr, apperr.NotFound("order missing"), "tid")

	var env apiresp.Envelope
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
	apiresp.FromError(rr, apperr.ErrUnauthorized, "t")

	var env apiresp.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusUnauthorized || env.Code != apperr.CodeUnauthorized {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
}

func TestFromErrorParsePageStyle(t *testing.T) {
	rr := httptest.NewRecorder()
	apiresp.FromError(rr, xerr.InvalidArgument("page_size exceeds maximum of 100"), "t")

	var env apiresp.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("status=%d env=%+v", rr.Code, env)
	}
}

func TestFromErrorInternalHidesDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	apiresp.FromError(rr, apperr.Internal("db dsn=secret", stderrors.New("boom")), "t")

	var env apiresp.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != apperr.CodeInternal || env.Message != "internal error" {
		t.Fatalf("env=%+v", env)
	}
}
