package app_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/pkg/envelope"
)

func TestEnsureAutoSecretsProdGeneratesPlaceholders(t *testing.T) {
	t.Setenv("CONFIG_BOOTSTRAP_GRANT_IDENTITY", "admin-api,biz-api")

	repos := memory.NewSecrets()
	kek, err := envelope.NewLocalKEK(bytes32(0x42), "local/test")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	svc, err := app.NewSecretService(repos.Secrets, repos.Grants, repos.Access, kek,
		&fakeEmitter{}, newIDs(), app.Options{
			Service: "config-api",
			Specs:   registry,
			Now:     func() time.Time { return testNow },
		})
	if err != nil {
		t.Fatal(err)
	}
	actor := app.Actor{SubjectID: "system:config-bootstrap", ActorType: "system", Channel: "admin"}
	if err := svc.EnsureAutoSecrets(context.Background(), domain.EnvProd, registry, actor); err != nil {
		t.Fatalf("EnsureAutoSecrets prod: %v", err)
	}

	smtp, err := repos.Secrets.GetByName(context.Background(), domain.EnvProd,
		spec.NamespaceNotification, "notification.smtp.password")
	if err != nil {
		t.Fatalf("smtp placeholder missing: %v", err)
	}
	if smtp.Status != domain.SecretStatusActive {
		t.Fatalf("smtp status=%s", smtp.Status)
	}
	if !strings.Contains(smtp.Description, "placeholder=true") {
		t.Fatalf("smtp description must mark placeholder: %q", smtp.Description)
	}

	if _, err := repos.Secrets.GetByName(context.Background(), domain.EnvProd,
		spec.NamespaceIdentity, "identity.saml.private_key"); err != nil {
		t.Fatalf("saml placeholder missing: %v", err)
	}
	items, err := svc.Resolve(context.Background(), app.ResolveInput{
		Env: domain.EnvProd, Namespace: spec.NamespaceIdentity,
		Names: []string{"identity.saml.private_key"}, ServiceIdentity: "admin-api",
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("resolve saml: %v %+v", err, items)
	}
	block, _ := pem.Decode([]byte(items[0].Value))
	if block == nil {
		t.Fatal("saml placeholder must be PEM")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		t.Fatalf("saml PEM parse: %v", err)
	}

	// 非 prod-like 不得为 AutoGenerate=false 生成。
	devRepos := memory.NewSecrets()
	devSvc, err := app.NewSecretService(devRepos.Secrets, devRepos.Grants, devRepos.Access, kek,
		&fakeEmitter{}, newIDs(), app.Options{
			Service: "config-api", Specs: registry,
			Now: func() time.Time { return testNow },
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := devSvc.EnsureAutoSecrets(context.Background(), domain.EnvDev, registry, actor); err != nil {
		t.Fatalf("EnsureAutoSecrets dev: %v", err)
	}
	if _, err := devRepos.Secrets.GetByName(context.Background(), domain.EnvDev,
		spec.NamespaceNotification, "notification.smtp.password"); err == nil {
		t.Fatal("dev must not auto-generate AutoGenerate=false secrets")
	}
}
