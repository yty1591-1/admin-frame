package audit_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

func validEvent() audit.Event {
	return audit.Event{
		EventID:      "evt-1",
		Action:       audit.ActionAuthLogin,
		ActorID:      "user-1",
		Channel:      "c",
		ResourceType: "session",
		ResourceID:   "sess-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   time.Now().UTC(),
		TraceID:      "trace-1",
		Service:      "c-api",
	}
}

func TestAuditEventValidate(t *testing.T) {
	e := validEvent()
	if err := e.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}

	bad := e
	bad.EventID = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for empty event_id")
	}

	bad = e
	bad.Channel = "x"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for bad channel")
	}

	bad = e
	bad.Outcome = "ok"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for bad outcome")
	}
}

func TestAuditEventRejectsSecrets(t *testing.T) {
	e := validEvent()
	e.Reason = "Authorization Bearer abc.def.ghi"
	if err := e.Validate(); err == nil {
		t.Fatal("expected AUD-08 rejection for bearer token in reason")
	}
	e.Reason = ""
	e.Attrs = map[string]string{"pwd": "password=secret123"}
	if err := e.Validate(); err == nil {
		t.Fatal("expected AUD-08 rejection for password in attrs")
	}
}

func TestKnownActionsMatchCatalogBaseline(t *testing.T) {
	actions := audit.KnownActions()
	if len(actions) < 8 {
		t.Fatalf("expected baseline actions, got %d", len(actions))
	}
	want := map[string]bool{
		audit.ActionAuthLogin:             true,
		audit.ActionAuthLoginFailed:       true,
		audit.ActionAuthAccountCreated:    true,
		audit.ActionAuthLogout:            true,
		audit.ActionAuthTokenRefresh:      true,
		audit.ActionAuthTokenReused:       true,
		audit.ActionAuthTokenRevoked:      true,
		audit.ActionAuthMFABound:          true,
		audit.ActionAuthMFAFailed:         true,
		audit.ActionAuthMFADisabled:       true,
		audit.ActionAuthAccountLocked:     true,
		audit.ActionAuthAccountDisabled:   true,
		audit.ActionAuthAccountEnabled:    true,
		audit.ActionAuthAccountUnlocked:   true,
		audit.ActionAuthAccountDeleted:    true,
		audit.ActionAuthAccountRestored:   true,
		audit.ActionAuthPasswordChanged:   true,
		audit.ActionAuthPasswordReset:     true,
		audit.ActionAuthProfileUpdated:    true,
		audit.ActionAuthPhoneRebound:      true,
		audit.ActionAuthAccountRead:       true,
		audit.ActionAuthSessionRead:       true,
		audit.ActionAuthMFAReset:          true,
		audit.ActionAuthMFASetup:          true,
		audit.ActionAuthMFARecoveryUsed:   true,
		audit.ActionAuthMFATicketReplay:   true,
		audit.ActionAuthSMS:               true,
		audit.ActionAuthOAuthAuthorize:    true,
		audit.ActionAuthOAuthCallback:     true,
		audit.ActionAuthSAMLAuthorize:     true,
		audit.ActionAuthSAMLCallback:      true,
		audit.ActionAuthM2MTokenIssued:    true,
		audit.ActionAuthM2MTokenDenied:    true,
		audit.ActionAuthM2MClientCreated:  true,
		audit.ActionAuthM2MClientUpdated:  true,
		audit.ActionAuthM2MClientDisabled: true,
		audit.ActionAuthM2MClientEnabled:  true,
		audit.ActionAuthM2MClientDeleted:  true,
		audit.ActionAuthM2MSecretRotated:  true,
		audit.ActionAuthScimUserCreated:   true,
		audit.ActionAuthScimUserUpdated:   true,
		audit.ActionAuthScimUserDeact:     true,
		audit.ActionAuthScimUserDeleted:   true,
		audit.ActionAuthScimGroupUpdated:  true,
		audit.ActionAuthScimTokenCreated:  true,
		audit.ActionAuthScimTokenRevoked:  true,
		audit.ActionAuthRateLimited:       true,
		audit.ActionAuthSessionEvicted:    true,
		audit.ActionAuthTenantSwitch:      true,
		audit.ActionRBACRoleChange:        true,
		audit.ActionRBACBindingChange:     true,
		audit.ActionRBACBreakglass:        true,
		audit.ActionRBACMenuChange:        true,
		audit.ActionRBACSodChange:         true,
		audit.ActionRBACCertification:     true,
		audit.ActionConfigChange:          true,
		audit.ActionDataExport:            true,
		audit.ActionDataQueryScope:        true,
		audit.ActionPaymentCapture:        true,
		audit.ActionPaymentRefund:         true,
		audit.ActionAuditQuery:            true,
		audit.ActionAuditExport:           true,
		audit.ActionAuditRetentionPurge:   true,
	}
	for _, a := range actions {
		if !want[a] {
			t.Fatalf("unexpected action %q", a)
		}
		delete(want, a)
	}
	if len(want) != 0 {
		t.Fatalf("missing actions: %v", want)
	}
}
