package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestValidatePermissionCode(t *testing.T) {
	cases := []struct {
		name string
		code iam.PermissionCode
		want error
	}{
		{name: "resource action", code: "iam:role:read"},
		{name: "domain resource action", code: "iam:role:write"},
		{name: "four segments", code: "iam:role:binding:read"},
		{name: "underscore", code: "order_item:read"},
		{name: "empty", code: "", want: ErrInvalidPermissionCode},
		{name: "no separator", code: "order", want: ErrInvalidPermissionCode},
		{name: "trailing separator", code: "order:", want: ErrInvalidPermissionCode},
		{name: "uppercase", code: "Order:read", want: ErrInvalidPermissionCode},
		{name: "dash", code: "order-item:read", want: ErrInvalidPermissionCode},
		{name: "leading digit", code: "1iam:role:read", want: ErrInvalidPermissionCode},
		{name: "five segments", code: "a:b:c:d:e", want: ErrInvalidPermissionCode},
		{name: "too long", code: iam.PermissionCode("a:" + strings.Repeat("b", MaxPermissionCodeLen)), want: ErrInvalidPermissionCode},
		{name: "bare wildcard", code: "*", want: ErrWildcardNotAllowed},
		{name: "trailing wildcard", code: "order:*", want: ErrWildcardNotAllowed},
		{name: "embedded wildcard", code: "or*der:read", want: ErrWildcardNotAllowed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePermissionCode(tc.code)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want sentinel %v", err, tc.want)
			}
			var ae *apperr.AppError
			if !errors.As(err, &ae) || ae.Code != apperr.CodeBadRequest {
				t.Fatalf("err=%v, want apperr bad request", err)
			}
		})
	}
}

// TestWildcardRejected 是 TESTING_STANDARDS 必测函数：任何写入路径都不得接受通配符。
func TestWildcardRejected(t *testing.T) {
	if _, err := NewPermissionSet([]iam.PermissionCode{"*"}); !errors.Is(err, ErrWildcardNotAllowed) {
		t.Fatalf("NewPermissionSet(*): err=%v, want ErrWildcardNotAllowed", err)
	}

	role := mustNewRole(t, nil)
	set, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := role.ReplacePermissions(set, "actor", role.Version); err != nil {
		t.Fatal(err)
	}

	// 绕过 NewPermissionSet 直接构造集合，聚合内部仍须拒绝。
	raw := PermissionSet{"order:*": {}}
	if _, err := role.ReplacePermissions(raw, "actor", role.Version); !errors.Is(err, ErrWildcardNotAllowed) {
		t.Fatalf("ReplacePermissions(*): err=%v, want ErrWildcardNotAllowed", err)
	}
}

func TestNewPermissionSetDedupesAndSorts(t *testing.T) {
	set, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleWrite, iam.PermRoleRead, iam.PermRoleRead})
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 2 {
		t.Fatalf("len=%d, want 2", set.Len())
	}
	if !set.Contains(iam.PermRoleRead) || set.Contains(iam.PermOrderRead) {
		t.Fatalf("unexpected membership: %v", set)
	}
	want := []iam.PermissionCode{"iam:role:read", "iam:role:write"}
	got := set.Sorted()
	if len(got) != len(want) {
		t.Fatalf("sorted=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted[%d]=%s, want %s", i, got[i], want[i])
		}
	}
	if set.String() != "iam:role:read,iam:role:write" {
		t.Fatalf("string=%q", set.String())
	}
}

func TestEmptyPermissionSetIsNotNil(t *testing.T) {
	set, err := NewPermissionSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil {
		t.Fatal("empty set must not be nil")
	}
	if set.Len() != 0 {
		t.Fatalf("len=%d", set.Len())
	}
	if set.String() != "" {
		t.Fatalf("string=%q", set.String())
	}
	if set.Sorted() == nil {
		t.Fatal("Sorted must return empty non-nil slice")
	}
}

func TestPermissionSetCloneIsolation(t *testing.T) {
	set, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead})
	if err != nil {
		t.Fatal(err)
	}
	clone := set.Clone()
	clone[iam.PermRoleWrite] = struct{}{}
	if set.Contains(iam.PermRoleWrite) {
		t.Fatal("clone must not mutate original")
	}
}

func TestPermissionSetDiff(t *testing.T) {
	current, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite})
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleWrite, iam.PermOrderRead})
	if err != nil {
		t.Fatal(err)
	}
	change := current.Diff(next)
	if change.IsEmpty() {
		t.Fatal("change must not be empty")
	}
	if len(change.Added) != 1 || change.Added[0] != iam.PermOrderRead {
		t.Fatalf("added=%v", change.Added)
	}
	if len(change.Removed) != 1 || change.Removed[0] != iam.PermRoleRead {
		t.Fatalf("removed=%v", change.Removed)
	}
	if !current.Diff(current).IsEmpty() {
		t.Fatal("identical sets must produce empty change")
	}
}
