package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
)

// newResetFixture 构造已完成播种的配置中心与启动自愈 Handler。
func newResetFixture(t *testing.T) *rest.ResetDefaultsHandler {
	t.Helper()
	t.Setenv("CONFIG_API_MTLS", "optional")
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
		},
	)
	emitter := quietEmitter{}
	ids := &seqIDs{next: 700000}
	options := app.Options{Service: "config-api", Now: func() time.Time {
		return time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	}}
	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, ids, options)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	install, err := app.NewInstallService(app.InstallServiceConfig{
		Install: repos.Install, Pointers: repos.Pointers, Bootstrap: bootstrap,
		Specs: registry, Emitter: emitter, IDs: ids,
		Options: options, SetupTok: "test-setup-token-32chars-xxxxxx",
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	ctx := context.Background()
	if _, err := install.EnsureState(ctx, "dev", domain.InstallPending); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	for _, step := range []func() error{
		func() error {
			_, err := install.Begin(ctx, app.BeginInput{Env: "dev", Owner: "owner-a", Actor: actor})
			return err
		},
		func() error {
			_, err := install.Apply(ctx, app.ApplyInput{Env: "dev", Owner: "owner-a", Actor: actor})
			return err
		},
		func() error {
			_, err := install.Complete(ctx, app.CompleteInput{Env: "dev", Owner: "owner-a", Actor: actor})
			return err
		},
	} {
		if err := step(); err != nil {
			t.Fatalf("install step: %v", err)
		}
	}
	handler, err := rest.NewResetDefaultsHandler(rest.ResetDefaultsHandlerConfig{
		Reset: bootstrap, DeployEnv: "dev",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return handler
}

// TestResetDefaultsRequiresServiceIdentity 验证缺少服务身份时拒绝。
func TestResetDefaultsRequiresServiceIdentity(t *testing.T) {
	handler := newResetFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/configs/repair/reset-defaults",
		strings.NewReader(`{"refs":["platform/log.level"]}`))
	handler.ResetDefaults(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 401/403", rec.Code)
	}
}

// TestResetDefaultsResetsDeclaredKey 验证带服务身份时可重置，并返回 release_keys。
func TestResetDefaultsResetsDeclaredKey(t *testing.T) {
	handler := newResetFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/configs/repair/reset-defaults",
		strings.NewReader(`{"env":"dev","refs":["platform/log.level"]}`))
	req.Header.Set("X-Service-Identity", "admin-api")
	handler.ResetDefaults(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Reset       []map[string]any  `json:"reset"`
			ReleaseKeys map[string]string `json:"release_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != 0 {
		t.Fatalf("code = %d", envelope.Code)
	}
	if len(envelope.Data.Reset) != 1 {
		t.Fatalf("reset = %+v", envelope.Data.Reset)
	}
	if envelope.Data.ReleaseKeys["platform"] == "" {
		t.Fatalf("release keys = %+v", envelope.Data.ReleaseKeys)
	}
}

// TestResetDefaultsRejectsEmptyRefs 验证空 refs 返回 400。
func TestResetDefaultsRejectsEmptyRefs(t *testing.T) {
	handler := newResetFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/configs/repair/reset-defaults",
		strings.NewReader(`{"refs":[]}`))
	req.Header.Set("X-Service-Identity", "admin-api")
	handler.ResetDefaults(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
