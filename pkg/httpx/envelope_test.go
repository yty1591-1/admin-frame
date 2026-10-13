package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/httpx"
)

func TestOKEnvelope(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.OK(rr, map[string]string{"id": "1"}, "trace-1")

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != httpx.ContentTypeJSON {
		t.Fatalf("content-type=%q", ct)
	}
	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != httpx.CodeOK || env.Message != "ok" || env.TraceID != "trace-1" {
		t.Fatalf("env=%+v", env)
	}
	data, ok := env.Data.(map[string]any)
	if !ok || data["id"] != "1" {
		t.Fatalf("data=%v", env.Data)
	}
}

func TestFailOmitsData(t *testing.T) {
	rr := httptest.NewRecorder()
	httpx.Fail(rr, http.StatusNotFound, httpx.BizCodeNotFound, "not found", "t2")

	var env httpx.Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != httpx.BizCodeNotFound || env.Message != "not found" || env.Data != nil {
		t.Fatalf("env=%+v", env)
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}
