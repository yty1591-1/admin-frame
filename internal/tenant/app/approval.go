package app

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// 审批审计动作（docs/audit/EVENTS.md 的 tenant.approval.* 段）。
const (
	// ActionApprovalSubmit 提交入驻申请。
	ActionApprovalSubmit = "tenant.approval.submit"
	// ActionApprovalApprove 入驻审核通过。
	ActionApprovalApprove = "tenant.approval.approve"
	// ActionApprovalReject 入驻审核驳回。
	ActionApprovalReject = "tenant.approval.reject"
)

// ApprovalInput 是审批动作入参。
type ApprovalInput struct {
	// TenantID 租户 ID。
	TenantID string
	// Note 申请说明或审批意见；驳回必填。
	Note string
	// Actor 操作者（提交人 / 审批人）。
	Actor Actor
}

// ApprovalView 是审批详情（当前态 + 最近一轮事件）。
type ApprovalView struct {
	// TenantID 租户 ID。
	TenantID string
	// TenantStatus 租户当前生命周期状态（审批通过不等于已开通）。
	TenantStatus domain.Status
	// Status 审批派生状态：draft / submitted / approved / rejected。
	Status domain.ApprovalStatus
	// Submitter 当前这一轮的申请人。
	Submitter string
	// Latest 最新一条事件；draft 时为零值。
	Latest domain.Approval
	// History 最近一轮相关事件（时间正序）。
	History []domain.Approval
}

// GetApproval 读取租户的审批态。
func (s *Service) GetApproval(ctx context.Context, tenantID string) (ApprovalView, error) {
	tenant, err := s.repo.Get(ctx, tenantID)
	if err != nil {
		return ApprovalView{}, translate(err)
	}
	decision, err := s.loadApproval(ctx, tenant.ID)
	if err != nil {
		return ApprovalView{}, translate(err)
	}
	return approvalView(tenant, decision), nil
}

// SubmitApproval 提交（或重新提交）入驻申请。
//
// 允许的状态：draft（首次）、rejected（驳回后补齐材料重提）。
// 已提交等待裁决返回 409，已通过返回 409——避免「重复提交刷掉审批意见」。
func (s *Service) SubmitApproval(ctx context.Context, in ApprovalInput) (ApprovalView, error) {
	return s.recordApproval(ctx, in, domain.ApprovalActionSubmit)
}

// ApproveApproval 审核通过；通过后仍需满足「≥1 名在册 owner」才能开通。
func (s *Service) ApproveApproval(ctx context.Context, in ApprovalInput) (ApprovalView, error) {
	return s.recordApproval(ctx, in, domain.ApprovalActionApprove)
}

// RejectApproval 审核驳回，原因必填；驳回后可修改材料重新提交。
func (s *Service) RejectApproval(ctx context.Context, in ApprovalInput) (ApprovalView, error) {
	return s.recordApproval(ctx, in, domain.ApprovalActionReject)
}

// recordApproval 是三种审批动作的公共流程：读租户 → 派生当前态 → 校验 → 追加事件 + 审计（同事务）。
//
// 并发说明：两个审批人同时点「通过」会各写一条 approve 事件（当前态仍是 approved），
// 结果一致、审计更完整，因此不加行锁；同时「通过 + 驳回」按事件 id 决出最终态，
// 两次动作都可追溯。真正需要串行的是开通（Transition 内校验最新态）。
func (s *Service) recordApproval(ctx context.Context, in ApprovalInput, action domain.ApprovalAction) (ApprovalView, error) {
	tenantID := strings.TrimSpace(in.TenantID)
	if tenantID == "" {
		return ApprovalView{}, apperr.BadRequest("tenant id is required")
	}
	event, err := domain.NewApproval(tenantID, action, in.Actor.SubjectID, in.Note, s.now())
	if err != nil {
		return ApprovalView{}, translate(err)
	}

	var tenant domain.Tenant
	var decision domain.ApprovalDecision
	err = s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		current, getErr := s.repo.Get(txCtx, tenantID)
		if getErr != nil {
			return getErr
		}
		if current.Status.Terminal() {
			return domain.ErrTerminated
		}
		// 事务内派生：锁外读到的审批态可能已被并发动作改掉。
		latest, deriveErr := s.loadApproval(txCtx, tenantID)
		if deriveErr != nil {
			return deriveErr
		}
		attrs := approvalAttrs(action)
		if checkErr := checkApprovalAction(action, latest, event.ActorID); checkErr != nil {
			if !errors.Is(checkErr, domain.ErrApprovalSelfReview) || !authz.IsSystemAdmin(txCtx) {
				return checkErr
			}
			attrs["sod_bypass"] = authz.RoleCodeSystemAdmin
		}
		if appendErr := s.approvals.AppendApproval(txCtx, event); appendErr != nil {
			return appendErr
		}
		if emitErr := s.emitWithAttrs(txCtx, in.Actor, approvalActionName(action), current,
			audit.OutcomeSuccess, event.Note, attrs); emitErr != nil {
			return emitErr
		}
		tenant = current
		// 追加后再派生一次：响应里的状态必须是落库后的事实。
		after, afterErr := s.loadApproval(txCtx, tenantID)
		if afterErr != nil {
			return afterErr
		}
		decision = after
		return nil
	})
	if err != nil {
		// 职责分离被拒是安全事件，留一条失败审计（其余校验失败是普通 409/400）。
		if errors.Is(err, domain.ErrApprovalSelfReview) {
			if current, getErr := s.repo.Get(ctx, tenantID); getErr == nil {
				_ = s.emitWithAttrs(ctx, in.Actor, approvalActionName(action), current,
					audit.OutcomeFailure, err.Error(), approvalAttrs(action))
			}
		}
		return ApprovalView{}, translateIfNeeded(err)
	}
	return approvalView(tenant, decision), nil
}

