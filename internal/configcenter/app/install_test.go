package app_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestInstallStateMachine(t *testing.T) {
	t.Setenv("CONFIG_INSTALL_SETUP_TOKEN", "test-setup-token-32chars-xxxxxx")
	repos := memory.New()
	registry := testSpecs(t)
	emitter := &fakeEmitter{}
	ids := newIDs()
	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, ids, app.Options{Service: "config-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	install, err := app.NewInstallService(app.InstallServiceConfig{
		Install: repos.Install, Pointers: repos.Pointers, Bootstrap: bootstrap,
		Specs: registry, Emitter: emitter, IDs: ids,
		Options:  app.Options{Service: "config-api", Now: func() time.Time { return testNow }},
		SetupTok: os.Getenv("CONFIG_INSTALL_SETUP_TOKEN"),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.EnsureState(ctx, "prod", domain.InstallPending); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := install.RequireSetupToken("wrong"); err == nil {
		t.Fatal("expected unauthorized")
	}
	if err := install.RequireSetupToken("test-setup-token-32chars-xxxxxx"); err != nil {
		t.Fatalf("token: %v", err)
	}
	begun, err := install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-a", Actor: actor})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if begun.Status != domain.InstallInstalling || begun.Owner != "owner-a" {
		t.Fatalf("begun=%+v", begun)
	}
	_, err = install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-b", Actor: actor})
	if err == nil {
		t.Fatal("expected in-progress conflict")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInstallInProgress {
		t.Fatalf("code=%v err=%v", appErr, err)
	}
	applied, err := install.Apply(ctx, app.ApplyInput{
		Env: "prod", Owner: "owner-a", Actor: actor,
		Overrides: map[string]string{"http.timeout": "20s"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.ItemCount <= 0 {
		t.Fatalf("expected seeded items, got %+v", applied)
	}
	ready, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-a", Actor: actor})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if ready.Status != domain.InstallReady {
		t.Fatalf("status=%s", ready.Status)
	}
	_, err = install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-a", Actor: actor})
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInstallCompleted {
		t.Fatalf("expected completed, got %v", err)
	}
}
