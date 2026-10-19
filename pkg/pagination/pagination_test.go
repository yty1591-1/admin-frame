package pagination_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

func TestParseDefaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	q, err := pagination.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != pagination.DefaultPage || q.PageSize != pagination.DefaultPageSize {
		t.Fatalf("got page=%d size=%d", q.Page, q.PageSize)
	}
	if q.Offset() != 0 || q.Limit() != pagination.DefaultPageSize {
		t.Fatalf("offset=%d limit=%d", q.Offset(), q.Limit())
	}
}

func TestParseValidAndOffset(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?page=3&page_size=10", http.NoBody)
	q, err := pagination.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != 3 || q.PageSize != 10 || q.Offset() != 20 {
		t.Fatalf("q=%+v offset=%d", q, q.Offset())
	}
}

func TestParseRejectsOverMax(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?page_size=101", http.NoBody)
	_, err := pagination.Parse(r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseWithCustomMaxAndParams(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?p=2&ps=40", http.NoBody)
	q, err := pagination.ParseWith(r, pagination.Config{
		MaxPageSize:   40,
		PageParam:     "p",
		PageSizeParam: "ps",
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != 2 || q.PageSize != 40 {
		t.Fatalf("q=%+v", q)
	}

	_, err = pagination.ParseWith(
		httptest.NewRequest(http.MethodGet, "/x?page_size=50", http.NoBody),
		pagination.Config{MaxPageSize: 40},
	)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseNilRequest(t *testing.T) {
	_, err := pagination.Parse(nil)
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseInvalidNumbersFallBack(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?page=abc&page_size=-1", http.NoBody)
	q, err := pagination.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != pagination.DefaultPage || q.PageSize != pagination.DefaultPageSize {
		t.Fatalf("q=%+v", q)
	}
}

func TestParseSort(t *testing.T) {
	cfg := pagination.SortConfig{
		Allowed: map[string]string{
			"created_at": "created_at",
			"amount":     "amount_cent",
		},
		Default: []pagination.Order{{Name: "created_at", Direction: pagination.Desc}},
	}

	orders, err := pagination.ParseSort(httptest.NewRequest(http.MethodGet, "/x", http.NoBody), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Name != "created_at" || !orders[0].Descending() {
		t.Fatalf("default=%+v", orders)
	}

	orders, err = pagination.ParseSort(
		httptest.NewRequest(http.MethodGet, "/x?sort=created_at,-amount", http.NoBody),
		cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 2 {
		t.Fatalf("len=%d", len(orders))
	}
	if orders[0].Name != "created_at" || orders[0].Direction != pagination.Asc {
		t.Fatalf("o0=%+v", orders[0])
	}
	if orders[1].Name != "amount_cent" || orders[1].Direction != pagination.Desc {
		t.Fatalf("o1=%+v", orders[1])
	}
	if got := pagination.SQLOrderClause(orders); got != "created_at ASC, amount_cent DESC" {
		t.Fatalf("sql=%q", got)
	}
}

func TestParseSortRejectsUnknownAndNoAllowlist(t *testing.T) {
	_, err := pagination.ParseSort(
		httptest.NewRequest(http.MethodGet, "/x?sort=secret", http.NoBody),
		pagination.SortConfig{Allowed: map[string]string{"id": "id"}},
	)
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}

	_, err = pagination.ParseSort(
		httptest.NewRequest(http.MethodGet, "/x?sort=id", http.NoBody),
		pagination.SortConfig{},
	)
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseSortMaxFieldsAndDedupe(t *testing.T) {
	cfg := pagination.SortConfig{
		Allowed:   map[string]string{"a": "a", "b": "b", "c": "c", "d": "d"},
		MaxFields: 2,
	}
	_, err := pagination.ParseSort(
		httptest.NewRequest(http.MethodGet, "/x?sort=a,b,c", http.NoBody),
		cfg,
	)
	if !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("err=%v", err)
	}

	orders, err := pagination.ParseSort(
		httptest.NewRequest(http.MethodGet, "/x?sort=a,-a,b", http.NoBody),
		pagination.SortConfig{Allowed: cfg.Allowed, MaxFields: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 2 || orders[0].Name != "a" || orders[1].Name != "b" {
		t.Fatalf("orders=%+v", orders)
	}
}

func TestParseParams(t *testing.T) {
	p, err := pagination.ParseParams(
		httptest.NewRequest(http.MethodGet, "/x?page=2&page_size=10&sort=-created_at", http.NoBody),
		pagination.Config{},
		pagination.SortConfig{Allowed: map[string]string{"created_at": "created_at"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if p.Query.Page != 2 || p.Query.PageSize != 10 {
		t.Fatalf("query=%+v", p.Query)
	}
	if len(p.Sort) != 1 || !p.Sort[0].Descending() {
		t.Fatalf("sort=%+v", p.Sort)
	}
}

func TestNewResultNilItems(t *testing.T) {
	res := pagination.NewResult(nil, 0)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"items":[],"total":0}` {
		t.Fatalf("json=%s", b)
	}
}
