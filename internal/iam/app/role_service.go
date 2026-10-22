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

// SnapshotInvalidator 在授权变更后主动失效主体快照。
//
// 失效失败不阻断业务（L1/L2 TTL 兜底），但必须记日志与指标（docs/rbac-iam.md §8.4）。
type SnapshotInvalidator interface {
	InvalidateSubject(ctx context.Context, channel, merchantID, subjectType, subjectID string) error
}

// 角色变更操作标记，写入审计属性 operation。
const (
	opCreate            = "create"
	opUpdate            = "update"
	opEnable            = "enable"
	opDisable           = "disable"
	opDelete            = "delete"
	opClone             = "clone"
	opReplacePermission = "replace_permissions"
	opBind              = "bind"
	opUnbind            = "unbind"
)

// RoleService 是角色与授权管理用例。
//
// 端别隔离不在此处推断：调用方（transport）必须显式传入 Channel / MerchantID，
// 且 B 端 MerchantID 只能来自 Principal（SEC-14）。
type RoleService struct {
	roles       domain.RoleRepository
	bindings    domain.BindingRepository
	recorder    ChangeRecorder
	invalidator SnapshotInvalidator
	uow         UnitOfWork
	log         *slog.Logger
	ids         id.Generator
	audit       auditBuilder
	limits      RoleLimits
}

// RoleLimits 是角色规模上限，超限返回 40000 而不是静默截断。
type RoleLimits struct {
	// MaxPermissionsPerRole 单角色权限点数上限。
	MaxPermissionsPerRole int
	// MaxCustomRolesPerMerchant B 端单商户自定义角色数上限；≤0 表示不限制（仅 A 端）。
	MaxCustomRolesPerMerchant int
	// RequireReason 破坏性操作是否强制填写原因。
	RequireReason bool
}

// RoleServiceConfig 是 RoleService 的装配参数。
type RoleServiceConfig struct {
	Roles       domain.RoleRepository
	Bindings    domain.BindingRepository
	Recorder    ChangeRecorder
	Invalidator SnapshotInvalidator
	// UnitOfWork 让「业务写入 + 审计事件」同事务提交；nil 时退化为无事务（内存仓储）。
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Limits     RoleLimits
	Logger     *slog.Logger
}

