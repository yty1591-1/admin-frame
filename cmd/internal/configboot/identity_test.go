package configboot

import "testing"

func TestWithProcessIdentity(t *testing.T) {
	got := WithProcessIdentity(map[string]string{
		"log.level": "info",
		"addr":      ":8080",
	}, ProcessIdentity{Service: "admin-api", Channel: "admin", Env: "staging", Addr: ":8081"})
	if got["service"] != "admin-api" || got["channel"] != "admin" {
		t.Fatalf("identity = %+v", got)
	}
	if got["env"] != "staging" {
		t.Fatalf("env = %q, want staging (must be visible to Validate)", got["env"])
	}
	if got["auth.expected_channel"] != "admin" {
		t.Fatalf("expected_channel = %q", got["auth.expected_channel"])
	}
	if got["addr"] != ":8081" || got["server.addr"] != ":8081" {
		t.Fatalf("addr override = %+v", got)
	}
	if got["log.level"] != "info" {
		t.Fatalf("pulled values lost: %+v", got)
	}
}

func TestSetIfEmpty(t *testing.T) {
	values := map[string]string{"auth.mode": "demo"}
	SetIfEmpty(values, "auth.mode", "jwt")
	SetIfEmpty(values, "auth.jwt.issuer", "admin-frame")
	if values["auth.mode"] != "demo" {
		t.Fatalf("must not override existing: %q", values["auth.mode"])
	}
	if values["auth.jwt.issuer"] != "admin-frame" {
		t.Fatalf("must fill empty: %q", values["auth.jwt.issuer"])
	}
}
