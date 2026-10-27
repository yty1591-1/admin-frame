package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// TemplateService 是角色模板用例：平台定义基线，商户按需实例化。
//
// 模板与实例是复制关系：实例化时复制权限与数据范围，模板后续变更不会自动扩散，
// 必须显式调用 Sync 收敛漂移，避免平台改动无通知地扩大存量角色权限。
type TemplateService struct {
	templates domain.RoleTemplateRepository
	roles     *RoleService
	roleRepo  domain.RoleRepository
	recorder  ChangeRecorder
	uow       UnitOfWork
	log       *slog.Logger
	ids       id.Generator
	audit     auditBuilder
	limits    RoleLimits
}

// TemplateServiceConfig 是 TemplateService 的装配参数。
type TemplateServiceConfig struct {
	Templates  domain.RoleTemplateRepository
	Roles      *RoleService
	RoleRepo   domain.RoleRepository
	Recorder   ChangeRecorder
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Limits     RoleLimits
	Logger     *slog.Logger
}

// NewTemplateService 构造模板用例；缺失必要依赖时返回错误（fail-closed）。
func NewTemplateService(cfg TemplateServiceConfig) (*TemplateService, error) {
	if cfg.Templates == nil {
		return nil, apperr.Internal("iam role template repository is required", nil)
	}
	if cfg.Roles == nil || cfg.RoleRepo == nil {
		return nil, apperr.Internal("iam role service is required for role templates", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	if cfg.Limits.MaxPermissionsPerRole <= 0 {
		cfg.Limits.MaxPermissionsPerRole = 200
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &TemplateService{
		templates: cfg.Templates,
		roles:     cfg.Roles,
		roleRepo:  cfg.RoleRepo,
		recorder:  cfg.Recorder,
		uow:       uow,
		log:       logger,
		ids:       cfg.IDs,
		audit:     newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:    cfg.Limits,
	}, nil
}

// CreateTemplateCommand 是创建模板的入参。
type CreateTemplateCommand struct {
	Channel     domain.Channel
	Code        string
	Name        string
	Description string
	Permissions []iam.PermissionCode
	DataScopes  []domain.DataScope
	Actor       string
}

// CreateTemplate 创建模板并初始化权限与数据范围基线。
func (s *TemplateService) CreateTemplate(ctx context.Context, cmd CreateTemplateCommand) (*domain.RoleTemplate, error) {
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	if len(cmd.Permissions) > s.limits.MaxPermissionsPerRole {
		return nil, apperr.BadRequest("permissions exceed iam.limits.max_permissions_per_role")
	}
	required, err := validateGrant(cmd.Channel, cmd.Permissions)
	if err != nil {
		return nil, err
	}
	set, err := domain.NewPermissionSet(cmd.Permissions)
	if err != nil {
		return nil, err
	}
	scopes, err := normalizeDataScopes(cmd.Channel, cmd.DataScopes)
	if err != nil {
		return nil, err
	}
	templateID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate role template id", err)
	}
	template, err := domain.NewRoleTemplate(domain.NewRoleTemplateParams{
		ID:          templateID,
		Channel:     cmd.Channel,
		Code:        cmd.Code,
		Name:        cmd.Name,
		Description: cmd.Description,
		Actor:       cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := template.ReplacePermissions(set, cmd.Actor, template.Version); err != nil {
		return nil, err
	}
	if err := template.ReplaceDataScopes(scopes, cmd.Actor, template.Version); err != nil {
		return nil, err
	}
	// Replace* 各递增一次版本；创建时尚未对外暴露版本，归一回 1 以保持「创建即基线」。
	template.Version = 1
	if err := requireTemplateDataScopes(template, required); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.templates.Save(ctx, template, 0); err != nil {
			return err
		}
		return s.recordTemplate(ctx, template, opCreate, cmd.Actor, "")
	}); err != nil {
		return nil, err
	}
	return template, nil
}

// UpdateTemplateCommand 是修改模板名称 / 说明 / 状态的入参。
type UpdateTemplateCommand struct {
	ID          string
	Name        *string
	Description *string
	Status      *string
	Version     int64
	Actor       string
}

// UpdateTemplate 局部更新模板；编码与端别不可改。
func (s *TemplateService) UpdateTemplate(ctx context.Context, cmd UpdateTemplateCommand) (*domain.RoleTemplate, error) {
	if cmd.Name == nil && cmd.Description == nil && cmd.Status == nil {
		return nil, apperr.BadRequest("no fields to update")
	}
	template, err := s.loadMutableTemplate(ctx, cmd.ID, cmd.Version)
	if err != nil {
		return nil, err
	}
	operation := opUpdate
	if cmd.Name != nil || cmd.Description != nil {
		name := template.Name
		if cmd.Name != nil {
			name = *cmd.Name
		}
		description := template.Description
		if cmd.Description != nil {
			description = *cmd.Description
		}
		if err := template.UpdateDetails(name, description, cmd.Actor, template.Version); err != nil {
			return nil, err
		}
	}
	if cmd.Status != nil {
		status, statusErr := normalizeTemplateStatus(*cmd.Status)
		if statusErr != nil {
			return nil, statusErr
		}
		operation = opUpdate
		if status == domain.RoleTemplateDisabled {
			operation = opDisable
			if err := template.Disable(cmd.Actor, template.Version); err != nil {
				return nil, err
			}
		} else {
			operation = opEnable
			if err := template.Enable(cmd.Actor, template.Version); err != nil {
				return nil, err
			}
		}
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.templates.Save(ctx, template, cmd.Version); err != nil {
			return err
		}
		return s.recordTemplate(ctx, template, operation, cmd.Actor, "")
	}); err != nil {
		return nil, err
	}
	return template, nil
}

// ReplaceTemplatePermissionsCommand 是全量替换模板基线的入参。
type ReplaceTemplatePermissionsCommand struct {
	ID          string
	Permissions []iam.PermissionCode
	DataScopes  *[]domain.DataScope
	Version     int64
	Actor       string
}

// ReplaceTemplatePermissions 全量替换模板权限与（可选）数据范围，并递增模板版本。
func (s *TemplateService) ReplaceTemplatePermissions(ctx context.Context, cmd ReplaceTemplatePermissionsCommand) (*domain.RoleTemplate, error) {
	if len(cmd.Permissions) > s.limits.MaxPermissionsPerRole {
		return nil, apperr.BadRequest("permissions exceed iam.limits.max_permissions_per_role")
	}
	template, err := s.loadMutableTemplate(ctx, cmd.ID, cmd.Version)
	if err != nil {
		return nil, err
	}
	required, err := validateGrant(template.Channel, cmd.Permissions)
	if err != nil {
		return nil, err
	}
	set, err := domain.NewPermissionSet(cmd.Permissions)
	if err != nil {
		return nil, err
	}
	if err := template.ReplacePermissions(set, cmd.Actor, template.Version); err != nil {
		return nil, err
	}
	if cmd.DataScopes != nil {
		scopes, normalizeErr := normalizeDataScopes(template.Channel, *cmd.DataScopes)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if err := template.ReplaceDataScopes(scopes, cmd.Actor, template.Version); err != nil {
			return nil, err
		}
	}
	if err := requireTemplateDataScopes(template, required); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.templates.Save(ctx, template, cmd.Version); err != nil {
			return err
		}
		return s.recordTemplate(ctx, template, opReplacePermission, cmd.Actor, "")
	}); err != nil {
		return nil, err
	}
	return template, nil
}