// NewRoleService 构造角色管理用例；缺失必要依赖时返回错误（fail-closed）。
func NewRoleService(cfg RoleServiceConfig) (*RoleService, error) {
	if cfg.Roles == nil {
		return nil, apperr.Internal("iam role repository is required", nil)
	}
	if cfg.Bindings == nil {
		return nil, apperr.Internal("iam binding repository is required", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		cfg.IDs = id.MustSnowflake(id.Options{NodeID: 1})
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
	return &RoleService{
		roles:       cfg.Roles,
		bindings:    cfg.Bindings,
		recorder:    cfg.Recorder,
		invalidator: cfg.Invalidator,
		uow:         uow,
		log:         logger,
		ids:         cfg.IDs,
		audit:       newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:      cfg.Limits,
	}, nil
}

// CreateRoleCommand 是创建角色的入参。
type CreateRoleCommand struct {
	Channel     domain.Channel
	Scope       domain.RoleScopeType
	MerchantID  string
	Code        string
	Name        string
	Description string
	Permissions []iam.PermissionCode
	DataScopes  []domain.DataScope
	Actor       string
}

// CreateRole 创建处于 active 状态、version=1 的角色，并初始化权限与数据范围。
func (s *RoleService) CreateRole(ctx context.Context, cmd CreateRoleCommand) (*domain.Role, error) {
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
	if err := s.ensureMerchantQuota(ctx, cmd.Scope, cmd.MerchantID); err != nil {
		return nil, err
	}

	roleID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate role id", err)
	}
	role, err := domain.NewRole(domain.NewRoleParams{
		ID:          roleID,
		Channel:     cmd.Channel,
		Scope:       cmd.Scope,
		MerchantID:  cmd.MerchantID,
		Code:        cmd.Code,
		Name:        cmd.Name,
		Description: cmd.Description,
		Actor:       cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := role.Initialize(set, scopes); err != nil {
		return nil, err
	}
	if err := requireDataScopes(role, required); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Save(ctx, role, 0); err != nil {
			return err
		}
		return s.recordRole(ctx, role, opCreate, cmd.Actor, "", map[string]string{
			attrAdded: codesAttr(rolePermissionStrings(role)),
		})
	}); err != nil {
		return nil, err
	}
	return role, nil
}

// ensureMerchantQuota 校验 B 端商户自定义角色配额。
//
// 只统计该商户未软删的 merchant 角色：平台角色、其它商户与已删除角色不占额度。
// Count 与 Save 不在同一事务内，并发创建可能短暂超出上限，因此该配额是
// 防滥用的软上限（硬约束仍是唯一键与审计）；需要严格配额时应加租户级行锁。
func (s *RoleService) ensureMerchantQuota(ctx context.Context, scope domain.RoleScopeType, merchantID string) error {
	if s.limits.MaxCustomRolesPerMerchant <= 0 || scope != domain.RoleScopeMerchant {
		return nil
	}
	if merchantID == "" {
		return apperr.BadRequest("merchant id is required")
	}
	total, err := s.roles.Count(ctx, domain.RoleFilter{
		Channel:    domain.ChannelBiz,
		MerchantID: merchantID,
	})
	if err != nil {
		return err
	}
	if total >= s.limits.MaxCustomRolesPerMerchant {
		return apperr.BadRequest("merchant custom roles exceed iam.limits.max_custom_roles_per_merchant")
	}
	return nil
}

// LoadByCode 按端别 + 商户 + 编码加载未删除角色。
//
// 供破玻璃执行器定位系统内置角色；跨端 / 跨商户一律 NotFound（SEC-14）。
func (s *RoleService) LoadByCode(
	ctx context.Context,
	channel domain.Channel,
	merchantID string,
	code string,
) (*domain.Role, error) {
	role, err := s.roles.FindByCode(ctx, channel, merchantID, code)
	if err != nil {
		return nil, err
	}
	if role.IsDeleted() || role.Channel != channel || role.MerchantID != merchantID {
		return nil, apperr.NotFound("role not found")
	}
	return role, nil
}

// LoadForChange 按 ID 加载角色并校验端别，供审批执行器在应用前重新校验。
//
// 审批通过到实际应用之间可能隔数小时：角色可能已被停用、删除或改版，
// 因此应用时必须重新加载当前状态，而不是信任载荷里的版本号。
func (s *RoleService) LoadForChange(ctx context.Context, roleID string, channel domain.Channel) (*domain.Role, error) {
	role, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if role.IsDeleted() {
		return nil, apperr.NotFound("role not found")
	}
	if channel != "" && role.Channel != channel {
		return nil, apperr.NotFound("role not found")
	}
	return role, nil
}

// UpdateRoleCommand 是 PATCH 角色的入参；nil 字段表示不修改。
type UpdateRoleCommand struct {
	ID          string
	Name        *string
	Description *string
	Status      *string
	Version     int64
	Actor       string
	Reason      string
}

// UpdateRole 局部更新名称、说明与状态；code / channel / merchant 不可改。
func (s *RoleService) UpdateRole(ctx context.Context, cmd UpdateRoleCommand) (*domain.Role, error) {
	if cmd.Name == nil && cmd.Description == nil && cmd.Status == nil {
		return nil, apperr.BadRequest("no fields to update")
	}
	role, err := roleForGrant(ctx, s.roles, cmd.ID, cmd.Version)
	if err != nil {
		return nil, err
	}
	operation := opUpdate
	if cmd.Name != nil || cmd.Description != nil {
		name := role.Name
		if cmd.Name != nil {
			name = *cmd.Name
		}
		description := role.Description
		if cmd.Description != nil {
			description = *cmd.Description
		}
		if err := role.UpdateDetails(name, description, cmd.Actor, role.Version); err != nil {
			return nil, err
		}
	}
	statusChanged := false
	if cmd.Status != nil {
		status, err := normalizeStatus(*cmd.Status)
		if err != nil {
			return nil, err
		}
		switch status {
		case domain.RoleStatusDisabled:
			// 停用角色等价于批量回收权限，按高风险变更处理。
			if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
				return nil, apperr.BadRequest("reason is required")
			}
			operation = opDisable
			if err := role.Disable(cmd.Actor, role.Version); err != nil {
				return nil, err
			}
			statusChanged = true
		case domain.RoleStatusActive:
			operation = opEnable
			if err := role.Enable(cmd.Actor, role.Version); err != nil {
				return nil, err
			}
			statusChanged = true
		}
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Save(ctx, role, cmd.Version); err != nil {
			return err
		}
		return s.recordRole(ctx, role, operation, cmd.Actor, cmd.Reason, map[string]string{
			attrOperation: operation,
		})
	}); err != nil {
		return nil, err
	}
	if statusChanged {
		// 启停改变快照内容，主动失效已绑定主体，避免等 TTL 才生效。
		s.invalidateRoleSubjects(ctx, role)
	}
	return role, nil
}

