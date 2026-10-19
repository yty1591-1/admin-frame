package contextx_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/logger"
)

func TestRequestIDSharesLoggerTrace(t *testing.T) {
	ctx := contextx.WithRequestID(context.Background(), "rid-1")
	if contextx.RequestID(ctx) != "rid-1" {
		t.Fatal(contextx.RequestID(ctx))
	}
	if logger.TraceIDFromContext(ctx) != "rid-1" {
		t.Fatal("logger correlation mismatch")
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	ctx := contextx.WithIdentity(context.Background(), contextx.Identity{
		RequestID:  "r1",
		UserID:     "u1",
		MerchantID: "m1",
		Channel:    "biz",
		Roles:      []string{"merchant_staff", "admin"},
	})
	got := contextx.IdentityFrom(ctx)
	if got.RequestID != "r1" || got.UserID != "u1" || got.MerchantID != "m1" || got.Channel != "biz" {
		t.Fatalf("%+v", got)
	}
	if len(got.Roles) != 2 || got.Roles[0] != "merchant_staff" {
		t.Fatalf("roles=%v", got.Roles)
	}
	// 角色为副本
	got.Roles[0] = "x"
	if contextx.Roles(ctx)[0] != "merchant_staff" {
		t.Fatal("roles not copied")
	}
}

func TestEmptyDoesNotClear(t *testing.T) {
	ctx := contextx.WithUserID(context.Background(), "u1")
	ctx = contextx.WithIdentity(ctx, contextx.Identity{UserID: ""})
	if contextx.UserID(ctx) != "u1" {
		t.Fatal("empty should not clear")
	}
}

func TestLogArgs(t *testing.T) {
	ctx := contextx.WithIdentity(context.Background(), contextx.Identity{
		RequestID:  "r",
		UserID:     "u",
		MerchantID: "m",
		Channel:    "c",
	})
	args := contextx.LogArgs(ctx)
	if len(args) != 8 {
		t.Fatalf("len=%d args=%v", len(args), args)
	}
	m := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		m[args[i].(string)] = args[i+1].(string)
	}
	if m[logger.FieldTraceID] != "r" || m[logger.FieldUserID] != "u" || m[logger.FieldMerchant] != "m" || m[logger.FieldChannel] != "c" {
		t.Fatalf("%v", m)
	}
}

func TestNilContextSafe(t *testing.T) {
	if contextx.UserID(nil) != "" || contextx.MerchantID(nil) != "" {
		t.Fatal("nil ctx")
	}
	_ = contextx.WithUserID(nil, "u")
}
