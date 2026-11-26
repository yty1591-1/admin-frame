package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// TestAuditPayloadDecodesIntoAuditEvent 守护引导审计载荷与消费端结构的兼容性。
//
// 回归背景：attrs 曾写入 bool，消费端 audit.Event.Attrs 是 map[string]string，
// 解码失败被 Relay 判为永久错误并进死信——引导管理员建号在生产上也查不到审计。
func TestAuditPayloadDecodesIntoAuditEvent(t *testing.T) {
	now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC)
	payload, err := auditPayload(1900000000000000002, "subject-index-fixture", "admin-api", now)
	if err != nil {
		t.Fatalf("auditPayload: %v", err)
	}

	var event audit.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode into audit.Event: %v (payload=%s)", err, payload)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("audit event invalid: %v", err)
	}
	if event.Action != "auth.account.created" {
		t.Fatalf("action = %q, want auth.account.created", event.Action)
	}
	if event.Channel != "admin" || event.ActorType != "system" {
		t.Fatalf("channel/actor_type = %q/%q, want admin/system", event.Channel, event.ActorType)
	}
	if event.ResourceID != "1900000000000000002" {
		t.Fatalf("resource_id = %q, want account id", event.ResourceID)
	}
	if event.Attrs["initial_credential_reset"] != "true" {
		t.Fatalf("attrs.initial_credential_reset = %q, want \"true\"", event.Attrs["initial_credential_reset"])
	}
	if event.Attrs["subject_index"] != "subject-index-fixture" {
		t.Fatalf("attrs.subject_index = %q", event.Attrs["subject_index"])
	}
	// 载荷不得出现明文登录名或口令。
	for _, forbidden := range []string{DefaultSubject, "Admin#Bootstrap2026"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("payload leaks plaintext %q: %s", forbidden, payload)
		}
	}
}
