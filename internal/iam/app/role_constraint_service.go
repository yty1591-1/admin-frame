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

// maxPrerequisiteDepth 限制前置角色链的遍历深度。
//
// 图规模远小于该值（单角色最多 16 个前置），深度上限只用于兜底，
// 避免历史脏数据形成超长链时把一次配置请求拖成 N 次查询。
const maxPrerequisiteDepth = 64

// RoleConstraintService 是 RBAC2 角色约束（基数 + 前置角色）的用例。
//
// 端别与商户只接受调用方显式传入：transport 必须从 Principal 派生，
// 服务层再比对一次，避免 B 端通过参数访问其它商户的角色（SEC-14）。
type RoleConstraintService struct {
	roles       domain.RoleRepository
	bindings    domain.BindingRepository
	constraints domain.RoleConstraintRepository
	sodGroups   domain.SodGroupRepository
	recorder    ChangeRecorder
	uow         UnitOfWork
	log         *slog.Logger
	ids         id.Generator
	audit       auditBuilder
	limits      RoleConstraintLimits
}

// RoleConstraintLimits 是约束配置的安全开关。
type RoleConstraintLimits struct {
	// RequireReason 修改约束是否强制填写原因。
	RequireReason bool
}

// RoleConstraintServiceConfig 是 RoleConstraintService 的装配参数。
type RoleConstraintServiceConfig struct {
	Roles       domain.RoleRepository
	Bindings    domain.BindingRepository
	Constraints domain.RoleConstraintRepository
	SodGroups   domain.SodGroupRepository
	Recorder    ChangeRecorder
	UnitOfWork  UnitOfWork
	IDs         id.Generator
	Audit       AuditMeta
	Limits      RoleConstraintLimits
	Logger      *slog.Logger
}

// NewRoleConstraintService 构造约束用例；缺失必要依赖时返回错误（fail-closed）。
func NewRoleConstraintService(cfg RoleConstraintServiceConfig) (*RoleConstraintService, error) {
	if cfg.Roles == nil || cfg.Bindings == nil || cfg.Constraints == nil || cfg.SodGroups == nil {
		return nil, apperr.Internal("iam role constraint dependencies are required", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &RoleConstraintService{
		roles:       cfg.Roles,
		bindings:    cfg.Bindings,
		constraints: cfg.Constraints,
		sodGroups:   cfg.SodGroups,
		recorder:    cfg.Recorder,
		uow:         uow,
		log:         logger,
		ids:         cfg.IDs,
		audit:       newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:      cfg.Limits,
	}, nil
}

// RoleConstraintView 是约束配置的只读视图。
type RoleConstraintView struct {
	// RoleID 角色 ID。
	RoleID string
	// MaxBindings 单角色最大有效绑定数；nil 表示不限。
	MaxBindings *int
	// RequiredRoleIDs 授予该角色前必须已持有的角色，按 ID 排序。
	RequiredRoleIDs []string
	// Version 角色聚合当前版本，供下一次写入做乐观锁。
	Version int64
}

// Get 返回角色当前的 RBAC2 约束配置；跨端 / 跨商户统一 NotFound。
func (s *RoleConstraintService) Get(
	ctx context.Context, roleID string, channel domain.Channel, merchantID string,
) (*RoleConstraintView, error) {
	role, err := s.loadScoped(ctx, roleID, channel, merchantID)
	if err != nil {
		return nil, err
	}
	constraint, err := s.constraints.ListByRole(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	return &RoleConstraintView{
		RoleID:          role.ID,
		MaxBindings:     copyMaxBindings(role.MaxBindings),
		RequiredRoleIDs: constraint.Sorted(),
		Version:         role.Version,
	}, nil
}

// UpdateRoleConstraintCommand 是全量替换约束的入参。
//
// PUT 语义：RequiredRoleIDs 与 MaxBindings 均视为完整快照，空数组 / nil
// 表示清空该维度；MaxBindings 为 nil 表示取消上限。
type UpdateRoleConstraintCommand struct {
	RoleID          string
	Channel         domain.Channel
	MerchantID      string
	RequiredRoleIDs []string
	MaxBindings     *int
	Version         int64
	Actor           string
	Reason          string
}

// Update 全量替换角色的基数与前置约束。
//
// 校验顺序刻意先失败关闭再写入：任何一项配置错误都必须在 Save 之前被发现，
// 否则带环或与互斥组冲突的前置关系会进入后续绑定预检路径。
func (s *RoleConstraintService) Update(
	ctx context.Context, cmd UpdateRoleConstraintCommand,
) (*RoleConstraintView, error) {
	actor := strings.TrimSpace(cmd.Actor)
	if actor == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return nil, apperr.BadRequest("reason is required")
	}
	role, err := s.loadScoped(ctx, cmd.RoleID, cmd.Channel, cmd.MerchantID)
	if err != nil {
		return nil, err
	}
	if role.Type == domain.RoleTypeBreakglass {
		return nil, apperr.Forbidden("breakglass role constraints are managed by the approval workflow")
	}
	if cmd.MaxBindings != nil && *cmd.MaxBindings < 1 {
		return nil, apperr.BadRequest("max_bindings must be >= 1")
	}
	constraint, err := domain.NewRoleConstraint(role.ID, cmd.RequiredRoleIDs)
	if err != nil {
		return nil, err
	}
	requiredRoles, err := s.loadPrerequisites(ctx, role, constraint.RequiredRoleIDs)
	if err != nil {
		return nil, err
	}
	if err := s.ensureAcyclic(ctx, role.ID, requiredRoles); err != nil {
		return nil, err
	}
	if err := s.ensureNoSodConflict(ctx, role, requiredRoles); err != nil {
		return nil, err
	}
	if err := s.ensureCardinalityFeasible(ctx, role, cmd.MaxBindings); err != nil {
		return nil, err
	}

	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := role.SetMaxBindings(cmd.MaxBindings, actor, cmd.Version); err != nil {
			return err
		}
		if err := s.constraints.Replace(ctx, role, constraint.RequiredRoleIDs, actor); err != nil {
			return err
		}
		if err := s.roles.Save(ctx, role, cmd.Version); err != nil {
			return err
		}
		return s.recordConstraints(ctx, role, constraint, cmd.MaxBindings, actor, cmd.Reason)
	}); err != nil {
		return nil, err
	}
	return &RoleConstraintView{
		RoleID:          role.ID,
		MaxBindings:     copyMaxBindings(role.MaxBindings),
		RequiredRoleIDs: constraint.Sorted(),
		Version:         role.Version,
	}, nil
}

