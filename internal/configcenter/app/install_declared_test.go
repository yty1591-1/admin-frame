package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestInstallRepairDeclaredKeys 验证运维声明的阻塞键（ADR-0018 待定 5）。
//
// 覆盖两件事：① 注册表算不出阻塞键时，声明白名单能破掉「gaps 为空 → 40952」死锁；
// ② 白名单仍然拒绝未声明键、敏感键、首装轮次，以及已有值的键（后者属正常变更，
// 应走配置审批流）。同时验证 apply 的 remaining 与 complete 门禁都认声明键，
// 避免「声明后 apply 什么也没做、complete 却放行」的假修复。
func TestInstallRepairDeclaredKeys(t *testing.T) {
	install, _, registry := newRepairServices(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	readyInstall(t, install, map[string]string{"http.base_url": "https://admin.example.com"})

	registry.MustRegister(domain.KeySpec{
		Namespace: "platform", Key: "tracing.endpoint", ValueType: domain.ValueTypeString,
		Description: "OTLP 上报地址", Owner: "team-platform",
	})
	registry.MustRegister(domain.KeySpec{
		Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
		Sensitive: true, Description: "SMTP 口令", Owner: "team-platform",
	})

	var appErr *apperr.AppError
	expectBadRequest := func(t *testing.T, err error, want string) {
		t.Helper()
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBadRequest {
			t.Fatalf("expected bad request %q, got %v", want, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected message %q, got %v", want, err)
		}
	}

	// 死锁现场：注册表没有 Required 缺键，但进程确实起不来。
	if gaps, err := install.Gaps(ctx, "prod"); err != nil || len(gaps) != 0 {
		t.Fatalf("gaps=%+v err=%v", gaps, err)
	}
	_, err := install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "repair", Actor: actor,
	})
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInstallCompleted {
		t.Fatalf("expected install completed without declared keys, got %v", err)
	}

	// 声明键的三种拒绝：未注册、敏感、首装轮次。
	_, err = install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "repair",
		DeclaredKeys: []string{"platform/not_declared"}, Actor: actor,
	})
	expectBadRequest(t, err, "not present in the config registry")

	_, err = install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "repair",
		DeclaredKeys: []string{"platform/smtp.password"}, Actor: actor,
	})
	expectBadRequest(t, err, "is sensitive")

	_, err = install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "first_install",
		DeclaredKeys: []string{"platform/tracing.endpoint"}, Actor: actor,
	})
	expectBadRequest(t, err, `declared_keys is only valid with reason "repair"`)

	// 正例：声明未进入快照的键 → 冻结进白名单。
	if _, err := install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-b", Reason: "repair",
		DeclaredKeys: []string{"platform/tracing.endpoint"}, Actor: actor,
	}); err != nil {
		t.Fatalf("begin with declared keys: %v", err)
	}
	state, err := install.Status(ctx, "prod")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if state.Status != domain.InstallInstalling || state.Mode != domain.InstallModeRepair {
		t.Fatalf("state=%+v", state)
	}
	if len(state.RepairKeys) != 1 || state.RepairKeys[0] != "platform/tracing.endpoint" {
		t.Fatalf("repair_keys=%+v", state.RepairKeys)
	}

	// 没提交值时声明键仍缺：apply 如实回报，complete 拒绝且不改状态。
	applied, err := install.Apply(ctx, app.ApplyInput{Env: "prod", Owner: "owner-b", Actor: actor})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied.RemainingGaps) != 1 ||
		applied.RemainingGaps[0].KeyRef() != "platform/tracing.endpoint" {
		t.Fatalf("remaining=%+v", applied.RemainingGaps)
	}
	_, err = install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-b", Actor: actor})
	if err == nil || !strings.Contains(err.Error(), "platform/tracing.endpoint") {
		t.Fatalf("complete should reject remaining declared key, got %v", err)
	}
	if after, statusErr := install.Status(ctx, "prod"); statusErr != nil ||
		after.Status != domain.InstallInstalling {
		t.Fatalf("complete 失败不应改变状态：%+v err=%v", after, statusErr)
	}

	// 补齐声明键后 complete 通过。
	applied, err = install.Apply(ctx, app.ApplyInput{
		Env: "prod", Owner: "owner-b", Actor: actor,
		Overrides: map[string]string{"tracing.endpoint": "https://otlp.example.com"},
	})
	if err != nil {
		t.Fatalf("apply declared value: %v", err)
	}
	if len(applied.RemainingGaps) != 0 {
		t.Fatalf("remaining=%+v", applied.RemainingGaps)
	}
	repaired, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-b", Actor: actor})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if repaired.Status != domain.InstallReady || repaired.Mode != domain.InstallModeRepair {
		t.Fatalf("state=%+v", repaired)
	}

	// 已有值的键不能借 repair 绕过审批。
	_, err = install.Begin(ctx, app.BeginInput{
		Env: "prod", Owner: "owner-c", Reason: "repair",
		DeclaredKeys: []string{"platform/log.level"}, Actor: actor,
	})
	expectBadRequest(t, err, "already have values in the active release")
}
