package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 邀请相关审计动作与资源类型（docs/audit/EVENTS.md 的 tenant.* 段）。
const (
	// ActionInvitationCreate 创建邀请。
	ActionInvitationCreate = "tenant.invitation.create"
	// ActionInvitationAccept 接受邀请（成员关系建立）。
	ActionInvitationAccept = "tenant.invitation.accept"
	// ActionInvitationRevoke 撤销邀请。
	ActionInvitationRevoke = "tenant.invitation.revoke"
	// ActionInvitationResend 重发邀请（作废旧 token、顺延有效期）。
	ActionInvitationResend = "tenant.invitation.resend"
	// ResourceTypeInvitation 邀请资源类型。
	ResourceTypeInvitation = "tenant_invitation"
)

// InvitationService 编排成员邀请用例（T-14）。
//
// 邮件投递依赖 ENT-10，未就绪前的明确语义是「只落库不发送」：
// 明文 token 仅在创建 / 重发响应返回一次，由运营线下转交。
type InvitationService struct {
	tenants     domain.Repository
	members     domain.MemberRepository
	invitations domain.InvitationRepository
	quotas      domain.QuotaRepository
	audit       auditContext
	ids         id.Generator
	now         func() time.Time
	ttl         time.Duration
}

// NewInvitationService 构造邀请用例；依赖均必填（配额仓储可空，见 Options.Quotas）。
func NewInvitationService(tenants domain.Repository, members domain.MemberRepository,
	invitations domain.InvitationRepository, ids id.Generator, emitter audit.Emitter,
	opts Options) (*InvitationService, error) {
	switch {
	case tenants == nil:
		return nil, apperr.Internal("tenant invitation service requires a tenant repository", nil)
	case members == nil:
		return nil, apperr.Internal("tenant invitation service requires a member repository", nil)
	case invitations == nil:
		return nil, apperr.Internal("tenant invitation service requires an invitation repository", nil)
	case ids == nil:
		return nil, apperr.Internal("tenant invitation service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("tenant invitation service requires an audit emitter", nil)
	}
	ttl := opts.InvitationTTL
	if ttl <= 0 {
		ttl = domain.DefaultInvitationTTL
	}
	now := time.Now
	return &InvitationService{
		tenants:     tenants,
		members:     members,
		invitations: invitations,
		quotas:      opts.Quotas,
		audit:       auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:         ids,
		now:         now,
		ttl:         ttl,
	}, nil
}

// CreateInvitationInput 是创建邀请入参。
type CreateInvitationInput struct {
	// TenantID 目标租户。
	TenantID string
	// Email 被邀请人邮箱（投递目标；ENT-10 就绪前仅记录）。
	Email string
	// Role 受邀角色。
	Role domain.MemberRole
	// Actor 操作者。
	Actor Actor
}

// AcceptInvitationInput 是接受邀请入参。
type AcceptInvitationInput struct {
	// Token 创建 / 重发响应中下发的一次性明文 token。
	Token string
	// SubjectID 实际接受邀请的主体（成员关系的键；与 email 解耦）。
	SubjectID string
	// Actor 操作者（运营代客户执行或集成方调用）。
	Actor Actor
}

// RevokeInvitationInput 是撤销邀请入参。
type RevokeInvitationInput struct {
	// TenantID 租户 ID。
	TenantID string
	// InvitationID 邀请 ID。
	InvitationID string
	// Reason 撤销原因，写入审计。
	Reason string
	// Actor 操作者。
	Actor Actor
}

// ResendInvitationInput 是重发邀请入参。
type ResendInvitationInput struct {
	// TenantID 租户 ID。
	TenantID string
	// InvitationID 邀请 ID。
	InvitationID string
	// Actor 操作者。
	Actor Actor
}

// InvitationAction 是一次接受的结果。
type InvitationAction struct {
	// Member 建立（或复活）的成员关系。
	Member domain.Member
	// Invitation 邀请终态。
	Invitation domain.Invitation
	// Idempotent 表示本次接受为重复接受（邀请与成员此前已生效）。
	Idempotent bool
}

// Create 创建邀请；同租户同邮箱的 pending 邀请唯一（重复邀请走 Resend）。
//
// 返回值里的明文 token 是唯一一次下发机会（库里只有摘要）。
func (s *InvitationService) Create(ctx context.Context, in CreateInvitationInput) (domain.Invitation, string, error) {
	var (
		invitation domain.Invitation
		plaintext  string
	)
	err := s.tenants.WithinTx(ctx, func(txCtx context.Context) error {
		if _, err := mutableTenant(txCtx, s.tenants, in.TenantID); err != nil {
			return err
		}
		invitationID, err := s.ids.NextString()
		if err != nil {
			return apperr.Internal("invitation id generation failed", err)
		}
		token, hash, err := domain.NewInviteToken()
		if err != nil {
			return apperr.Internal("invitation token generation failed", err)
		}
		built, err := domain.NewInvitation(invitationID, in.TenantID, in.Email, in.Role,
			in.Actor.SubjectID, hash, s.ttl, s.now())
		if err != nil {
			return translate(err)
		}
		if err := s.invitations.CreateInvitation(txCtx, built); err != nil {
			return translate(err)
		}
		if err := s.audit.emit(txCtx, in.Actor, ActionInvitationCreate, ResourceTypeInvitation, built.ID,
			audit.OutcomeSuccess, "", map[string]string{
				"tenant_id":  built.TenantID,
				"email":      built.Email,
				"role":       string(built.Role),
				"expires_at": built.ExpiresAt.UTC().Format(time.RFC3339),
			}); err != nil {
			return err
		}
		invitation = built
		plaintext = token
		return nil
	})
	if err != nil {
		return domain.Invitation{}, "", translateIfNeeded(err)
	}
	return invitation, plaintext, nil
}

// Accept 接受邀请并建立成员关系。
//
// 四条语义：
//   - token 无效 / 非法统一 400（不区分「不存在」与「格式错误」，防枚举）；
//   - 过期在受领时惰性判定并回写 expired——没有定时扫描任务也能保证
//     「过期邀请不可用」（T-14 验收）；
//   - 重复接受幂等：同一主体重复接受返回既有成员关系，换人抢受返回 409；
//   - 成员写入复用席位校验（T-13）与复活语义，接受与成员落库、审计同事务。
func (s *InvitationService) Accept(ctx context.Context, in AcceptInvitationInput) (InvitationAction, error) {
	var result InvitationAction
	err := s.tenants.WithinTx(ctx, func(txCtx context.Context) error {
		current, err := s.loadByToken(txCtx, in.Token)
		if err != nil {
			return err
		}

		// 快路径：同一主体重复接受 → 幂等返回既有成员关系。
		if current.Status == domain.InvitationAccepted && current.AcceptedBy == strings.TrimSpace(in.SubjectID) {
			member, memberErr := s.members.GetMember(txCtx, current.TenantID, in.SubjectID)
			if memberErr != nil {
				return memberErr
			}
			result = InvitationAction{Member: member, Invitation: current, Idempotent: true}
			return nil
		}
		if current.Status.Terminal() {
			if current.Status == domain.InvitationAccepted {
				// 已被其他主体接受：token 是共享凭证，抢受必须显式失败。
				return domain.ErrInvitationAlreadyAccepted
			}
			return domain.ErrInvitationNotPending
		}
		if current.Expired(s.now()) {
			// 惰性回写不能在事务内做：拒绝路径整体回滚会把它一起吞掉。
			// 这里只报错，回写由事务外的 best-effort 完成（见 Accept 尾部）。
			return &lazyExpire{invitation: current}
		}

		if _, err := mutableTenant(txCtx, s.tenants, current.TenantID); err != nil {
			return err
		}
		if err := checkSeat(txCtx, s.quotas, s.members, current.TenantID); err != nil {
			return err
		}

		built, err := domain.NewMember(current.TenantID, in.SubjectID, current.Role, s.now())
		if err != nil {
			return translate(err)
		}
		reactivated := false
		existing, memberErr := s.members.GetMember(txCtx, current.TenantID, in.SubjectID)
		switch {
		case memberErr == nil && existing.Active():
			// 成员已在（此前手动添加）：接受动作仍有效，成员关系幂等。
			built = existing
		case memberErr == nil:
			// 已停用 → 复活，沿用首次加入时间（与 Add 的复活语义一致）。
			built.CreatedAt = existing.CreatedAt
			reactivated = true
		case errors.Is(memberErr, domain.ErrMemberNotFound):
			// 正常新增路径。
		default:
			return memberErr
		}
		if err := s.members.UpsertMember(txCtx, built); err != nil {
			return translate(err)
		}

		acceptedAt := s.now().UTC()
		next := current
		next.Status = domain.InvitationAccepted
		next.AcceptedBy = strings.TrimSpace(in.SubjectID)
		next.AcceptedAt = &acceptedAt
		next.UpdatedAt = acceptedAt
		if err := s.invitations.CompleteInvitation(txCtx, next); err != nil {
			if !errors.Is(err, domain.ErrInvitationNotPending) {
				return err
			}
			// 并发被处理：重读按幂等 / 冲突分流。
			reread, readErr := s.invitations.GetInvitation(txCtx, current.TenantID, current.ID)
			if readErr != nil {
				return readErr
			}
			if reread.Status == domain.InvitationAccepted && reread.AcceptedBy == strings.TrimSpace(in.SubjectID) {
				member, getErr := s.members.GetMember(txCtx, current.TenantID, in.SubjectID)
				if getErr != nil {
					return getErr
				}
				result = InvitationAction{Member: member, Invitation: reread, Idempotent: true}
				return nil
			}
			return domain.ErrInvitationAlreadyAccepted
		}

		if err := s.audit.emit(txCtx, in.Actor, ActionInvitationAccept, ResourceTypeInvitation, next.ID,
			audit.OutcomeSuccess, "", map[string]string{
				"tenant_id":  next.TenantID,
				"email":      next.Email,
				"role":       string(next.Role),
				"subject_id": strings.TrimSpace(in.SubjectID),
			}); err != nil {
			return err
		}
		// 成员建立本身也要按 member.add 留痕：与手动添加同一动作语义，
		// source=invitation 区分入口（AUD-03：成员关系变更必录）。
		if err := s.audit.emit(txCtx, in.Actor, ActionMemberAdd, ResourceTypeMember, next.TenantID,
			audit.OutcomeSuccess, "", map[string]string{
				"subject_id":  built.SubjectID,
				"role":        string(built.Role),
				"source":      "invitation",
				"reactivated": boolString(reactivated),
			}); err != nil {
			return err
		}
		result = InvitationAction{Member: built, Invitation: next}
		return nil
	})
	if err != nil {
		// 过期邀请的惰性回写独立提交（best-effort）：拒绝本身已定，回写失败
		// 不影响 409 响应；下次接受会再次触发判定。
		var le *lazyExpire
		if errors.As(err, &le) {
			expired := le.invitation
			expired.Status = domain.InvitationExpired
			expired.UpdatedAt = s.now().UTC()
			_ = s.invitations.CompleteInvitation(ctx, expired)
		}
		return InvitationAction{}, translateIfNeeded(err)
	}
	return result, nil
}

// lazyExpire 标记「接受被拒：邀请已过期」，携带待回写的邀请，
// 由事务外的 best-effort 落库（见 Accept）。
type lazyExpire struct {
	invitation domain.Invitation
}

func (e *lazyExpire) Error() string { return domain.ErrInvitationExpired.Error() }
func (e *lazyExpire) Unwrap() error { return domain.ErrInvitationExpired }

// Revoke 撤销待处理邀请；已处理的邀请不可撤销（终态不可逆）。
func (s *InvitationService) Revoke(ctx context.Context, in RevokeInvitationInput) (domain.Invitation, error) {
	var invitation domain.Invitation
	err := s.tenants.WithinTx(ctx, func(txCtx context.Context) error {
		current, err := s.invitations.GetInvitation(txCtx, in.TenantID, in.InvitationID)
		if err != nil {
			return err
		}
		if current.Status.Terminal() {
			return domain.ErrInvitationNotPending
		}
		next := current
		next.Status = domain.InvitationRevoked
		next.UpdatedAt = s.now().UTC()
		if err := s.invitations.CompleteInvitation(txCtx, next); err != nil {
			return err
		}
		if err := s.audit.emit(txCtx, in.Actor, ActionInvitationRevoke, ResourceTypeInvitation, next.ID,
			audit.OutcomeSuccess, in.Reason, map[string]string{
				"tenant_id": next.TenantID,
				"email":     next.Email,
				"role":      string(next.Role),
			}); err != nil {
			return err
		}
		invitation = next
		return nil
	})
	if err != nil {
		return domain.Invitation{}, translateIfNeeded(err)
	}
	return invitation, nil
}

// Resend 重发邀请：作废旧 token、生成新 token、顺延有效期。
//
// 旧 token 立即失效（摘要被覆盖）——转交链路怀疑泄露时，resend 也是轮换手段。
func (s *InvitationService) Resend(ctx context.Context, in ResendInvitationInput) (domain.Invitation, string, error) {
	var (
		invitation domain.Invitation
		plaintext  string
	)
	err := s.tenants.WithinTx(ctx, func(txCtx context.Context) error {
		current, err := s.invitations.GetInvitation(txCtx, in.TenantID, in.InvitationID)
		if err != nil {
			return err
		}
		if current.Status.Terminal() {
			return domain.ErrInvitationNotPending
		}
		token, hash, err := domain.NewInviteToken()
		if err != nil {
			return apperr.Internal("invitation token generation failed", err)
		}
		next := current
		next.TokenHash = hash
		next.ExpiresAt = s.now().UTC().Add(s.ttl)
		next.UpdatedAt = s.now().UTC()
		if err := s.invitations.RefreshInvitation(txCtx, next); err != nil {
			return translate(err)
		}
		if err := s.audit.emit(txCtx, in.Actor, ActionInvitationResend, ResourceTypeInvitation, next.ID,
			audit.OutcomeSuccess, "", map[string]string{
				"tenant_id":  next.TenantID,
				"email":      next.Email,
				"role":       string(next.Role),
				"expires_at": next.ExpiresAt.UTC().Format(time.RFC3339),
			}); err != nil {
			return err
		}
		invitation = next
		plaintext = token
		return nil
	})
	if err != nil {
		return domain.Invitation{}, "", translateIfNeeded(err)
	}
	return invitation, plaintext, nil
}

// List 分页列出租户邀请；status 为空表示全部，非法枚举 400。
func (s *InvitationService) List(ctx context.Context, tenantID, status string, limit, offset int) ([]domain.Invitation, int, error) {
	if status != "" && !domain.InvitationStatus(status).Valid() {
		return nil, 0, apperr.BadRequest("status must be one of pending, accepted, revoked, expired")
	}
	if _, err := s.tenants.Get(ctx, tenantID); err != nil {
		return nil, 0, translate(err)
	}
	invitations, total, err := s.invitations.ListInvitations(ctx, tenantID, status, limit, offset)
	if err != nil {
		return nil, 0, translate(err)
	}
	return invitations, total, nil
}

// loadByToken 加载并校验 token：无效 / 非法统一 400，不区分内部原因（防枚举）。
func (s *InvitationService) loadByToken(ctx context.Context, token string) (domain.Invitation, error) {
	if strings.TrimSpace(token) == "" {
		return domain.Invitation{}, apperr.BadRequest("invalid or expired invitation token")
	}
	invitation, err := s.invitations.FindByTokenHash(ctx, domain.HashInviteToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return domain.Invitation{}, apperr.BadRequest("invalid or expired invitation token")
		}
		return domain.Invitation{}, err
	}
	if err := invitation.Validate(); err != nil {
		return domain.Invitation{}, apperr.Internal("invitation data invalid", err)
	}
	return invitation, nil
}
