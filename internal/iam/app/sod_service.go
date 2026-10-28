package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// defaultMaxSodViolations 是预检默认返回上限。
const defaultMaxSodViolations = 50

// SodGroupService 是 RBAC2 互斥角色组用例。
//
// 端别与商户只接受调用方显式传入：transport 必须从 Principal 派生，
// 服务层再做一次比对，避免 B 端通过请求参数访问其它商户的组（SEC-14）。
type SodGroupService struct {
	groups     domain.SodGroupRepository
	roles      domain.RoleRepository
	violations domain.SodViolationRepository
	recorder   ChangeRecorder
	uow        UnitOfWork
	log        *slog.Logger
	ids        id.Generator
	audit      auditBuilder
	limits     SodGroupLimits
}

// SodGroupLimits 是互斥组安全开关与规模上限。
type SodGroupLimits struct {
	// RequireReason 成员变更 / 启停 / 删除是否强制填写原因。
	RequireReason bool
	// MaxViolations 预检最多返回的违规主体数；≤0 时使用默认值。
	MaxViolations int
}

// SodGroupServiceConfig 是 SodGroupService 的装配参数。
type SodGroupServiceConfig struct {
	Groups     domain.SodGroupRepository
	Roles      domain.RoleRepository
	Violations domain.SodViolationRepository
	Recorder   ChangeRecorder
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Limits     SodGroupLimits
	Logger     *slog.Logger
}

