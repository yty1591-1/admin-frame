package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/httpx"
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
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}
}

func TestParsePageWithCustomMax(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?page_size=50", http.NoBody)
	_, _, err := httpx.ParsePageWith(r, httpx.PageConfig{MaxPageSize: 40})
	if err == nil {
		t.Fatal("expected error")
	}
	page, size, err := httpx.ParsePageWith(
		httptest.NewRequest(http.MethodGet, "/x?page=3&page_size=40", http.NoBody),
		httpx.PageConfig{MaxPageSize: 40},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page != 3 || size != 40 {
		t.Fatalf("page=%d size=%d", page, size)
	}
}

func TestOffset(t *testing.T) {
	if got := httpx.Offset(1, 20); got != 0 {
		t.Fatalf("got=%d", got)
	}
	if got := httpx.Offset(3, 20); got != 40 {
		t.Fatalf("got=%d", got)
	}
	if got := httpx.Offset(0, 20); got != 0 {
		t.Fatalf("page<=0 got=%d", got)
	}
}

func TestNewPageDataNilItems(t *testing.T) {
	pd := httpx.NewPageData(nil, 0)
	b, err := json.Marshal(pd)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"items":[],"total":0}` {
		t.Fatalf("json=%s", b)
	}
}
