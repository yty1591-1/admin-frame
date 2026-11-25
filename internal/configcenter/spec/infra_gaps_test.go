package spec_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestMissingInfraRefs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values map[string]string
		want   []string
	}{
		{name: "nil", values: nil, want: nil},
		{name: "memory email", values: map[string]string{
			"notification.channels.email.driver": "memory",
		}, want: nil},
		{name: "smtp missing host and from", values: map[string]string{
			"notification.channels.email.driver": "smtp",
		}, want: []string{
			"notification/notification.channels.email.smtp.host",
			"notification/notification.channels.email.from",
		}},
		{name: "smtp host only", values: map[string]string{
			"notification.channels.email.driver":    "smtp",
			"notification.channels.email.smtp.host": "mail.example.com",
		}, want: []string{"notification/notification.channels.email.from"}},
		{name: "smtp complete", values: map[string]string{
			"notification.channels.email.driver":    "smtp",
			"notification.channels.email.smtp.host": "mail.example.com",
			"notification.channels.email.from":      "noreply@example.com",
		}, want: nil},
		{name: "redis required missing addr", values: map[string]string{
			"idempotency.driver": "redis",
		}, want: []string{"platform/redis.addr"}},
		{name: "redis required with addr", values: map[string]string{
			"idempotency.driver": "redis",
			"redis.addr":         "127.0.0.1:6379",
		}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := spec.MissingInfraRefs(tc.values)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestRequiredInfraSecrets(t *testing.T) {
	t.Parallel()
	got := spec.RequiredInfraSecrets(map[string]string{
		"async.enabled": "true", "async.driver": "nats",
		"notification.channels.email.driver": "smtp",
	})
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if got[0].Key != "async.broker.url" || got[1].Key != "notification.smtp.password" {
		t.Fatalf("unexpected refs: %#v", got)
	}
	if len(spec.RequiredInfraSecrets(map[string]string{"async.enabled": "false"})) != 0 {
		t.Fatal("disabled async must not require nats url")
	}
}
