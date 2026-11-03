package app

import (
	"context"
	"errors"
	"strconv"
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

// ResourceUser 席位对应的配额资源键（T-13）。
//
// 席位即「在册成员数」：上限读 tenant_quota 的 user 资源，与套餐物化、
// ENT-14 执行层使用同一个键。
const ResourceUser = "user"

// MemberService 编排租户成员用例。
type MemberService struct {
	tenants domain.Repository
	members domain.MemberRepository
	// quotas 配额仓储（T-13 席位上限）；nil 表示不启用席位校验（单测 / 简化部署）。
	quotas domain.QuotaRepository
	// rolesync 成员角色 → IAM 绑定同步（T-18，ADR-0007）；noop 当未装配。
	rolesync RoleSyncer
	audit    auditContext
	now      func() time.Time
}

// NewMemberService 构造成员用例；三个依赖均必填（配额仓储可空，见 Options.Quotas）。
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
		tenants:  tenants,
		members:  members,
		quotas:   opts.Quotas,
		rolesync: roleSyncerOrNoop(opts.RoleSyncer),
		audit:    auditContext{emitter: emitter, ids: nil, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		now:      now,
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
//
// 成员写入与审计同事务：审计写不进去就不加成员，避免「成员已加、无记录」。
//
// 席位上限（T-13）：临界区内先校验 tenant_quota 的 user 上限——
// 用 MemberRepository.Mutate（租户行锁）而不是普通事务，否则两个并发
// 添加会各自读到相同的在册数而同时通过校验。复活停用成员同样占一个
// 席位：在册数会 +1，语义上没有豁免的理由。
func (s *MemberService) Add(ctx context.Context, in AddMemberInput) (domain.Member, error) {
	var member domain.Member
	err := s.members.Mutate(ctx, in.TenantID, func(txCtx context.Context) error {
		if _, err := mutableTenant(txCtx, s.tenants, in.TenantID); err != nil {
			return err
		}
		existing, err := s.members.GetMember(txCtx, in.TenantID, in.SubjectID)
		reactivated := false
		switch {
		case err == nil && existing.Active():
			// 显式 Add 不应顺手改角色：改角色是另一个动作，需要单独的审计语义。
			return domain.ErrMemberAlreadyExists
		case err == nil:
			// 已停用 → 重新启用并采用入参角色；审计仍记 add，用 reactivated 标明这次是复活。
			reactivated = true
		case errors.Is(err, domain.ErrMemberNotFound):
			// 正常新增路径。
		default:
			return err
		}

		if err := s.ensureSeatAvailable(txCtx, in.TenantID); err != nil {
			return err
		}
		built, buildErr := domain.NewMember(in.TenantID, in.SubjectID, in.Role, s.now())
		if buildErr != nil {
			return buildErr
		}
		if reactivated {
			// 复活沿用首次加入时间：UPSERT 有意不覆盖 created_at，
			// 返回体必须与库中一致，否则前端展示的「加入时间」会跳变。
			built.CreatedAt = existing.CreatedAt
		}
		if upsertErr := s.members.UpsertMember(txCtx, built); upsertErr != nil {
			return upsertErr
		}
		// T-18（ADR-0007）：成员角色同步到 IAM 模板绑定；同事务内失败即整体回滚。
		if syncErr := s.rolesync.SyncRole(txCtx, built.TenantID, built.SubjectID, &built.Role); syncErr != nil {
			return apperr.Internal("sync member role binding failed", syncErr)
		}
		if emitErr := s.audit.emit(txCtx, in.Actor, ActionMemberAdd, ResourceTypeMember, built.TenantID,
			audit.OutcomeSuccess, "", s.addAttrs(txCtx, built, reactivated)); emitErr != nil {
			return emitErr
		}
		member = built
		return nil
	})
	if err != nil {
		// 席位拒绝独立留痕（best-effort）：拒绝本身是运营信号——
		// 连续被拒说明该提配额了；但不允许审计失败把 409 变成 500。
		var seatErr *domain.SeatLimitError
		if errors.As(err, &seatErr) {
			_ = s.audit.emit(ctx, in.Actor, ActionMemberAdd, ResourceTypeMember, in.TenantID,
				audit.OutcomeFailure, err.Error(), map[string]string{
					"subject_id":     in.SubjectID,
					"role":           string(in.Role),
					"active_members": strconv.Itoa(seatErr.Active),
					"seat_limit":     strconv.FormatInt(seatErr.Limit, 10),
				})
		}
		return domain.Member{}, translateIfNeeded(err)
	}
	return member, nil
}

// addAttrs 组装成员添加的审计属性；席位上下文读取失败不阻断添加
// （审计属性是补充信息，席位本身已在写入前校验过）。
func (s *MemberService) addAttrs(ctx context.Context, member domain.Member, reactivated bool) map[string]string {
	attrs := map[string]string{
		"subject_id":  member.SubjectID,
		"role":        string(member.Role),
		"reactivated": boolString(reactivated),
	}
	if active, err := s.members.CountActive(ctx, member.TenantID); err == nil {
		attrs["active_members"] = strconv.Itoa(active)
	}
	if limit := seatLimitOf(ctx, s.quotas, member.TenantID); limit != nil {
		attrs["seat_limit"] = strconv.FormatInt(*limit, 10)
	}
	return attrs
}

// ensureSeatAvailable 校验席位：在册数 +1 后不得超过 user 配额（T-13）。
//
// 未配置 user 配额视为不限（与 ENT-14「缺行按默认兜底」的口径一致，
// 但本切片不预置默认值——套餐物化保证新建租户都有该行）。
// 必须在 Mutate 临界区内调用：并发添加依赖租户行锁串行化。
func (s *MemberService) ensureSeatAvailable(ctx context.Context, tenantID string) error {
	return checkSeat(ctx, s.quotas, s.members, tenantID)
}

// checkSeat 是席位校验的包级实现，成员添加与建租户落 owner 共用。
func checkSeat(ctx context.Context, quotas domain.QuotaRepository, members domain.MemberRepository, tenantID string) error {
	limit := seatLimitOf(ctx, quotas, tenantID)
	if limit == nil {
		return nil
	}
	active, err := members.CountActive(ctx, tenantID)
	if err != nil {
		return err
	}
	if int64(active)+1 > *limit {
		return &domain.SeatLimitError{Active: active, Limit: *limit}
	}
	return nil
}

// seatLimitOf 读取席位上限；未配置或读取失败返回 nil（=不限）。
func seatLimitOf(ctx context.Context, quotas domain.QuotaRepository, tenantID string) *int64 {
	if quotas == nil {
		return nil
	}
	listed, err := quotas.ListQuotas(ctx, tenantID)
	if err != nil {
		return nil
	}
	for _, quota := range listed {
		if quota.Resource == ResourceUser {
			if quota.Unlimited() {
				return nil
			}
			value := quota.Limit
			return &value
		}
	}
	return nil
}

// List 分页列出租户成员（T-13）；limit<=0 由仓储按全量处理。
func (s *MemberService) List(ctx context.Context, tenantID string, includeDisabled bool, limit, offset int) ([]domain.Member, int, error) {
	if _, err := s.tenants.Get(ctx, tenantID); err != nil {
		return nil, 0, translate(err)
	}
	members, total, err := s.members.ListMembers(ctx, tenantID, includeDisabled, limit, offset)
	if err != nil {
		return nil, 0, translate(err)
	}
	return members, total, nil
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
// 四条约束：
//   - 先校验全部入参再落库，避免「角色改了、状态非法」的半提交；
//   - 读当前状态、计数、写入全部在 MemberRepository.Mutate 的临界区内完成，
//     否则两个 owner 并发降级会都读到 count=2 而全部通过；
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

	var (
		current       domain.Member
		next          domain.Member
		roleChanged   bool
		statusChanged bool
	)
	err := s.members.Mutate(ctx, in.TenantID, func(txCtx context.Context) error {
		// 锁内重新读租户与成员：锁外读到的状态可能已被并发请求改掉。
		if _, err := mutableTenant(txCtx, s.tenants, in.TenantID); err != nil {
			return err
		}
		loaded, err := s.members.GetMember(txCtx, in.TenantID, in.SubjectID)
		if err != nil {
			return err
		}
		current = loaded
		roleChanged = in.Role != nil && *in.Role != current.Role
		statusChanged = in.Status != nil && *in.Status != current.Status
		if !roleChanged && !statusChanged {
			// 幂等：无实际变化不写库、不写审计，避免审计里出现空变更。
			next = current
			return nil
		}

		next = current
		if roleChanged {
			if next, err = next.WithRole(*in.Role, s.now()); err != nil {
				return err
			}
		}
		if statusChanged {
			if next, err = next.WithStatus(*in.Status, s.now()); err != nil {
				return err
			}
		}
		if err := s.ensureOwnerRemains(txCtx, current, next); err != nil {
			return err
		}
		if err := s.members.UpsertMember(txCtx, next); err != nil {
			return err
		}
		// T-18（ADR-0007）：成员状态 / 角色变更同步 IAM 模板绑定；
		// 停用成员解绑（role=nil），在册成员绑定到目标角色。同事务 fail closed。
		var targetRole *domain.MemberRole
		if next.Active() {
			targetRole = &next.Role
		}
		if syncErr := s.rolesync.SyncRole(txCtx, next.TenantID, next.SubjectID, targetRole); syncErr != nil {
			return apperr.Internal("sync member role binding failed", syncErr)
		}
		if roleChanged {
			if err := s.audit.emit(txCtx, in.Actor, ActionMemberRoleChange, ResourceTypeMember, next.TenantID,
				audit.OutcomeSuccess, "", map[string]string{
					"subject_id": next.SubjectID,
					"from":       string(current.Role),
					"to":         string(next.Role),
				}); err != nil {
				return err
			}
		}
		if statusChanged {
			action := ActionMemberDisable
			if next.Active() {
				action = ActionMemberEnable
			}
			if err := s.audit.emit(txCtx, in.Actor, action, ResourceTypeMember, next.TenantID,
				audit.OutcomeSuccess, in.Reason, map[string]string{
					"subject_id": next.SubjectID,
					"role":       string(next.Role),
				}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Member{}, translateIfNeeded(err)
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

// isInvalidEnum 判断是否为枚举 / 格式类错误（角色 / 状态 / 资源键 / 编码 / 元数据），统一按 400 处理。
func isInvalidEnum(err error) bool {
	var invalidRole *domain.InvalidRoleError
	var invalidStatus *domain.InvalidStatusError
	var invalidResource *domain.InvalidResourceError
	var invalidCode *domain.InvalidCodeError
	var fieldTooLong *domain.FieldTooLongError
	var invalidEmail *domain.InvalidEmailError
	var invalidField *domain.InvalidFieldError
	return errors.As(err, &invalidRole) || errors.As(err, &invalidStatus) || errors.As(err, &invalidResource) ||
		errors.As(err, &invalidCode) || errors.As(err, &fieldTooLong) ||
		errors.As(err, &invalidEmail) || errors.As(err, &invalidField)
}
