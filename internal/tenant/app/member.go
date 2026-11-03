package app

import (
	"context"
	"errors"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// 成员相关审计动作。
const (
	// ActionMemberAdd 添加成员（含重新启用已停用成员）。
	ActionMemberAdd = "tenant.member.add"
	// ActionMemberRoleChange 变更成员角色。
	ActionMemberRoleChange = "tenant.member.role_change"
	// ActionMemberDisable 停用成员（不物理删除）。
	ActionMemberDisable = "tenant.member.disable"
	// ActionMemberEnable 重新启用成员。
	ActionMemberEnable = "tenant.member.enable"
)

// MemberService 编排租户成员用例。
type MemberService struct {
	tenants domain.Repository
	members domain.MemberRepository
	audit   auditContext
	now     func() time.Time
}

// NewMemberService 构造成员用例；三个依赖均必填。
func NewMemberService(tenants domain.Repository, members domain.MemberRepository, emitter audit.Emitter, opts Options) (*MemberService, error) {
	if tenants == nil {
		return nil, apperr.Internal("tenant member service requires a tenant repository", nil)
	}
	if members == nil {
		return nil, apperr.Internal("tenant member service requires a member repository", nil)
	}
	if emitter == nil {
		return nil, apperr.Internal("tenant member service requires an audit emitter", nil)
	}
	now := time.Now
	return &MemberService{
		tenants: tenants,
		members: members,
		audit:   auditContext{emitter: emitter, ids: nil, service: opts.Service, now: now},
		now:     now,
	}, nil
}

// AddMemberInput 是添加成员入参。
type AddMemberInput struct {
	// TenantID 租户 ID。
	TenantID string
	// SubjectID 成员主体 ID。
	SubjectID string
	// Role 成员角色。
	Role domain.MemberRole
	// Actor 操作者。
	Actor Actor
}

// SetMemberRoleInput 是改角色入参。
type SetMemberRoleInput struct {
	// TenantID 租户 ID。
	TenantID string
	// SubjectID 成员主体 ID。
	SubjectID string
	// Role 目标角色。
	Role domain.MemberRole
	// Actor 操作者。
	Actor Actor
}

// SetMemberStatusInput 是启停成员入参。
type SetMemberStatusInput struct {
	// TenantID 租户 ID。
	TenantID string
	// SubjectID 成员主体 ID。
	SubjectID string
	// Status 目标状态。
	Status domain.MemberStatus
	// Reason 变更原因（停用建议填写，便于回溯）。
	Reason string
	// Actor 操作者。
	Actor Actor
}

// Add 添加成员；已在册的成员重复添加返回冲突，不静默改角色。
func (s *MemberService) Add(ctx context.Context, in AddMemberInput) (domain.Member, error) {
	if _, err := mutableTenant(ctx, s.tenants, in.TenantID); err != nil {
		return domain.Member{}, err
	}
	existing, err := s.members.GetMember(ctx, in.TenantID, in.SubjectID)
	reactivated := false
	switch {
	case err == nil && existing.Active():
		// 显式 Add 不应顺手改角色：改角色是另一个动作，需要单独的审计语义。
		return domain.Member{}, translate(domain.ErrMemberAlreadyExists)
	case err == nil:
		// 已停用 → 重新启用并采用入参角色；审计仍记 add，用 reactivated 标明这次是复活。
		reactivated = true
	case errors.Is(err, domain.ErrMemberNotFound):
		// 正常新增路径。
	default:
		return domain.Member{}, translate(err)
	}

	member, err := domain.NewMember(in.TenantID, in.SubjectID, in.Role, s.now())
	if err != nil {
		return domain.Member{}, translate(err)
	}
	if err := s.members.UpsertMember(ctx, member); err != nil {
		return domain.Member{}, translate(err)
	}
	s.audit.emit(ctx, in.Actor, ActionMemberAdd, ResourceTypeMember, member.TenantID,
		audit.OutcomeSuccess, "", map[string]string{
			"subject_id":  member.SubjectID,
			"role":        string(member.Role),
			"reactivated": boolString(reactivated),
		})
	return member, nil
}

// List 列出租户成员。
func (s *MemberService) List(ctx context.Context, tenantID string, includeDisabled bool) ([]domain.Member, error) {
	if _, err := s.tenants.Get(ctx, tenantID); err != nil {
		return nil, translate(err)
	}
	members, err := s.members.ListMembers(ctx, tenantID, includeDisabled)
	if err != nil {
		return nil, translate(err)
	}
	return members, nil
}

// SetRole 变更成员角色；降级最后一名在册 owner 会被拒绝。
func (s *MemberService) SetRole(ctx context.Context, in SetMemberRoleInput) (domain.Member, error) {
	if _, err := mutableTenant(ctx, s.tenants, in.TenantID); err != nil {
		return domain.Member{}, err
	}
	member, err := s.members.GetMember(ctx, in.TenantID, in.SubjectID)
	if err != nil {
		return domain.Member{}, translate(err)
	}
	if member.Role == in.Role {
		return member, nil
	}
	// 降级 / 停用最后一名 owner 会让租户失去管理者，必须先在别处补一个 owner。
	if member.Role == domain.MemberRoleOwner && member.Active() {
		count, err := s.members.CountActiveByRole(ctx, in.TenantID, domain.MemberRoleOwner)
		if err != nil {
			return domain.Member{}, translate(err)
		}
		if count <= 1 {
			return domain.Member{}, translate(domain.ErrLastOwner)
		}
	}
	next, err := member.WithRole(in.Role, s.now())
	if err != nil {
		return domain.Member{}, translate(err)
	}
	if err := s.members.UpsertMember(ctx, next); err != nil {
		return domain.Member{}, translate(err)
	}
	s.audit.emit(ctx, in.Actor, ActionMemberRoleChange, ResourceTypeMember, next.TenantID,
		audit.OutcomeSuccess, "", map[string]string{
			"subject_id": next.SubjectID,
			"from":       string(member.Role),
			"to":         string(next.Role),
		})
	return next, nil
}

// SetStatus 启用 / 停用成员；停用最后一名在册 owner 会被拒绝。
func (s *MemberService) SetStatus(ctx context.Context, in SetMemberStatusInput) (domain.Member, error) {
	if _, err := mutableTenant(ctx, s.tenants, in.TenantID); err != nil {
		return domain.Member{}, err
	}
	member, err := s.members.GetMember(ctx, in.TenantID, in.SubjectID)
	if err != nil {
		return domain.Member{}, translate(err)
	}
	if member.Status == in.Status {
		return member, nil
	}
	if in.Status == domain.MemberStatusDisabled &&
		member.Role == domain.MemberRoleOwner && member.Active() {
		count, err := s.members.CountActiveByRole(ctx, in.TenantID, domain.MemberRoleOwner)
		if err != nil {
			return domain.Member{}, translate(err)
		}
		if count <= 1 {
			return domain.Member{}, translate(domain.ErrLastOwner)
		}
	}
	next, err := member.WithStatus(in.Status, s.now())
	if err != nil {
		return domain.Member{}, translate(err)
	}
	if err := s.members.UpsertMember(ctx, next); err != nil {
		return domain.Member{}, translate(err)
	}

	action := ActionMemberDisable
	if next.Active() {
		action = ActionMemberEnable
	}
	s.audit.emit(ctx, in.Actor, action, ResourceTypeMember, next.TenantID, audit.OutcomeSuccess,
		in.Reason, map[string]string{
			"subject_id": next.SubjectID,
			"role":       string(next.Role),
		})
	return next, nil
}

// boolString 输出 true/false，供审计 attrs 使用。
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// isInvalidEnum 判断是否为枚举类错误（角色 / 资源键），统一按 400 处理。
func isInvalidEnum(err error) bool {
	var invalidRole *domain.InvalidRoleError
	var invalidResource *domain.InvalidResourceError
	return errors.As(err, &invalidRole) || errors.As(err, &invalidResource)
}