// ReplacePermissionsCommand 是 PUT 权限的入参。
type ReplacePermissionsCommand struct {
	ID          string
	Permissions []iam.PermissionCode
	DataScopes  *[]domain.DataScope
	Version     int64
	Actor       string
	Reason      string
}

// ReplacePermissions 全量替换角色权限与（可选）数据范围，并返回最新角色。
//
// 差量写入审计：只记录 added / removed，避免审计日志变成全量权限快照。
func (s *RoleService) ReplacePermissions(ctx context.Context, cmd ReplacePermissionsCommand) (*domain.Role, domain.PermissionChange, error) {
	if len(cmd.Permissions) > s.limits.MaxPermissionsPerRole {
		return nil, domain.PermissionChange{}, apperr.BadRequest("permissions exceed iam.limits.max_permissions_per_role")
	}
	role, err := roleForGrant(ctx, s.roles, cmd.ID, cmd.Version)
	if err != nil {
		return nil, domain.PermissionChange{}, err
	}
	if role.Type == domain.RoleTypeBreakglass {
		// 破玻璃角色不持有权限点：超级权限只由快照在审批有效期内展开，
		// 允许写入权限点会把临时超级权限变成永久授权。
		return nil, domain.PermissionChange{}, apperr.Forbidden("breakglass role permissions are managed by the approval workflow")
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return nil, domain.PermissionChange{}, apperr.BadRequest("reason is required")
	}
	required, err := validateGrant(role.Channel, cmd.Permissions)
	if err != nil {
		return nil, domain.PermissionChange{}, err
	}
	set, err := domain.NewPermissionSet(cmd.Permissions)
	if err != nil {
		return nil, domain.PermissionChange{}, err
	}
	change, err := role.ReplacePermissions(set, cmd.Actor, cmd.Version)
	if err != nil {
		return nil, domain.PermissionChange{}, err
	}
	if cmd.DataScopes != nil {
		scopes, normalizeErr := normalizeDataScopes(role.Channel, *cmd.DataScopes)
		if normalizeErr != nil {
			return nil, domain.PermissionChange{}, normalizeErr
		}
		if err := role.ReplaceDataScopes(scopes, cmd.Actor, role.Version); err != nil {
			return nil, domain.PermissionChange{}, err
		}
	}
	if err := requireDataScopes(role, required); err != nil {
		return nil, domain.PermissionChange{}, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Save(ctx, role, cmd.Version); err != nil {
			return err
		}
		return s.recordRole(ctx, role, opReplacePermission, cmd.Actor, cmd.Reason, map[string]string{
			attrAdded:   codesAttr(permissionCodeStrings(change.Added)),
			attrRemoved: codesAttr(permissionCodeStrings(change.Removed)),
		})
	}); err != nil {
		return nil, domain.PermissionChange{}, err
	}
	s.invalidateRoleSubjects(ctx, role)
	return role, change, nil
}

// CloneRoleCommand 是复制角色的入参。
type CloneRoleCommand struct {
	SourceID string
	Code     string
	Name     string
	Actor    string
}

