package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
)

// newInstallFixture 构造只含安装态所需的 Handler（内存仓储 + 必填键）。
func newInstallFixture(t *testing.T) *rest.InstallHandler {
	t.Helper()
	t.Setenv("CONFIG_INSTALL_SETUP_TOKEN", "test-setup-token-32chars-xxxxxx")
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "http.base_url", ValueType: domain.ValueTypeString,
			Required: true, Description: "对外基地址", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
		},
	)
	options := app.Options{Service: "config-api", Now: func() time.Time { return time.Unix(0, 0).UTC() }}
	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, quietEmitter{}, &seqIDs{}, options)
	if err != nil {
		t.Fatalf("NewBootstrapService() error = %v", err)
	}
	install, err := app.NewInstallService(app.InstallServiceConfig{
		Install: repos.Install, Pointers: repos.Pointers, Bootstrap: bootstrap,
		Specs: registry, Emitter: quietEmitter{}, IDs: &seqIDs{},
		Options:  options,
		SetupTok: os.Getenv("CONFIG_INSTALL_SETUP_TOKEN"),
	})
	if err != nil {
		t.Fatalf("NewInstallService() error = %v", err)
	}
	handler, err := rest.NewInstallHandler(rest.InstallHandlerConfig{Install: install, DeployEnv: "dev"})
	if err != nil {
		t.Fatalf("NewInstallHandler() error = %v", err)
	}
	if _, err := install.EnsureState(context.Background(), "dev", domain.InstallPending); err != nil {
		t.Fatalf("EnsureState() error = %v", err)
	}
	return handler
}

func TestInstallSchemaRequiresSetupToken(t *testing.T) {
	handler := newInstallFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/install/schema", http.NoBody)
	handler.Schema(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("缺失 token 应 401，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/internal/v1/install/schema", http.NoBody)
	request.Header.Set(app.SetupTokenHeader(), "wrong-token")
	handler.Schema(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", recorder.Code)
	}
}

func TestInstallSchemaReturnsFieldsAndBlocking(t *testing.T) {
	handler := newInstallFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/install/schema", http.NoBody)
	request.Header.Set(app.SetupTokenHeader(), "test-setup-token-32chars-xxxxxx")
	handler.Schema(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("schema 应 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Env          string `json:"env"`
			Mode         string `json:"mode"`
			BlockingKeys []struct {
				Namespace string `json:"namespace"`
				Key       string `json:"key"`
				Label     string `json:"label"`
				Reason    string `json:"reason"`
			} `json:"blocking_keys"`
			Fields []struct {
				Key     string `json:"key"`
				Widget  string `json:"widget"`
				Default struct {
					Value  string `json:"value"`
					Source string `json:"source"`
				} `json:"default"`
			} `json:"fields"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if env.Code != 0 || env.Data.Env != "dev" || env.Data.Mode != app.InstallModeFirstInstall {
		t.Fatalf("envelope=%+v", env)
	}
	if len(env.Data.BlockingKeys) != 1 || env.Data.BlockingKeys[0].Key != "http.base_url" {
		t.Fatalf("blocking=%+v", env.Data.BlockingKeys)
	}
	if env.Data.BlockingKeys[0].Label != "对外基地址" {
		t.Fatalf("label=%q", env.Data.BlockingKeys[0].Label)
	}
	if len(env.Data.Fields) != 2 {
		t.Fatalf("fields=%+v", env.Data.Fields)
	}
	for _, field := range env.Data.Fields {
		if field.Key == "log.level" && (field.Default.Value != "info" || field.Default.Source != app.DefaultSourceCode) {
			t.Fatalf("log.level default=%+v", field.Default)
		}
	}
}
