package persistence

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestMarshalUnmarshalScopeValues(t *testing.T) {
	all := domain.DataScope{Resource: "order", Type: domain.DataScopeAll}
	payload, err := marshalScopeValues(all)
	if err != nil {
		t.Fatal(err)
	}
	if payload != "{}" {
		t.Fatalf("payload=%q, want {}", payload)
	}

	set := domain.DataScope{Resource: "order", Type: domain.DataScopeMerchantSet, MerchantIDs: []string{"m1", "m2"}}
	payload, err = marshalScopeValues(set)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"m1"`) || !strings.Contains(payload, `"m2"`) {
		t.Fatalf("payload=%q", payload)
	}

	ids, err := unmarshalScopeValues([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "m1" || ids[1] != "m2" {
		t.Fatalf("ids=%v", ids)
	}

	if ids, err := unmarshalScopeValues(nil); err != nil || ids != nil {
		t.Fatalf("empty payload ids=%v err=%v", ids, err)
	}
	if ids, err := unmarshalScopeValues([]byte(`{}`)); err != nil || len(ids) != 0 {
		t.Fatalf("empty object ids=%v err=%v", ids, err)
	}
	if _, err := unmarshalScopeValues([]byte(`not-json`)); err == nil {
		t.Fatal("invalid JSON must fail")
	}
}

func TestIsDuplicateEntry(t *testing.T) {
	if isDuplicateEntry(nil) {
		t.Fatal("nil must not be duplicate")
	}
	if !isDuplicateEntry(&mysql.MySQLError{Number: mysqlErrDuplicateEntry}) {
		t.Fatal("1062 must be duplicate")
	}
	if isDuplicateEntry(&mysql.MySQLError{Number: 1452}) {
		t.Fatal("foreign key error must not be treated as duplicate")
	}
	if isDuplicateEntry(errors.New("random")) {
		t.Fatal("generic error must not be duplicate")
	}
}

func TestConflictOnDuplicate(t *testing.T) {
	if err := conflictOnDuplicate(nil, "dup"); err != nil {
		t.Fatalf("nil must stay nil, got %v", err)
	}
	err := conflictOnDuplicate(&mysql.MySQLError{Number: mysqlErrDuplicateEntry}, "role code exists")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	foreignKey := &mysql.MySQLError{Number: 1452}
	if got := conflictOnDuplicate(foreignKey, "dup"); !errors.Is(got, foreignKey) {
		t.Fatalf("non-duplicate error must pass through, got %v", got)
	}
}

func TestPaginationHelpers(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{{-1, maxListLimit}, {0, maxListLimit}, {1, 1}, {maxListLimit, maxListLimit}, {maxListLimit + 1, maxListLimit}}
	for _, tc := range cases {
		if got := normalizeLimit(tc.in); got != tc.want {
			t.Fatalf("normalizeLimit(%d)=%d, want %d", tc.in, got, tc.want)
		}
	}
	if got := normalizeOffset(-5); got != 0 {
		t.Fatalf("normalizeOffset(-5)=%d", got)
	}
	if got := normalizeOffset(7); got != 7 {
		t.Fatalf("normalizeOffset(7)=%d", got)
	}
}

func TestNullTime(t *testing.T) {
	if got := nullTime(nil); got != nil {
		t.Fatalf("nil pointer must map to nil, got %v", got)
	}
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	got, ok := nullTime(&at).(time.Time)
	if !ok {
		t.Fatalf("value must be time.Time, got %T", nullTime(&at))
	}
	if got.Location() != time.UTC || !got.Equal(at) {
		t.Fatalf("value=%v, want UTC %v", got, at.UTC())
	}
}
