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
	if reactivated {
		// 复活沿用首次加入时间：UPSERT 有意不覆盖 created_at，
		// 返回体必须与库中一致，否则前端展示的「加入时间」会跳变。
		member.CreatedAt = existing.CreatedAt
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

// UpdateMemberInput 是成员变更入参；Role 与 Status 至少给一个。
//
// 用指针区分「未提供」与「显式设为某值」：只改角色时不能顺手把状态重置为 active。
type UpdateMemberInput struct {
	// TenantID 租户 ID。
	TenantID string
	// SubjectID 成员主体 ID。
	SubjectID string
	// Role 目标角色；nil 表示不改角色。
	Role *domain.MemberRole
	// Status 目标状态；nil 表示不改状态。
	Status *domain.MemberStatus
	// Reason 状态变更原因，写入审计。
	Reason string
	// Actor 操作者。
	Actor Actor
}

// Update 变更成员角色与状态，一次请求只写一次。
//
// 三条约束：
//   - 先校验全部入参再落库，避免「角色改了、状态非法」的半提交；
//   - 角色与状态合并成一行一次 UPSERT，审计按实际变化维度各记一条；
//   - 「最后一名 owner」按变更后的终态判断，降级与停用共用同一段逻辑。
func (s *MemberService) Update(ctx context.Context, in UpdateMemberInput) (domain.Member, error) {
	if in.Role == nil && in.Status == nil {
		return domain.Member{}, apperr.BadRequest("role or status is required")
	}
	// 入参校验前置：非法枚举不查库、不写库，直接 400。
	if in.Role != nil && !in.Role.Valid() {
		return domain.Member{}, translate(&domain.InvalidRoleError{Role: string(*in.Role)})
	}
	if in.Status != nil {
		if err := domain.ValidateMemberStatus(*in.Status); err != nil {
			return domain.Member{}, translate(err)
		}
	}
	if _, err := mutableTenant(ctx, s.tenants, in.TenantID); err != nil {
		return domain.Member{}, err
	}
	current, err := s.members.GetMember(ctx, in.TenantID, in.SubjectID)
	if err != nil {
		return domain.Member{}, translate(err)
	}

	roleChanged := in.Role != nil && *in.Role != current.Role
	statusChanged := in.Status != nil && *in.Status != current.Status
	if !roleChanged && !statusChanged {
		// 幂等：无实际变化不写库、不写审计，避免审计里出现空变更。
		return current, nil
	}

	next := current
	if roleChanged {
		if next, err = next.WithRole(*in.Role, s.now()); err != nil {
			return domain.Member{}, translate(err)
		}
	}
	if statusChanged {
		if next, err = next.WithStatus(*in.Status, s.now()); err != nil {
			return domain.Member{}, translate(err)
		}
	}
	if err := s.ensureOwnerRemains(ctx, current, next); err != nil {
		return domain.Member{}, err
	}
	if err := s.members.UpsertMember(ctx, next); err != nil {
		return domain.Member{}, translate(err)
	}

	if roleChanged {
		s.audit.emit(ctx, in.Actor, ActionMemberRoleChange, ResourceTypeMember, next.TenantID,
			audit.OutcomeSuccess, "", map[string]string{
				"subject_id": next.SubjectID,
				"from":       string(current.Role),
				"to":         string(next.Role),
			})
	}
	if statusChanged {
		action := ActionMemberDisable
		if next.Active() {
			action = ActionMemberEnable
		}
		s.audit.emit(ctx, in.Actor, action, ResourceTypeMember, next.TenantID,
			audit.OutcomeSuccess, in.Reason, map[string]string{
				"subject_id": next.SubjectID,
				"role":       string(next.Role),
			})
	}
	return next, nil
}

// ensureOwnerRemains 保证变更后仍有在册 owner。
//
// 只处理「原本是在册 owner、变更后不再是」这一种情况：其余变更不会减少在册 owner 数。
func (s *MemberService) ensureOwnerRemains(ctx context.Context, current, next domain.Member) error {
	wasOwner := current.Role == domain.MemberRoleOwner && current.Active()
	stillOwner := next.Role == domain.MemberRoleOwner && next.Active()
	if !wasOwner || stillOwner {
		return nil
	}
	count, err := s.members.CountActiveByRole(ctx, current.TenantID, domain.MemberRoleOwner)
	if err != nil {
		return translate(err)
	}
	if count <= 1 {
		return translate(domain.ErrLastOwner)
	}
	return nil
}

// SetRole 变更成员角色；等价于只提供 Role 的 Update。
func (s *MemberService) SetRole(ctx context.Context, in SetMemberRoleInput) (domain.Member, error) {
	role := in.Role
	return s.Update(ctx, UpdateMemberInput{
		TenantID: in.TenantID, SubjectID: in.SubjectID, Role: &role, Actor: in.Actor,
	})
}

// SetStatus 启用 / 停用成员；等价于只提供 Status 的 Update。
func (s *MemberService) SetStatus(ctx context.Context, in SetMemberStatusInput) (domain.Member, error) {
	status := in.Status
	return s.Update(ctx, UpdateMemberInput{
		TenantID: in.TenantID, SubjectID: in.SubjectID, Status: &status,
		Reason: in.Reason, Actor: in.Actor,
	})
}

// boolString 输出 true/false，供审计 attrs 使用。
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// isInvalidEnum 判断是否为枚举类错误（角色 / 状态 / 资源键），统一按 400 处理。
func isInvalidEnum(err error) bool {
	var invalidRole *domain.InvalidRoleError
	var invalidStatus *domain.InvalidStatusError
	var invalidResource *domain.InvalidResourceError
	return errors.As(err, &invalidRole) || errors.As(err, &invalidStatus) || errors.As(err, &invalidResource)
}