// loadScoped 加载角色并校验端别 / 商户 / 软删；跨租户统一 NotFound（SEC-14）。
func (s *RoleConstraintService) loadScoped(
	ctx context.Context, roleID string, channel domain.Channel, merchantID string,
) (*domain.Role, error) {
	role, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if role.IsDeleted() || role.Channel != channel || role.MerchantID != merchantID {
		return nil, apperr.NotFound("role not found")
	}
	return role, nil
}

// loadPrerequisites 逐个校验前置角色：存在、未删除、active、同端同商户、非破玻璃。
func (s *RoleConstraintService) loadPrerequisites(
	ctx context.Context, role *domain.Role, requiredRoleIDs []string,
) ([]*domain.Role, error) {
	out := make([]*domain.Role, 0, len(requiredRoleIDs))
	for _, requiredID := range requiredRoleIDs {
		required, err := s.roles.FindByID(ctx, requiredID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return nil, apperr.BadRequest("prerequisite role not found: " + requiredID)
			}
			return nil, err
		}
		if required.IsDeleted() || !required.IsActive() {
			return nil, apperr.BadRequest("prerequisite role is not active: " + required.Code)
		}
		if required.Channel != role.Channel || required.MerchantID != role.MerchantID {
			return nil, apperr.BadRequest("prerequisite role must belong to the same channel and merchant: " + required.Code)
		}
		if required.Type == domain.RoleTypeBreakglass {
			return nil, apperr.BadRequest("breakglass role must not be a prerequisite: " + required.Code)
		}
		out = append(out, required)
	}
	return out, nil
}

// ensureAcyclic 校验新增前置边不会形成环。
//
// 只检查「前置角色能否沿已有前置边回到 roleID」：其它历史环不影响本次写入，
// 若一并拒绝会让一次无关修复被历史脏数据锁死。
func (s *RoleConstraintService) ensureAcyclic(
	ctx context.Context, roleID string, requiredRoles []*domain.Role,
) error {
	visited := make(map[string]struct{}, len(requiredRoles))
	for _, required := range requiredRoles {
		if err := s.walkPrerequisites(ctx, required.ID, roleID, 0, visited); err != nil {
			return err
		}
	}
	return nil
}

