package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// TestIdentityAuditSQLEmitter 验证审计事件落库、按 actor/target/trace 检索与重试幂等。
func TestIdentityAuditSQLEmitter(t *testing.T) {
	ctx := context.Background()
	sqlDB := testkit.MySQL(t).SQL()
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM audit_event"); err != nil {
		t.Fatalf("clear audit table: %v", err)
	}

	now := time.Now().UTC()
	event := audit.Event{
		EventID:      "evt-audit-1",
		Action:       audit.ActionAuthAccountDisabled,
		ActorID:      "admin-1",
		ActorType:    "admin",
		Channel:      "admin",
		MerchantID:   "m-1",
		ResourceType: "account",
		ResourceID:   "acct-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   now,
		TraceID:      "trace-audit-1",
		Service:      "admin-api",
		Reason:       "left_company",
		Attrs:        map[string]string{"scope": "list"},
	}
	emitter := audit.SQLEmitter{DB: sqlDB}

	if err := emitter.Emit(ctx, event); err != nil {
		t.Fatalf("emit audit event: %v", err)
	}
	// Outbox 重试必须幂等：同一 event_id 重复投递仍只有一行。
	if err := emitter.Emit(ctx, event); err != nil {
		t.Fatalf("emit audit event retry: %v", err)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_event WHERE event_id = ?`, event.EventID).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit rows = %d, want 1 (retry must be idempotent)", count)
	}

	assertAuditCount := func(where, value string) {
		t.Helper()
		var got int
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM audit_event WHERE `+where+` = ?`, value).Scan(&got); err != nil {
			t.Fatalf("query %s: %v", where, err)
		}
		if got != 1 {
			t.Fatalf("audit rows by %s=%q = %d, want 1", where, value, got)
		}
	}
	assertAuditCount("actor_id", event.ActorID)
	assertAuditCount("resource_id", event.ResourceID)
	assertAuditCount("trace_id", event.TraceID)

	var outcome, action string
	var attrs string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT outcome, action, attrs FROM audit_event WHERE event_id = ?`,
		event.EventID).Scan(&outcome, &action, &attrs); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if outcome != string(audit.OutcomeSuccess) || action != event.Action {
		t.Fatalf("audit row outcome/action = %s/%s", outcome, action)
	}
	if attrs == "" {
		t.Fatalf("audit attrs must be persisted")
	}
}
