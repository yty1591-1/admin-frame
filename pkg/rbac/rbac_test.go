package rbac_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

func TestPermissionMatches(t *testing.T) {
	cases := []struct {
		grant, need rbac.Permission
		want        bool
	}{
		{"*", "order:read", true},
		{"order:*", "order:read", true},
		{"order:*", "order:write", true},
		{"order:*", "user:read", false},
		{"order:read", "order:read", true},
		{"order:read", "order:write", false},
		{"", "order:read", false},
	}
	for _, tc := range cases {
		if got := tc.grant.Matches(tc.need); got != tc.want {
			t.Fatalf("%q vs %q: got %v want %v", tc.grant, tc.need, got, tc.want)
		}
	}
}

func TestCheckerDefaultDenyAndRole(t *testing.T) {
	c := rbac.New()
	c.GrantRole("ops", "order:read", "order:write")
	sub := rbac.Subject{Roles: []string{"ops"}}
	if !c.Allow(sub, "order:read") {
		t.Fatal("expected allow")
	}
	if c.Allow(sub, "user:read") {
		t.Fatal("expected deny")
	}
	if c.Allow(rbac.Subject{}, "order:read") {
		t.Fatal("empty subject deny")
	}
}

func TestCheckerSuperRoleAndDirectPerm(t *testing.T) {
	c := rbac.New(rbac.WithSuperRoles("admin"))
	if !c.Allow(rbac.Subject{Roles: []string{"admin"}}, "anything:here") {
		t.Fatal("super role")
	}
	if !c.Allow(rbac.Subject{Permissions: []rbac.Permission{"order:read"}}, "order:read") {
		t.Fatal("direct perm")
	}
}

func TestCheckerWildcardToggle(t *testing.T) {
	c := rbac.New(rbac.WithWildcards(false))
	c.GrantRole("ops", "order:*")
	if c.Allow(rbac.Subject{Roles: []string{"ops"}}, "order:read") {
		t.Fatal("wildcards disabled should not expand order:*")
	}
	if !c.Allow(rbac.Subject{Roles: []string{"ops"}}, "order:*") {
		t.Fatal("exact grant still works")
	}
}

func TestAllowAnyAllExpand(t *testing.T) {
	c := rbac.New()
	c.GrantRole("ops", "order:read")
	sub := rbac.Subject{Roles: []string{"ops"}}
	if !c.AllowAny(sub, "user:read", "order:read") {
		t.Fatal("any")
	}
	if c.AllowAll(sub, "order:read", "order:write") {
		t.Fatal("all should fail")
	}
	c.GrantRole("ops", "order:write")
	if !c.AllowAll(sub, "order:read", "order:write") {
		t.Fatal("all after grant")
	}
	exp := c.Expand(sub)
	if len(exp) != 2 {
		t.Fatalf("expand=%v", exp)
	}
	admin := rbac.New(rbac.WithSuperRoles("admin"))
	if got := admin.Expand(rbac.Subject{Roles: []string{"admin"}}); len(got) != 1 || got[0] != "*" {
		t.Fatalf("super expand=%v", got)
	}
}

func TestDeniedError(t *testing.T) {
	err := rbac.Denied("order:read")
	if !errors.IsCode(err, errors.CodePermissionDenied) {
		t.Fatalf("err=%v", err)
	}
}

func TestRequireMiddleware(t *testing.T) {
	c := rbac.New()
	c.GrantRole("ops", "order:read")

	var ran bool
	h := rbac.Require(rbac.RequireConfig{
		Checker:    c,
		Permission: "order:read",
		SubjectFrom: func(*http.Request) (rbac.Subject, bool) {
			return rbac.Subject{Roles: []string{"ops"}}, true
		},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if !ran || rr.Code != http.StatusOK {
		t.Fatalf("ran=%v code=%d", ran, rr.Code)
	}

	denied := rbac.Require(rbac.RequireConfig{
		Checker:    c,
		Permission: "order:write",
		SubjectFrom: func(*http.Request) (rbac.Subject, bool) {
			return rbac.Subject{Roles: []string{"ops"}}, true
		},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not run")
	}))
	rr2 := httptest.NewRecorder()
	denied.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rr2.Code)
	}

	unauth := rbac.Require(rbac.RequireConfig{
		Checker:    c,
		Permission: "order:read",
		SubjectFrom: func(*http.Request) (rbac.Subject, bool) {
			return rbac.Subject{}, false
		},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not run")
	}))
	rr3 := httptest.NewRecorder()
	unauth.ServeHTTP(rr3, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr3.Code)
	}
}

func TestRevokeRole(t *testing.T) {
	c := rbac.New()
	c.GrantRole("ops", "order:read")
	c.RevokeRole("ops")
	if c.Allow(rbac.Subject{Roles: []string{"ops"}}, "order:read") {
		t.Fatal("revoked")
	}
}
