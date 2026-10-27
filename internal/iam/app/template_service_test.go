package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestTemplateInstantiateDriftAndSync 覆盖模板基线的复制、漂移识别与显式收敛。
func TestTemplateInstantiateDriftAndSync(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	template, err := f.templateSvc.CreateTemplate(ctx, app.CreateTemplateCommand{
		Channel:     domain.ChannelAdmin,
		Code:        "ops_template",
		Name:        "运维只读",
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if template.Version != 1 {
		t.Fatalf("created template version=%d, want 1", template.Version)
	}

	role, err := f.templateSvc.Instantiate(ctx, app.InstantiateTemplateCommand{
		TemplateID: template.ID,
		Channel:    domain.ChannelAdmin,
		Scope:      domain.RoleScopePlatform,
		Code:       "ops_reader",
		Name:       "运维只读实例",
		Actor:      "1002",
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if role.TemplateID != template.ID || role.TemplateVersion != 1 {
		t.Fatalf("template baseline=%q/%d", role.TemplateID, role.TemplateVersion)
	}
	if !role.Permissions.Contains(iam.PermRoleRead) {
		t.Fatalf("instantiated permissions=%v", role.PermissionCodes())
	}

	// 模板基线变更后，实例不会自动跟随，必须先由漂移扫描暴露影响面。
	if _, err := f.templateSvc.ReplaceTemplatePermissions(ctx, app.ReplaceTemplatePermissionsCommand{
		ID:          template.ID,
		Permissions: []iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite},
		Version:     template.Version,
		Actor:       "1001",
	}); err != nil {
		t.Fatalf("ReplaceTemplatePermissions: %v", err)
	}
	drift, err := f.templateSvc.Drift(ctx, template.ID)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drift) != 1 || drift[0].RoleID != role.ID || drift[0].TemplateVersion != 1 {
		t.Fatalf("drift=%+v", drift)
	}

	synced, err := f.templateSvc.SyncTemplate(ctx, app.SyncTemplateCommand{
		TemplateID: template.ID,
		Actor:      "1001",
		Reason:     "align with platform baseline",
	})
	if err != nil {
		t.Fatalf("SyncTemplate: %v", err)
	}
	if len(synced) != 1 {
		t.Fatalf("synced=%d, want 1", len(synced))
	}

	updated, err := f.roles.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.TemplateVersion != 2 {
		t.Fatalf("template_version=%d, want 2", updated.TemplateVersion)
	}
	if !updated.Permissions.Contains(iam.PermRoleWrite) {
		t.Fatalf("synced permissions=%v", updated.PermissionCodes())
	}
	drift, err = f.templateSvc.Drift(ctx, template.ID)
	if err != nil {
		t.Fatalf("Drift after sync: %v", err)
	}
	if len(drift) != 0 {
		t.Fatalf("drift after sync=%+v, want empty", drift)
	}

	// 停用模板只阻止新实例化，既有实例保留。
	disabled := "disabled"
	if _, err := f.templateSvc.UpdateTemplate(ctx, app.UpdateTemplateCommand{
		ID:      template.ID,
		Status:  &disabled,
		Version: 2,
		Actor:   "1001",
	}); err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if _, err := f.templateSvc.Instantiate(ctx, app.InstantiateTemplateCommand{
		TemplateID: template.ID,
		Channel:    domain.ChannelAdmin,
		Scope:      domain.RoleScopePlatform,
		Code:       "ops_reader_v2",
		Name:       "运维只读实例二",
		Actor:      "1002",
	}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want conflict for disabled template", err)
	}
}