// DeleteTemplateCommand 是软删模板的入参。
type DeleteTemplateCommand struct {
	ID      string
	Version int64
	Actor   string
	Reason  string
}

// DeleteTemplate 软删模板；已实例化角色保留，仅停止后续实例化与同步。
func (s *TemplateService) DeleteTemplate(ctx context.Context, cmd DeleteTemplateCommand) error {
	template, err := s.loadMutableTemplate(ctx, cmd.ID, cmd.Version)
	if err != nil {
		return err
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return apperr.BadRequest("reason is required")
	}
	if err := template.Delete(cmd.Actor, cmd.Version); err != nil {
		return err
	}
	return s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.templates.Save(ctx, template, cmd.Version); err != nil {
			return err
		}
		return s.recordTemplate(ctx, template, opDelete, cmd.Actor, cmd.Reason)
	})
}

// GetTemplate 按 ID 加载模板并校验端别隔离。
func (s *TemplateService) GetTemplate(ctx context.Context, templateID string, channel domain.Channel) (*domain.RoleTemplate, error) {
	template, err := s.templates.FindByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if template.IsDeleted() {
		return nil, apperr.NotFound("role template not found")
	}
	if channel != "" && template.Channel != channel {
		return nil, apperr.NotFound("role template not found")
	}
	return template, nil
}

