package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/pkg/envelope"
)

func newSecretService(t *testing.T) (*app.SecretService, *memory.SecretRepositories) {
	t.Helper()
	repos := memory.NewSecrets()
	kek, err := envelope.NewLocalKEK(bytes32(0x42), "local/test")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewSecretService(repos.Secrets, repos.Grants, repos.Access, kek,
		&fakeEmitter{}, newIDs(), app.Options{Service: "test", Now: func() time.Time {
			return time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
		}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, repos
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestSystemAdminSecretSubmitAutoApproves(t *testing.T) {
	svc, _ := newSecretService(t)
	ctx := context.Background()
	admin := app.Actor{
		SubjectID: "u-super", Channel: "admin",
		Roles: []string{app.RoleCodeSystemAdmin},
	}
	secret, err := svc.CreateSecret(ctx, app.CreateSecretInput{
		Env: "dev", Namespace: "platform", Name: "platform.demo.hmac_key",
		Owner: "team-platform", Plaintext: "super-secret-value", Actor: admin,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	activated, err := svc.SubmitSecret(ctx, secret.ID, admin)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if activated.Status != domain.SecretStatusActive {
		t.Fatalf("status = %s, want active", activated.Status)
	}
}

func TestSecretLifecycleAndResolve(t *testing.T) {
	svc, _ := newSecretService(t)
	ctx := context.Background()
	creator := app.Actor{SubjectID: "u-create", Channel: "admin"}
	approver := app.Actor{SubjectID: "u-approve", Channel: "admin"}

	secret, err := svc.CreateSecret(ctx, app.CreateSecretInput{
		Env: "dev", Namespace: "platform", Name: "platform.demo.hmac_key",
		Owner: "team-platform", Plaintext: "super-secret-value", Actor: creator,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if secret.Fingerprint == "" || strings.Contains(secret.Fingerprint, "super-secret") {
		t.Fatalf("fingerprint leaked plaintext: %q", secret.Fingerprint)
	}
	if _, err := svc.SubmitSecret(ctx, secret.ID, creator); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: creator,
	}); err == nil {
		t.Fatal("self-approve must fail")
	}
	secret, err = svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if secret.Status != domain.SecretStatusActive {
		t.Fatalf("status = %s", secret.Status)
	}

	grant, err := svc.CreateGrant(ctx, app.CreateGrantInput{
		SecretID: secret.ID, ServiceIdentity: "spiffe://local/biz-api", Actor: creator,
	})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if grant.Permission != "pending" {
		t.Fatalf("grant permission = %s, want pending", grant.Permission)
	}
	if _, err := svc.Resolve(ctx, app.ResolveInput{
		Env: "dev", Namespace: "platform", Names: []string{"platform.demo.hmac_key"},
		ServiceIdentity: "spiffe://local/biz-api",
	}); err == nil {
		t.Fatal("resolve with pending grant must fail")
	}
	if _, err := svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	}); err != nil {
		t.Fatalf("Approve grant: %v", err)
	}

	if _, err := svc.Resolve(ctx, app.ResolveInput{
		Env: "dev", Namespace: "platform", Names: []string{"platform.demo.hmac_key"},
		ServiceIdentity: "spiffe://local/other",
	}); err == nil {
		t.Fatal("resolve without grant must fail")
	}
	items, err := svc.Resolve(ctx, app.ResolveInput{
		Env: "dev", Namespace: "platform", Names: []string{"platform.demo.hmac_key"},
		ServiceIdentity: "spiffe://local/biz-api", TraceID: "t1",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(items) != 1 || items[0].Value != "super-secret-value" {
		t.Fatalf("resolve items = %+v", items)
	}

	secret, err = svc.RotateSecret(ctx, app.RotateSecretInput{
		ID: secret.ID, Plaintext: "rotated-secret-value", Actor: creator,
	})
	if err != nil {
		t.Fatalf("Rotate submit: %v", err)
	}
	if secret.PendingAction != domain.PendingActionRotate {
		t.Fatalf("pending_action = %q", secret.PendingAction)
	}
	if secret.ActiveVersion == nil || *secret.ActiveVersion != 1 {
		t.Fatalf("active version before approve = %v", secret.ActiveVersion)
	}
	items, err = svc.Resolve(ctx, app.ResolveInput{
		Env: "dev", Namespace: "platform", Names: []string{"platform.demo.hmac_key"},
		ServiceIdentity: "spiffe://local/biz-api",
	})
	if err != nil || items[0].Value != "super-secret-value" {
		t.Fatalf("resolve during pending rotate: %v %+v", err, items)
	}
	secret, err = svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	})
	if err != nil {
		t.Fatalf("Approve rotate: %v", err)
	}
	if secret.ActiveVersion == nil || *secret.ActiveVersion != 2 {
		t.Fatalf("active version = %v", secret.ActiveVersion)
	}
	items, err = svc.Resolve(ctx, app.ResolveInput{
		Env: "dev", Namespace: "platform", Names: []string{"platform.demo.hmac_key"},
		ServiceIdentity: "spiffe://local/biz-api",
	})
	if err != nil || items[0].Value != "rotated-secret-value" {
		t.Fatalf("resolve after rotate: %v %+v", err, items)
	}

	if _, err := svc.RevokeSecret(ctx, secret.ID, "drill", creator); err != nil {
		t.Fatalf("Revoke submit: %v", err)
	}
	if _, err := svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	}); err != nil {
		t.Fatalf("Approve revoke: %v", err)
	}
	secret, err = svc.Get(ctx, secret.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if secret.Status != domain.SecretStatusRevoked {
		t.Fatalf("status after revoke = %s", secret.Status)
	}
}

func TestSecretRuleGenerate(t *testing.T) {
	rule := domain.SecretRule{MinLen: 16, Encoding: "hex", AutoGenerate: true}
	value, err := rule.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(value) < 16 {
		t.Fatalf("generated too short: %q", value)
	}
}

func TestActivateVersionRequiresBreakGlass(t *testing.T) {
	svc, _ := newSecretService(t)
	ctx := context.Background()
	creator := app.Actor{SubjectID: "u-create", Channel: "admin"}
	approver := app.Actor{SubjectID: "u-approve", Channel: "admin"}

	secret, err := svc.CreateSecret(ctx, app.CreateSecretInput{
		Env: "dev", Namespace: "platform", Name: "platform.demo.hmac_key",
		Owner: "team-platform", Plaintext: "super-secret-value", Actor: creator,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := svc.SubmitSecret(ctx, secret.ID, creator); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	secret, err = svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	secret, err = svc.RotateSecret(ctx, app.RotateSecretInput{
		ID: secret.ID, Plaintext: "rotated-secret-value", Actor: creator,
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	secret, err = svc.ApproveSecret(ctx, app.ApproveSecretInput{
		ID: secret.ID, Decision: domain.DecisionApproved, Actor: approver,
	})
	if err != nil {
		t.Fatalf("Approve rotate: %v", err)
	}

	if _, err := svc.ActivateVersion(ctx, app.ActivateVersionInput{
		SecretID: secret.ID, Version: 1, Actor: creator,
	}); err == nil {
		t.Fatal("activate without break_glass must fail")
	}
	if _, err := svc.ActivateVersion(ctx, app.ActivateVersionInput{
		SecretID: secret.ID, Version: 1, BreakGlass: true, Actor: creator,
	}); err == nil {
		t.Fatal("activate without break_glass_reason must fail")
	}
	secret, err = svc.ActivateVersion(ctx, app.ActivateVersionInput{
		SecretID: secret.ID, Version: 1,
		BreakGlass: true, BreakGlassReason: "incident rollback drill", Actor: creator,
	})
	if err != nil {
		t.Fatalf("ActivateVersion: %v", err)
	}
	if secret.ActiveVersion == nil || *secret.ActiveVersion != 1 {
		t.Fatalf("active version after activate = %v", secret.ActiveVersion)
	}
}
