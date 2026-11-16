package configboot

import "testing"

func TestWithProcessIdentity(t *testing.T) {
	got := WithProcessIdentity(map[string]string{
		"log.level": "info",
		"addr":      ":8080",
	}, ProcessIdentity{Service: "admin-api", Channel: "admin", Addr: ":8081"})
	if got["service"] != "admin-api" || got["channel"] != "admin" {
		t.Fatalf("identity = %+v", got)
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