// ListTemplates 分页列举模板；Limit 上限 100（API-05）。
func (s *TemplateService) ListTemplates(ctx context.Context, filter domain.RoleTemplateFilter) ([]*domain.RoleTemplate, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		return nil, apperr.BadRequest("page_size exceeds maximum of 100")
	}
	return s.templates.List(ctx, filter)
}

// CountTemplates 返回与 ListTemplates 同条件的模板总数。
func (s *TemplateService) CountTemplates(ctx context.Context, filter domain.RoleTemplateFilter) (int, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		return 0, apperr.BadRequest("page_size exceeds maximum of 100")
	}
	return s.templates.Count(ctx, filter)
}

// InstantiateTemplateCommand 是按模板创建角色的入参。
type InstantiateTemplateCommand struct {
	TemplateID string
	Channel    domain.Channel
	Scope      domain.RoleScopeType
	MerchantID string
	Code       string
	Name       string
	Actor      string
	Reason     string
}

// Instantiate 把模板基线复制为一个新角色，并记录 template_id / template_version。
func (s *TemplateService) Instantiate(ctx context.Context, cmd InstantiateTemplateCommand) (*domain.Role, error) {
	template, err := s.templates.FindByID(ctx, cmd.TemplateID)
	if err != nil {
		return nil, err
	}
	if !template.IsActive() {
		return nil, apperr.Conflict("role template is not active")
	}
	if template.Channel != cmd.Channel {
		return nil, apperr.BadRequest("role template channel does not match request channel")
	}
	scopes := template.SortedDataScopes()
	return s.roles.CreateRole(ctx, CreateRoleCommand{
		Channel:         cmd.Channel,
		Scope:           cmd.Scope,
		MerchantID:      cmd.MerchantID,
		Code:            cmd.Code,
		Name:            cmd.Name,
		Description:     template.Description,
		Permissions:     template.PermissionCodes(),
		DataScopes:      scopes,
		TemplateID:      template.ID,
		TemplateVersion: template.Version,
		Actor:           cmd.Actor,
	})
}

// TemplateDrift 描述一个与模板基线不一致的角色实例。
type TemplateDrift struct {
	RoleID          string
	RoleCode        string
	RoleVersion     int64
	TemplateVersion int64
}

// Drift 返回模板下版本落后于模板基线的实例，供显式同步前预览影响面。
func (s *TemplateService) Drift(ctx context.Context, templateID string) ([]TemplateDrift, error) {
	template, err := s.templates.FindByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	roles, err := s.roleRepo.ListByTemplate(ctx, template.ID)
	if err != nil {
		return nil, err
	}
	out := make([]TemplateDrift, 0, len(roles))
	for _, role := range roles {
		if role.TemplateVersion >= template.Version {
			continue
		}
		out = append(out, TemplateDrift{
			RoleID:          role.ID,
			RoleCode:        role.Code,
			RoleVersion:     role.Version,
			TemplateVersion: role.TemplateVersion,
		})
	}
	return out, nil
}

