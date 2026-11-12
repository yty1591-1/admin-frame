package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

var testActor = app.Actor{SubjectID: "admin-1", ActorType: "user", Channel: "admin", TraceID: "trace-1"}

func TestCreateItemWithValue(t *testing.T) {
	service, repos, emitter := newItemService(t)
	ctx := context.Background()
	value := "30s"

	item, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: &value, Actor: testActor,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if item.CurrentVersion != 1 || item.Version != 1 || item.ValueType != domain.ValueTypeDuration {
		t.Fatalf("item head mismatch: %+v", item)
	}
	if item.Owner != "team-platform" || item.Description != "HTTP 超时" {
		t.Fatalf("spec defaults not applied: %+v", item)
	}

	stored, err := repos.Items.GetByKey(ctx, "dev", "platform", "http.timeout")
	if err != nil || stored.CurrentVersion != 1 {
		t.Fatalf("stored item mismatch: %v %+v", err, stored)
	}
	versions, total, err := repos.Items.ListVersions(ctx, item.ID, 10, 0)
	if err != nil || total != 1 || versions[0].ValueText != "30s" {
		t.Fatalf("versions mismatch: %v total=%d %+v", err, total, versions)
	}
	if versions[0].ValueHash != domain.HashValue("30s") {
		t.Fatalf("value hash mismatch: %s", versions[0].ValueHash)
	}

	events := emitter.all()
	if len(events) != 1 || events[0].Action != audit.ActionConfigChange ||
		events[0].ResourceID != "dev/platform/http.timeout" || events[0].Reason != "create" {
		t.Fatalf("unexpected audit events: %+v", events)
	}
}

func TestCreateItemRejectsUndeclaredAndInvalid(t *testing.T) {
	service, _, _ := newItemService(t)
	ctx := context.Background()

	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.unknown", Actor: testActor,
	}); err == nil {
		t.Fatal("undeclared key must be rejected")
	}

	invalidEnum := "trace"
	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "log.level", Value: &invalidEnum, Actor: testActor,
	}); err == nil {
		t.Fatal("enum violation must be rejected")
	}

	outOfRange := "99"
	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.retries", Value: &outOfRange, Actor: testActor,
	}); err == nil {
		t.Fatal("range violation must be rejected")
	}

	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Actor: app.Actor{},
	}); err == nil {
		t.Fatal("missing actor must be rejected")
	}
}

func TestCreateItemSensitive(t *testing.T) {
	service, repos, _ := newItemService(t)
	ctx := context.Background()

	item, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "smtp.password", Actor: testActor,
	})
	if err != nil {
		t.Fatalf("sensitive item metadata should be created without value: %v", err)
	}
	if !item.Sensitive || item.CurrentVersion != 0 {
		t.Fatalf("sensitive item mismatch: %+v", item)
	}

	secret := "p@ssw0rd"
	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "smtp.password", Value: &secret, Actor: testActor,
	}); err == nil {
		t.Fatal("sensitive value must be rejected in M1")
	}

	if _, err := service.UpdateValue(ctx, app.UpdateValueInput{
		Env: "dev", Namespace: "platform", Key: "smtp.password", Value: secret, Actor: testActor,
	}); err == nil {
		t.Fatal("sensitive value update must be rejected in M1")
	}

	if _, _, err := repos.Items.ListVersions(ctx, item.ID, 10, 0); err != nil {
		t.Fatalf("list versions: %v", err)
	}
}

func TestUpdateValueAppendsVersionAndUsesCAS(t *testing.T) {
	service, repos, emitter := newItemService(t)
	ctx := context.Background()

	first := "10s"
	item, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: &first, Actor: testActor,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	updated, err := service.UpdateValue(ctx, app.UpdateValueInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: "20s",
		ChangeSummary: "调大超时", Actor: testActor,
	})
	if err != nil {
		t.Fatalf("update value: %v", err)
	}
	if updated.CurrentVersion != 2 || updated.Version != 2 {
		t.Fatalf("item version mismatch: %+v", updated)
	}

	versions, total, err := repos.Items.ListVersions(ctx, item.ID, 10, 0)
	if err != nil || total != 2 {
		t.Fatalf("versions mismatch: %v total=%d", err, total)
	}
	if versions[1].ValueText != "20s" || versions[1].ChangeSummary != "调大超时" {
		t.Fatalf("version 2 mismatch: %+v", versions[1])
	}

	// 陈旧版本号：追加版本后 CAS 失败。
	stale := item.Version
	if _, err := service.UpdateValue(ctx, app.UpdateValueInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: "30s",
		ExpectedVersion: &stale, Actor: testActor,
	}); err == nil {
		t.Fatal("stale expected version must fail")
	}

	events := emitter.all()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(events))
	}
	if events[1].Reason != "update_value" {
		t.Fatalf("unexpected audit reason: %s", events[1].Reason)
	}
	if _, ok := events[1].Attrs["value_hash"]; !ok {
		t.Fatal("audit must record value hash instead of raw value")
	}
	if _, ok := events[1].Attrs["value"]; ok {
		t.Fatal("audit must not record raw config value")
	}
}

func TestTransitionItemStateMachine(t *testing.T) {
	service, _, _ := newItemService(t)
	ctx := context.Background()
	value := "10s"
	if _, err := service.CreateItem(ctx, app.CreateItemInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: &value, Actor: testActor,
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	activated, err := service.Transition(ctx, app.TransitionInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout",
		To: domain.ItemStatusActive, Actor: testActor,
	})
	if err != nil || activated.Status != domain.ItemStatusActive {
		t.Fatalf("activate: %v %+v", err, activated)
	}

	archived, err := service.Transition(ctx, app.TransitionInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout",
		To: domain.ItemStatusArchived, Actor: testActor,
	})
	if err != nil || archived.Status != domain.ItemStatusArchived {
		t.Fatalf("archive: %v %+v", err, archived)
	}

	if _, err := service.Transition(ctx, app.TransitionInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout",
		To: domain.ItemStatusActive, Actor: testActor,
	}); err == nil {
		t.Fatal("archived item must be terminal")
	}

	if _, err := service.UpdateValue(ctx, app.UpdateValueInput{
		Env: "dev", Namespace: "platform", Key: "http.timeout", Value: "40s", Actor: testActor,
	}); err == nil {
		t.Fatal("archived item must not accept new values")
	}
}
