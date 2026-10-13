package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

func TestParsePageDefaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	page, size, err := httpx.ParsePage(r)
	if err != nil {
		t.Fatal(err)
	}
	if page != httpx.DefaultPage || size != httpx.DefaultPageSize {
		t.Fatalf("page=%d size=%d", page, size)
	}
}

func TestParsePageRejectsOverMax(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?page=2&page_size=101", http.NoBody)
	_, _, err := httpx.ParsePage(r)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *apperr.AppError
	if !errors.As(err, &ae) || ae.Code != apperr.CodeBadRequest {
		t.Fatalf("err=%v", err)
	}
}