// NewSodGroupService 构造互斥组用例；缺失必要依赖时返回错误（fail-closed）。
func NewSodGroupService(cfg SodGroupServiceConfig) (*SodGroupService, error) {
	if cfg.Groups == nil {
		return nil, apperr.Internal("iam sod group repository is required", nil)
	}
	if cfg.Roles == nil {
		return nil, apperr.Internal("iam role repository is required", nil)
	}
	if cfg.Violations == nil {
		return nil, apperr.Internal("iam sod violation repository is required", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	if cfg.Limits.MaxViolations <= 0 {
		cfg.Limits.MaxViolations = defaultMaxSodViolations
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &SodGroupService{
		groups:     cfg.Groups,
		roles:      cfg.Roles,
		violations: cfg.Violations,
		recorder:   cfg.Recorder,
		uow:        uow,
		log:        logger,
		ids:        cfg.IDs,
		audit:      newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:     cfg.Limits,
	}, nil
}

// CreateSodGroupCommand 是创建互斥组的入参。
type CreateSodGroupCommand struct {
	Channel        domain.Channel
	MerchantID     string
	Code           string
	Name           string
	Description    string
	MaxActiveRoles int
	RoleIDs        []string
	Actor          string
	Reason         string
}

// Create 创建互斥组；存在存量违规时拒绝写入（40900）。
func (s *SodGroupService) Create(ctx context.Context, cmd CreateSodGroupCommand) (*domain.SodGroup, error) {
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	groupID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate sod group id", err)
	}
	group, err := domain.NewSodGroup(domain.NewSodGroupParams{
		ID:             groupID,
		Channel:        cmd.Channel,
		MerchantID:     cmd.MerchantID,
		Code:           cmd.Code,
		Name:           cmd.Name,
		Description:    cmd.Description,
		MaxActiveRoles: cmd.MaxActiveRoles,
		RoleIDs:        cmd.RoleIDs,
		Actor:          cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := s.validateSodRoles(ctx, group.Channel, group.MerchantID, group.RoleIDs); err != nil {
		return nil, err
	}
	if err := s.ensureNoViolations(ctx, group); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.groups.Save(ctx, group, 0); err != nil {
			return err
		}
		return s.recordSodGroup(ctx, group, opCreate, cmd.Actor, cmd.Reason)
	}); err != nil {
		return nil, err
	}
	return group, nil
}

// UpdateSodGroupCommand 是 PATCH 互斥组的入参；nil 字段表示不修改。
type UpdateSodGroupCommand struct {
	ID             string
	Channel        domain.Channel
	MerchantID     string
	Name           *string
	Description    *string
	MaxActiveRoles *int
	RoleIDs        *[]string
	Status         *string
	Version        int64
	Actor          string
	Reason         string
}

// Update 局部更新名称、说明、阈值、成员与启停。
//
// 成员变化 / 阈值收紧 / 启用都会扩大约束面，必须通过存量预检；
// 存在违规时拒绝修改，避免把带病数据直接推入 enforce 模式。
func (s *SodGroupService) Update(ctx context.Context, cmd UpdateSodGroupCommand) (*domain.SodGroup, error) {
	if cmd.Name == nil && cmd.Description == nil && cmd.MaxActiveRoles == nil &&
		cmd.RoleIDs == nil && cmd.Status == nil {
		return nil, apperr.BadRequest("no fields to update")
	}
	group, err := s.loadScoped(ctx, cmd.ID, cmd.Channel, cmd.MerchantID)
	if err != nil {
		return nil, err
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" &&
		(cmd.RoleIDs != nil || cmd.Status != nil) {
		return nil, apperr.BadRequest("reason is required")
	}

	operation := opUpdate
	rolesChanged := false
	if cmd.Name != nil || cmd.Description != nil || cmd.MaxActiveRoles != nil {
		name := group.Name
		if cmd.Name != nil {
			name = *cmd.Name
		}
		description := group.Description
		if cmd.Description != nil {
			description = *cmd.Description
		}
		maxActive := group.MaxActiveRoles
		if cmd.MaxActiveRoles != nil {
			maxActive = *cmd.MaxActiveRoles
		}
		if err := group.UpdateDetails(name, description, maxActive, cmd.Actor, group.Version); err != nil {
			return nil, err
		}
	}
	if cmd.RoleIDs != nil {
		next := *cmd.RoleIDs
		if err := s.validateSodRoles(ctx, group.Channel, group.MerchantID, next); err != nil {
			return nil, err
		}
		if err := group.ReplaceRoles(next, cmd.Actor, group.Version); err != nil {
			return nil, err
		}
		rolesChanged = true
	}
	statusChanged := false
	if cmd.Status != nil {
		switch domain.SodGroupStatus(strings.TrimSpace(*cmd.Status)) {
		case domain.SodGroupActive:
			operation = opEnable
			if err := group.Enable(cmd.Actor, group.Version); err != nil {
				return nil, err
			}
			statusChanged = true
		case domain.SodGroupDisabled:
			operation = opDisable
			if err := group.Disable(cmd.Actor, group.Version); err != nil {
				return nil, err
			}
			statusChanged = true
		default:
			return nil, apperr.BadRequest("status must be active or disabled")
		}
	}

	// 只有「收紧约束」方向的变更需要预检；停用直接放行。
	if statusChanged && group.Status == domain.SodGroupActive {
		// 启用前复核成员仍可用：角色可能在停用期间被软删或停用。
		if err := s.validateSodRoles(ctx, group.Channel, group.MerchantID, group.RoleIDs); err != nil {
			return nil, err
		}
	}
	if rolesChanged || cmd.MaxActiveRoles != nil || (statusChanged && group.Status == domain.SodGroupActive) {
		if err := s.ensureNoViolations(ctx, group); err != nil {
			return nil, err
		}
	}

	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.groups.Save(ctx, group, cmd.Version); err != nil {
			return err
		}
		return s.recordSodGroup(ctx, group, operation, cmd.Actor, cmd.Reason)
	}); err != nil {
		return nil, err
	}
	return group, nil
}

// DeleteSodGroupCommand 是软删互斥组的入参。
type DeleteSodGroupCommand struct {
	ID         string
	Channel    domain.Channel
	MerchantID string
	Version    int64
	Actor      string
	Reason     string
}

// Delete 软删互斥组；停用后不再拦截新绑定，但保留历史关系。
func (s *SodGroupService) Delete(ctx context.Context, cmd DeleteSodGroupCommand) error {
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return apperr.BadRequest("reason is required")
	}
	group, err := s.loadScoped(ctx, cmd.ID, cmd.Channel, cmd.MerchantID)
	if err != nil {
		return err
	}
	if err := group.Delete(cmd.Actor, cmd.Version); err != nil {
		return err
	}
	return s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.groups.Save(ctx, group, cmd.Version); err != nil {
			return err
		}
		return s.recordSodGroup(ctx, group, opDelete, cmd.Actor, cmd.Reason)
	})
}

// Get 按 ID 加载互斥组；跨端 / 跨商户统一 NotFound。
func (s *SodGroupService) Get(
	ctx context.Context, groupID string, channel domain.Channel, merchantID string,
) (*domain.SodGroup, error) {
	return s.loadScoped(ctx, groupID, channel, merchantID)
}

// LoadForChange 供审批执行器按作用域重新加载目标聚合。
func (s *SodGroupService) LoadForChange(
	ctx context.Context, groupID string, channel domain.Channel, merchantID string,
) (*domain.SodGroup, error) {
	return s.loadScoped(ctx, groupID, channel, merchantID)
}

// List 按条件分页列举互斥组；作用域过滤由调用方在 filter 中传入。
func (s *SodGroupService) List(
	ctx context.Context, filter domain.SodGroupFilter,
) ([]*domain.SodGroup, error) {
	return s.groups.List(ctx, filter)
}

// Count 返回满足条件的互斥组总数。
func (s *SodGroupService) Count(ctx context.Context, filter domain.SodGroupFilter) (int, error) {
	return s.groups.Count(ctx, filter)
}