// CloneRole 复制角色权限与数据范围，新角色默认停用，必须显式启用后才参与鉴权。
func (s *RoleService) CloneRole(ctx context.Context, cmd CloneRoleCommand) (*domain.Role, error) {
	source, err := s.roles.FindByID(ctx, cmd.SourceID)
	if err != nil {
		return nil, err
	}
	if source.IsDeleted() {
		return nil, apperr.NotFound("role not found")
	}
	roleID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate role id", err)
	}
	role, err := domain.NewRole(domain.NewRoleParams{
		ID:          roleID,
		Channel:     source.Channel,
		Scope:       source.Scope,
		MerchantID:  source.MerchantID,
		Code:        cmd.Code,
		Name:        cmd.Name,
		Description: source.Description,
		Actor:       cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	scopes := make([]domain.DataScope, 0, len(source.DataScopes))
	scopes = append(scopes, source.SortedDataScopes()...)
	if err := role.Initialize(source.Permissions.Clone(), scopes); err != nil {
		return nil, err
	}
	if err := role.Disable(cmd.Actor, role.Version); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Save(ctx, role, 0); err != nil {
			return err
		}
		return s.recordRole(ctx, role, opClone, cmd.Actor, "", map[string]string{
			attrRoleID: source.ID,
		})
	}); err != nil {
		return nil, err
	}
	return role, nil
}

// DeleteRoleCommand 是软删角色的入参。
type DeleteRoleCommand struct {
	ID      string
	Version int64
	Actor   string
	Reason  string
}

// DeleteRole 软删角色；存在**有效**绑定时返回 40900，避免静默回收权限。
//
// 过期绑定不阻断删除：它在快照中已不生效，只需保留记录供审计追溯。
func (s *RoleService) DeleteRole(ctx context.Context, cmd DeleteRoleCommand) error {
	role, err := roleForGrant(ctx, s.roles, cmd.ID, cmd.Version)
	if err != nil {
		return err
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return apperr.BadRequest("reason is required")
	}
	bindings, err := s.bindings.ListByRole(ctx, role.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, binding := range bindings {
		if binding.IsEffective(now) {
			return apperr.Conflict("role still has bindings; unbind them first")
		}
	}
	if err := role.Delete(cmd.Actor, cmd.Version); err != nil {
		return err
	}
	return s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Save(ctx, role, cmd.Version); err != nil {
			return err
		}
		return s.recordRole(ctx, role, opDelete, cmd.Actor, cmd.Reason, map[string]string{
			attrRoleCode: role.Code,
		})
	})
}

// recordRole 写入角色变更审计（action=rbac.role_change）。
func (s *RoleService) recordRole(ctx context.Context, role *domain.Role, operation, actor, reason string, attrs map[string]string) error {
	if attrs == nil {
		attrs = make(map[string]string, 3)
	}
	attrs[attrOperation] = operation
	attrs[attrRoleCode] = role.Code
	attrs[attrRoleID] = role.ID
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACRoleChange,
		actor:        actor,
		channel:      string(role.Channel),
		merchantID:   role.MerchantID,
		resourceType: ResourceTypeRole,
		resourceID:   role.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       reason,
		attrs:        attrs,
	})
}

// invalidateRoleSubjects 失效该角色全部绑定主体的快照。
//
// 失败只记录：TTL 是最终兜底，不能因为缓存删除失败而让已提交的授权变更返回错误。
func (s *RoleService) invalidateRoleSubjects(ctx context.Context, role *domain.Role) {
	if s.invalidator == nil {
		return
	}
	bindings, err := s.bindings.ListByRole(ctx, role.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "iam invalidate: list bindings failed", "role_id", role.ID, "err", err)
		return
	}
	for _, binding := range bindings {
		if err := s.invalidator.InvalidateSubject(ctx, string(binding.Channel), binding.MerchantID,
			string(binding.SubjectType), binding.SubjectID); err != nil {
			s.log.WarnContext(ctx, "iam invalidate subject cache failed",
				"role_id", role.ID, "subject_id", binding.SubjectID, "err", err)
		}
	}
}