// SyncTemplateCommand 是把模板基线同步到漂移实例的入参。
type SyncTemplateCommand struct {
	TemplateID string
	Actor      string
	Reason     string
	// DryRun 为 true 时只返回待同步实例，不写入。
	DryRun bool
}

// SyncTemplate 把模板当前权限与数据范围复制到全部漂移实例。
//
// 不修改未漂移实例：同步是显式收敛动作，重复执行应保持幂等，
// 且不应把「平台的当前意图」无差别覆盖到已人工调整过的角色。
func (s *TemplateService) SyncTemplate(ctx context.Context, cmd SyncTemplateCommand) ([]TemplateDrift, error) {
	template, err := s.templates.FindByID(ctx, cmd.TemplateID)
	if err != nil {
		return nil, err
	}
	if template.IsDeleted() {
		return nil, apperr.NotFound("role template not found")
	}
	drift, err := s.Drift(ctx, template.ID)
	if err != nil {
		return nil, err
	}
	if cmd.DryRun || len(drift) == 0 {
		return drift, nil
	}
	for _, item := range drift {
		role, loadErr := s.roleRepo.FindByID(ctx, item.RoleID)
		if loadErr != nil {
			return nil, loadErr
		}
		scopes := template.SortedDataScopes()
		updated, _, replaceErr := s.roles.ReplacePermissions(ctx, ReplacePermissionsCommand{
			ID:          role.ID,
			Permissions: template.PermissionCodes(),
			DataScopes:  &scopes,
			Version:     role.Version,
			Actor:       cmd.Actor,
			Reason:      cmd.Reason,
		})
		if replaceErr != nil {
			return nil, replaceErr
		}
		// 记录实例基线版本，避免下一次同步重复命中。
		expected := updated.Version
		updated.TemplateVersion = template.Version
		updated.UpdatedBy = cmd.Actor
		updated.UpdatedAt = time.Now().UTC()
		updated.Version++
		if saveErr := s.roleRepo.Save(ctx, updated, expected); saveErr != nil {
			return nil, saveErr
		}
	}
	return drift, nil
}

// loadMutableTemplate 加载未删除模板并校验乐观锁版本。
func (s *TemplateService) loadMutableTemplate(ctx context.Context, templateID string, version int64) (*domain.RoleTemplate, error) {
	if strings.TrimSpace(templateID) == "" {
		return nil, apperr.BadRequest("template id is required")
	}
	template, err := s.templates.FindByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if template.IsDeleted() {
		return nil, apperr.NotFound("role template not found")
	}
	if template.Version != version {
		return nil, apperr.Conflict("role template version conflict")
	}
	return template, nil
}

// recordTemplate 写入模板变更审计（action=rbac.role_change，resource_type=iam_role_template）。
func (s *TemplateService) recordTemplate(ctx context.Context, template *domain.RoleTemplate, operation, actor, reason string) error {
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACRoleChange,
		actor:        actor,
		channel:      string(template.Channel),
		resourceType: ResourceTypeRoleTemplate,
		resourceID:   template.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       reason,
		attrs: map[string]string{
			attrOperation: operation,
			attrRoleCode:  template.Code,
			attrRoleID:    template.ID,
		},
	})
}

// normalizeTemplateStatus 解析模板状态字符串。
func normalizeTemplateStatus(status string) (domain.RoleTemplateStatus, error) {
	value := domain.RoleTemplateStatus(strings.TrimSpace(status))
	if err := value.Validate(); err != nil {
		return "", err
	}
	return value, nil
}

// requireTemplateDataScopes 校验 data_bearing 权限在模板基线中已配置数据范围。
func requireTemplateDataScopes(template *domain.RoleTemplate, required []string) error {
	probe := &domain.Role{
		Channel:    template.Channel,
		DataScopes: template.DataScopes,
	}
	if missing := probe.MissingDataScopes(required); len(missing) > 0 {
		return apperr.BadRequest("missing data scope for resources: " + strings.Join(missing, ", "))
	}
	return nil
}