// ListViolations 返回指定互斥组的存量违规主体。
func (s *SodGroupService) ListViolations(
	ctx context.Context, groupID string, channel domain.Channel, merchantID string, limit int,
) ([]domain.SodViolation, error) {
	group, err := s.loadScoped(ctx, groupID, channel, merchantID)
	if err != nil {
		return nil, err
	}
	return s.violations.List(ctx, group, time.Now().UTC(), limit)
}

// Preflight 在不落库的前提下评估候选互斥组，供创建 / 改组前预检。
//
// 返回空切片表示当前租户内没有主体会被该组规则直接判为违规。
func (s *SodGroupService) Preflight(
	ctx context.Context,
	channel domain.Channel,
	merchantID string,
	roleIDs []string,
	maxActiveRoles, limit int,
) ([]domain.SodViolation, error) {
	group, err := domain.NewSodGroup(domain.NewSodGroupParams{
		ID:             "preflight",
		Channel:        channel,
		MerchantID:     merchantID,
		Code:           "preflight",
		Name:           "preflight",
		MaxActiveRoles: maxActiveRoles,
		RoleIDs:        roleIDs,
		Actor:          "preflight",
	})
	if err != nil {
		return nil, err
	}
	if err := s.validateSodRoles(ctx, group.Channel, group.MerchantID, group.RoleIDs); err != nil {
		return nil, err
	}
	return s.violations.List(ctx, group, time.Now().UTC(), limit)
}

// loadScoped 加载互斥组并校验端别 / 商户；跨租户统一 NotFound（SEC-14）。
func (s *SodGroupService) loadScoped(
	ctx context.Context, groupID string, channel domain.Channel, merchantID string,
) (*domain.SodGroup, error) {
	group, err := s.groups.FindByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group.IsDeleted() || group.Channel != channel || group.MerchantID != merchantID {
		return nil, apperr.NotFound("sod group not found")
	}
	return group, nil
}

// validateSodRoles 校验组成员：同端同商户、active、非破玻璃、互不为祖先。
func (s *SodGroupService) validateSodRoles(
	ctx context.Context, channel domain.Channel, merchantID string, roleIDs []string,
) error {
	members := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		role, err := s.roles.FindByID(ctx, roleID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.BadRequest("role not found: " + roleID)
			}
			return err
		}
		if role.IsDeleted() || !role.IsActive() {
			return apperr.BadRequest("role is not active: " + role.Code)
		}
		if role.Channel != channel || role.MerchantID != merchantID {
			return apperr.BadRequest("role must belong to the same channel and merchant: " + role.Code)
		}
		if role.Type == domain.RoleTypeBreakglass {
			return apperr.BadRequest("breakglass role must not join sod group: " + role.Code)
		}
		members[roleID] = struct{}{}
	}

	// 祖先与后代同时入组意味着「持有后代必然违规」，配置即错误。
	for roleID := range members {
		ancestors, err := s.roles.ListAncestors(ctx, roleID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				continue
			}
			return err
		}
		for _, ancestor := range ancestors {
			if _, ok := members[ancestor.ID]; ok {
				return apperr.BadRequest(fmt.Sprintf(
					"roles %s and %s are ancestor/descendant and cannot join the same sod group",
					ancestor.Code, roleID))
			}
		}
	}
	return nil
}

// ensureNoViolations 预检存量数据；存在违规时拒绝变更并提示先清理绑定。
func (s *SodGroupService) ensureNoViolations(ctx context.Context, group *domain.SodGroup) error {
	violations, err := s.violations.List(ctx, group, time.Now().UTC(), s.limits.MaxViolations)
	if err != nil {
		return apperr.Internal("check sod group violations", err)
	}
	if len(violations) == 0 {
		return nil
	}
	s.log.WarnContext(ctx, "iam sod group rejected by existing violations",
		"group_code", group.Code, "violation_count", len(violations))
	return apperr.Conflict(fmt.Sprintf(
		"sod group %s would violate %d existing subjects; clean up bindings first",
		group.Code, len(violations)))
}

// recordSodGroup 写入互斥组变更审计（action=rbac.sod_change）。
func (s *SodGroupService) recordSodGroup(
	ctx context.Context, group *domain.SodGroup, operation, actor, reason string,
) error {
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACSodChange,
		actor:        actor,
		channel:      string(group.Channel),
		merchantID:   group.MerchantID,
		resourceType: ResourceTypeSodGroup,
		resourceID:   group.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       reason,
		attrs: map[string]string{
			attrOperation: operation,
			attrGroupCode: group.Code,
			attrRoleIDs:   codesAttr(group.SortedRoleIDs()),
			attrMaxActive: strconv.Itoa(group.MaxActiveRoles),
		},
	})
}