// walkPrerequisites 深度优先遍历前置链；命中 target 说明成环。
func (s *RoleConstraintService) walkPrerequisites(
	ctx context.Context, currentID, targetID string, depth int, visited map[string]struct{},
) error {
	if depth >= maxPrerequisiteDepth {
		return apperr.BadRequest("prerequisite chain exceeds max depth")
	}
	if _, seen := visited[currentID]; seen {
		// 已遍历过的节点不含到 target 的路径（否则已在首次遍历时返回）。
		return nil
	}
	visited[currentID] = struct{}{}
	constraint, err := s.constraints.ListByRole(ctx, currentID)
	if err != nil {
		return err
	}
	for _, nextID := range constraint.RequiredRoleIDs {
		if nextID == targetID {
			return apperr.BadRequest(fmt.Sprintf(
				"prerequisite cycle detected: role %s would require itself through %s", targetID, currentID))
		}
		if err := s.walkPrerequisites(ctx, nextID, targetID, depth+1, visited); err != nil {
			return err
		}
	}
	return nil
}

// ensureNoSodConflict 校验前置角色与角色自身的有效闭包不会同时触发活跃互斥组。
//
// 语义：持有 role 即代表同时持有全部前置角色，因此两个闭包的并集必须满足
// 每个活跃互斥组的 max_active_roles；否则配置本身就把所有持有者置于违规状态。
func (s *RoleConstraintService) ensureNoSodConflict(
	ctx context.Context, role *domain.Role, requiredRoles []*domain.Role,
) error {
	closure := make(map[string]*domain.Role, len(requiredRoles)+1)
	if err := s.collectClosure(ctx, role, closure); err != nil {
		return err
	}
	for _, required := range requiredRoles {
		if err := s.collectClosure(ctx, required, closure); err != nil {
			return err
		}
	}
	groupSeen := make(map[string]struct{}, len(closure))
	for roleID := range closure {
		groups, err := s.sodGroups.ListByRole(ctx, role.Channel, role.MerchantID, roleID)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if !group.IsActive() {
				continue
			}
			if _, dup := groupSeen[group.ID]; dup {
				continue
			}
			groupSeen[group.ID] = struct{}{}
			count := 0
			for _, memberID := range group.RoleIDs {
				if _, ok := closure[memberID]; ok {
					count++
				}
			}
			if count > group.MaxActiveRoles {
				return apperr.BadRequest(fmt.Sprintf(
					"constraints conflict with sod group %s: %d roles would be held together, limit %d",
					group.Code, count, group.MaxActiveRoles))
			}
		}
	}
	return nil
}

// collectClosure 把角色自身与全部 active 祖先并入闭包。
func (s *RoleConstraintService) collectClosure(
	ctx context.Context, role *domain.Role, closure map[string]*domain.Role,
) error {
	if _, ok := closure[role.ID]; ok {
		return nil
	}
	closure[role.ID] = role
	ancestors, err := s.roles.ListAncestors(ctx, role.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil
		}
		return err
	}
	for _, ancestor := range ancestors {
		if !ancestor.IsActive() {
			continue
		}
		if _, ok := closure[ancestor.ID]; ok {
			continue
		}
		closure[ancestor.ID] = ancestor
	}
	return nil
}

// ensureCardinalityFeasible 拒绝低于存量有效绑定数的上限。
//
// 允许写入会让角色立刻处于违规状态；与互斥组配置「存在存量违规即拒绝」保持一致。
func (s *RoleConstraintService) ensureCardinalityFeasible(
	ctx context.Context, role *domain.Role, maxBindings *int,
) error {
	if maxBindings == nil {
		return nil
	}
	count, err := s.bindings.CountEffectiveByRole(ctx, role.Channel, role.MerchantID, role.ID, time.Now().UTC())
	if err != nil {
		return apperr.Internal("count effective bindings", err)
	}
	if count > *maxBindings {
		return apperr.Conflict(fmt.Sprintf(
			"role %s already has %d effective bindings, max_bindings %d cannot be applied",
			role.Code, count, *maxBindings))
	}
	return nil
}

// recordConstraints 写入约束变更审计（action=rbac.role_change）。
func (s *RoleConstraintService) recordConstraints(
	ctx context.Context,
	role *domain.Role,
	constraint domain.RoleConstraint,
	maxBindings *int,
	actor, reason string,
) error {
	maxBindingsAttr := ""
	if maxBindings != nil {
		maxBindingsAttr = strconv.Itoa(*maxBindings)
	}
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACRoleChange,
		actor:        actor,
		channel:      string(role.Channel),
		merchantID:   role.MerchantID,
		resourceType: ResourceTypeRole,
		resourceID:   role.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       reason,
		attrs: map[string]string{
			attrOperation:   "update_constraints",
			attrRoleCode:    role.Code,
			attrRoleID:      role.ID,
			attrRoleIDs:     codesAttr(constraint.Sorted()),
			attrMaxBindings: maxBindingsAttr,
		},
	})
}

// copyMaxBindings 复制可选整数，避免把聚合内部指针暴露给调用方。
func copyMaxBindings(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
