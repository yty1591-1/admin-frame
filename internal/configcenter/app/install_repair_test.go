package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// newRepairServices 构造带必填键的安装用例，并暴露注册表以便中途新增声明键。
func newRepairServices(t *testing.T) (*app.InstallService, *memory.Repositories, *spec.Registry) {
	t.Helper()
	t.Setenv("CONFIG_INSTALL_SETUP_TOKEN", "test-setup-token-32chars-xxxxxx")
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "http.base_url", ValueType: domain.ValueTypeString,
			Required: true, Description: "对外基地址（含协议）", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
		},
	)
	emitter := &fakeEmitter{}
	ids := newIDs()
	options := app.Options{Service: "config-api", Now: func() time.Time { return testNow }}
	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, ids, options)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	install, err := app.NewInstallService(app.InstallServiceConfig{
		Install: repos.Install, Pointers: repos.Pointers, Bootstrap: bootstrap,
		Specs: registry, Emitter: emitter, IDs: ids,
		Options:  options,
		SetupTok: os.Getenv("CONFIG_INSTALL_SETUP_TOKEN"),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := install.EnsureState(context.Background(), "prod", domain.InstallPending); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return install, repos, registry
}

// readyInstall 跑完首次安装；overrides 必须覆盖全部阻塞键。
func readyInstall(t *testing.T, install *app.InstallService, overrides map[string]string) {
	t.Helper()
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-a", Reason: "first_install", Actor: actor,
	}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := install.Apply(ctx, app.ApplyInput{
		Env: "prod", Owner: "owner-a", Actor: actor, Overrides: overrides,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// TestInstallCompleteRejectsRemainingGaps 验证 complete 前置：仍有阻塞键时不允许进入 ready。
func TestInstallCompleteRejectsRemainingGaps(t *testing.T) {
	install, _, _ := newRepairServices(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 不提交 http.base_url，播种只补有默认值的键，因此必填键仍缺。
	if _, err := install.Apply(ctx, app.ApplyInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	_, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-a", Actor: actor})
	if err == nil {
		t.Fatal("expected complete to fail while blocking keys remain")
	}
	if !strings.Contains(err.Error(), "platform/http.base_url") {
		t.Fatalf("error should name the blocking key: %v", err)
	}
	state, statusErr := install.Status(ctx, "prod")
	if statusErr != nil {
		t.Fatalf("status: %v", statusErr)
	}
	if state.Status != domain.InstallInstalling {
		t.Fatalf("complete 失败不应改变状态，actual=%s", state.Status)
	}
}

// TestInstallRepairWhitelist 验证 ready 后的 repair：冻结白名单、拒绝非白名单键、补齐全流程。
func TestInstallRepairWhitelist(t *testing.T) {
	install, _, registry := newRepairServices(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	readyInstall(t, install, map[string]string{"http.base_url": "https://admin.example.com"})

	if gaps, err := install.Gaps(ctx, "prod"); err != nil || len(gaps) != 0 {
		t.Fatalf("ready 后不应有阻塞键：gaps=%+v err=%v", gaps, err)
	}

	// 新增一个必填键但未进入快照：模拟升级后缺键。
	registry.MustRegister(domain.KeySpec{
		Namespace: "platform", Key: "http.metrics_url", ValueType: domain.ValueTypeString,
		Required: true, Description: "指标上报地址", Owner: "team-platform",
	})
	gaps, err := install.Gaps(ctx, "prod")
	if err != nil {
		t.Fatalf("gaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0].Key != "http.metrics_url" || gaps[0].Reason != "missing_in_snapshot" {
		t.Fatalf("gaps=%+v", gaps)
	}

	// 无阻塞键时 repair 直接被拒（40952）。
	if _, err := install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "repair", Actor: actor,
	}); err != nil {
		t.Fatalf("begin repair: %v", err)
	}

	state, err := install.Status(ctx, "prod")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state.Status != domain.InstallInstalling || state.Mode != domain.InstallModeRepair {
		t.Fatalf("state=%+v", state)
	}
	if len(state.RepairKeys) != 1 || state.RepairKeys[0] != "platform/http.metrics_url" {
		t.Fatalf("repair_keys=%+v", state.RepairKeys)
	}

	// 非白名单键（即使已声明且有默认值）必须被拒。
	_, err = install.Apply(ctx, app.ApplyInput{
		Env: "prod", Owner: "owner-b", Actor: actor,
		Overrides: map[string]string{"log.level": "debug"},
	})
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBadRequest {
		t.Fatalf("expected bad request, got %v", err)
	}
	if !strings.Contains(err.Error(), "repair only accepts frozen blocking keys") {
		t.Fatalf("unexpected message: %v", err)
	}
	if after, statusErr := install.Status(ctx, "prod"); statusErr != nil ||
		after.Status != domain.InstallInstalling {
		t.Fatalf("被拒的 apply 不应改变状态：%+v err=%v", after, statusErr)
	}

	// 白名单键通过，阻塞清零。
	applied, err := install.Apply(ctx, app.ApplyInput{
		Env: "prod", Owner: "owner-b", Actor: actor,
		Overrides: map[string]string{"http.metrics_url": "https://metrics.example.com"},
	})
	if err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	if len(applied.RemainingGaps) != 0 {
		t.Fatalf("remaining=%+v", applied.RemainingGaps)
	}

	repaired, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-b", Actor: actor})
	if err != nil {
		t.Fatalf("complete repair: %v", err)
	}
	if repaired.Status != domain.InstallReady || repaired.Mode != domain.InstallModeRepair {
		t.Fatalf("state=%+v", repaired)
	}
	if gaps, err := install.Gaps(ctx, "prod"); err != nil || len(gaps) != 0 {
		t.Fatalf("repair 后应无阻塞键：%+v err=%v", gaps, err)
	}

	// 修复完成后再次 repair 无人可修 → 40952。
	_, err = install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-c", Reason: "repair", Actor: actor})
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInstallCompleted {
		t.Fatalf("expected install completed, got %v", err)
	}
}