// checkApprovalAction 施加三种动作各自的前置条件。
func checkApprovalAction(action domain.ApprovalAction, decision domain.ApprovalDecision, actorID string) error {
	switch action {
	case domain.ApprovalActionSubmit:
		return decision.EnsureSubmittable()
	case domain.ApprovalActionApprove, domain.ApprovalActionReject:
		if err := decision.EnsureDecidable(); err != nil {
			return err
		}
		return decision.EnsureNotSelfReview(actorID)
	default:
		return errors.New("tenant: unsupported approval action")
	}
}

// approvalActionName 把动作映射为审计 action。
func approvalActionName(action domain.ApprovalAction) string {
	switch action {
	case domain.ApprovalActionSubmit:
		return ActionApprovalSubmit
	case domain.ApprovalActionApprove:
		return ActionApprovalApprove
	case domain.ApprovalActionReject:
		return ActionApprovalReject
	default:
		return ActionApprovalSubmit
	}
}

// approvalAttrs 补充审批动作到审计属性，便于按动作筛选事件。
func approvalAttrs(action domain.ApprovalAction) map[string]string {
	return map[string]string{"approval_action": string(action)}
}

// loadApproval 读取事件并派生当前态。
func (s *Service) loadApproval(ctx context.Context, tenantID string) (domain.ApprovalDecision, error) {
	events, err := s.approvals.ListRecentApprovals(ctx, tenantID, domain.MaxApprovalHistory())
	if err != nil {
		return domain.ApprovalDecision{}, err
	}
	// 仓储按 id 倒序返回；派生需要时间正序，这里反转。
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return domain.DeriveApproval(events), nil
}

// ensureApproved 是开通前置：pending → active 必须已通过入驻审核。
func (s *Service) ensureApproved(ctx context.Context, tenantID string) error {
	decision, err := s.loadApproval(ctx, tenantID)
	if err != nil {
		return err
	}
	if !decision.Approved() {
		return domain.ErrApprovalRequired
	}
	return nil
}

// approvalView 组装详情响应。
func approvalView(tenant domain.Tenant, decision domain.ApprovalDecision) ApprovalView {
	return ApprovalView{
		TenantID:     tenant.ID,
		TenantStatus: tenant.Status,
		Status:       decision.Status,
		Submitter:    decision.Submitter,
		Latest:       decision.Latest,
		History:      decision.History,
	}
}

// memoryApprovalRepository 是审批仓储的进程内兜底实现。
//
// 用途：Options.Approvals 未注入时（单测、最小进程形态）仍保持「无审批记录
// 不能开通」的 fail-closed 语义，而不是让开通前置静默失效。多副本生产环境
// 必须注入持久化实现，否则审批记录只存在于单个进程内。
type memoryApprovalRepository struct {
	mu     sync.Mutex
	events map[string][]domain.Approval
	seq    int64
}

func newMemoryApprovalRepository() *memoryApprovalRepository {
	return &memoryApprovalRepository{events: make(map[string][]domain.Approval)}
}

// AppendApproval 追加事件（进程内实现）。
func (r *memoryApprovalRepository) AppendApproval(_ context.Context, approval domain.Approval) error {
	if err := approval.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	approval.ID = r.seq
	r.events[approval.TenantID] = append(r.events[approval.TenantID], approval)
	return nil
}

// ListRecentApprovals 按时间倒序返回最近事件（进程内实现）。
func (r *memoryApprovalRepository) ListRecentApprovals(_ context.Context, tenantID string, limit int) ([]domain.Approval, error) {
	if limit <= 0 {
		limit = domain.MaxApprovalHistory()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := r.events[tenantID]
	out := make([]domain.Approval, 0, limit)
	for i := len(stored) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, stored[i])
	}
	return out, nil
}

// 编译期断言：兜底实现满足契约。
var _ domain.ApprovalRepository = (*memoryApprovalRepository)(nil)
