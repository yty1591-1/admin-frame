package domain_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

func TestSecretValidateAndTransitions(t *testing.T) {
	secret := domain.Secret{
		Env: "dev", Namespace: "platform", Name: "notification.smtp.password",
		Owner: "platform", Status: domain.SecretStatusDraft, Version: 1,
		RotateIntervalDays: 90,
	}
	if err := secret.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !secret.CanSubmit() || secret.CanApprove() || secret.CanRotate() {
		t.Fatalf("draft transitions unexpected")
	}
	secret.Status = domain.SecretStatusPendingReview
	if !secret.CanApprove() {
		t.Fatal("pending should allow approve")
	}
	secret.Status = domain.SecretStatusActive
	if !secret.CanRotate() || !secret.CanRevoke() {
		t.Fatal("active should allow rotate/revoke")
	}
}

func TestSecretGrantRejectsWildcard(t *testing.T) {
	grant := domain.SecretGrant{
		Env: "dev", Namespace: "platform", ServiceIdentity: "spiffe://cluster/ns/*",
		SecretID: 1, Permission: "resolve",
	}
	if err := grant.Validate(); err == nil {
		t.Fatal("expected wildcard rejection")
	}
	grant.ServiceIdentity = "spiffe://cluster/ns/biz-api"
	if err := grant.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	now := time.Now().UTC()
	if !grant.Active(now) {
		t.Fatal("expected active grant")
	}
}

func TestSecretVersionValidate(t *testing.T) {
	v := domain.SecretVersion{
		SecretID: 1, Version: 1,
		Ciphertext: []byte("c"), Nonce: make([]byte, 12), WrappedDEK: []byte("d"),
		KEKKeyID: "local/1", Algorithm: "aes-256-gcm", ValueHash: "0123456789abcdef",
		Status: domain.SecretVersionActive,
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
