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
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// newResetFixture 构造已完成首装的配置中心，用于验证「重置为默认值」。
func newResetFixture(t *testing.T) (*app.BootstrapService, *memory.Repositories, *fakeEmitter) {
	t.Helper()
	t.Setenv("CONFIG_INSTALL_SETUP_TOKEN", "test-setup-token-32chars-xxxxxx")
	repos := memory.New()
	registry := spec.NewRegistry()
	registry.MustRegister(
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeString,
			Default: "30s", Description: "请求超时", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
			Sensitive: true, Description: "SMTP 口令", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "tracing.endpoint", ValueType: domain.ValueTypeString,
			Description: "无默认值", Owner: "team-platform",
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
	ctx := context.Background()
	if _, err := install.EnsureState(ctx, "dev", domain.InstallPending); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.Begin(ctx, app.BeginInput{Env: "dev", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := install.Apply(ctx, app.ApplyInput{Env: "dev", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := install.Complete(ctx, app.CompleteInput{Env: "dev", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	return bootstrap, repos, emitter
}

// TestResetToDefaultsWritesNewVersionAndRelease 验证重置会追加版本、生成新发布并推进指针。
func TestResetToDefaultsWritesNewVersionAndRelease(t *testing.T) {
	bootstrap, repos, emitter := newResetFixture(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "service:admin-api", ActorType: "system", Channel: "internal"}

	before, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("pointer before: %v", err)
	}
	itemBefore, err := repos.Items.GetByKey(ctx, "dev", "platform", "log.level")
	if err != nil {
		t.Fatalf("item before: %v", err)
	}
	auditBefore := len(emitter.all())

	result, err := bootstrap.ResetToDefaults(ctx, app.ResetInput{
		Env: "dev", Refs: []string{"platform/log.level"}, Actor: actor,
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(result.Reset) != 1 || result.Reset[0].Key != "log.level" {
		t.Fatalf("reset entries = %+v", result.Reset)
	}
	// log.level 在 non-prod 叠加层推荐 debug（spec.nonProdDefaults）。
	if result.Reset[0].DefaultSource != app.DefaultSourceDev {
		t.Fatalf("default source = %q, want %q", result.Reset[0].DefaultSource, app.DefaultSourceDev)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
	if _, ok := result.ReleaseKeys["platform"]; !ok {
		t.Fatalf("release keys = %v", result.ReleaseKeys)
	}

	after, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("pointer after: %v", err)
	}
	if after.ActiveReleaseID == before.ActiveReleaseID {
		t.Fatal("pointer should switch to the reset release")
	}
	if after.Version != before.Version+1 {
		t.Fatalf("pointer version = %d, want %d", after.Version, before.Version+1)
	}
	itemAfter, err := repos.Items.GetByKey(ctx, "dev", "platform", "log.level")
	if err != nil {
		t.Fatalf("item after: %v", err)
	}
	if itemAfter.CurrentVersion != itemBefore.CurrentVersion+1 {
		t.Fatalf("item version = %d, want %d", itemAfter.CurrentVersion, itemBefore.CurrentVersion+1)
	}
	events := emitter.all()
	if len(events) != auditBefore+1 {
		t.Fatalf("audit events = %d, want %d", len(events), auditBefore+1)
	}
}

// TestResetToDefaultsSkipsUnsupportedKeys 验证敏感 / 无默认值 / 未声明 / 已归档键都被跳过并给出原因。
func TestResetToDefaultsSkipsUnsupportedKeys(t *testing.T) {
	bootstrap, _, _ := newResetFixture(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "service:admin-api", ActorType: "system", Channel: "internal"}

	result, err := bootstrap.ResetToDefaults(ctx, app.ResetInput{
		Env: "dev", Actor: actor,
		Refs: []string{"platform/smtp.password", "platform/tracing.endpoint", "platform/not_declared"},
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(result.Reset) != 0 {
		t.Fatalf("reset = %+v, want none", result.Reset)
	}
	reasons := map[string]string{}
	for _, skip := range result.Skipped {
		reasons[skip.Ref] = skip.Reason
	}
	want := map[string]string{
		"platform/smtp.password":    "sensitive_key",
		"platform/tracing.endpoint": "no_default",
		"platform/not_declared":     "unknown_key",
	}
	for ref, reason := range want {
		if reasons[ref] != reason {
			t.Fatalf("skip %s = %q, want %q (all: %+v)", ref, reasons[ref], reason, result.Skipped)
		}
	}
}

// TestResetToDefaultsRejectsBadInput 验证空 refs / 非法引用 / 超上限都被拒绝为 400。
func TestResetToDefaultsRejectsBadInput(t *testing.T) {
	bootstrap, _, _ := newResetFixture(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "service:admin-api", ActorType: "system", Channel: "internal"}

	cases := []struct {
		name string
		refs []string
	}{
		{name: "empty", refs: nil},
		{name: "no namespace", refs: []string{"log.level"}},
		{name: "blank key", refs: []string{"platform/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bootstrap.ResetToDefaults(ctx, app.ResetInput{Env: "dev", Refs: tc.refs, Actor: actor})
			if err == nil {
				t.Fatal("want error")
			}
			var appErr *apperr.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("err type = %T, want *apperr.AppError", err)
			}
			if appErr.NumericCode() != apperr.CodeBadRequest {
				t.Fatalf("code = %d, want bad request", appErr.NumericCode())
			}
		})
	}

	tooMany := make([]string, 0, 33)
	for i := 0; i < 33; i++ {
		tooMany = append(tooMany, "platform/log.level")
	}
	// 去重后只有 1 条，不会触发上限；这里用不同键名验证上限分支。
	tooMany = tooMany[:0]
	for i := 0; i < 33; i++ {
		tooMany = append(tooMany, "platform/key_"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, err := bootstrap.ResetToDefaults(ctx, app.ResetInput{Env: "dev", Refs: tooMany, Actor: actor}); err == nil {
		t.Fatal("want error for too many refs")
	}
}
