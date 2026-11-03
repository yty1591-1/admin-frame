// Package app 编排租户生命周期用例（ENT-09）。
//
// 职责边界：状态机在 domain，持久化在 infrastructure；本包只做
// 「读 → 迁移 → CAS 写 → 审计」的编排，并把领域错误翻译为统一错误码。
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

// 审计动作（docs/audit/EVENTS.md 的 tenant.* 段）。
const (
	// ActionCreate 创建租户。
	ActionCreate = "tenant.create"
	// ActionActivate 开通（pending → active）。
	ActionActivate = "tenant.activate"
	// ActionSuspend 停用（active → suspended）。
	ActionSuspend = "tenant.suspend"
	// ActionResume 恢复（suspended → active）。
	ActionResume = "tenant.resume"
	// ActionTerminate 终止（→ terminated，终态）。
	ActionTerminate = "tenant.terminate"
)

// TopicAudit 租户审计事件的 Outbox 主题。
const TopicAudit = "tenant.audit"

// 审计资源类型。
const (
	// ResourceTypeTenant 租户主数据。
	ResourceTypeTenant = "tenant"
	// ResourceTypeMember 租户成员关系。
	ResourceTypeMember = "tenant_member"
	// ResourceTypeQuota 租户配额。
	ResourceTypeQuota = "tenant_quota"
)

// auditContext 封装审计写入所需的依赖，供租户 / 成员 / 配额三类用例复用。
//
// 状态与成员变更都必须留痕（AUD-03）；把写入收敛到一处，避免某个新用例
// 忘了传 service 或 TraceID 导致事件被 Validate 拒绝后静默丢失。
type auditContext struct {
	emitter audit.Emitter
	ids     id.Generator
	service string
	now     func() time.Time
}

// emit 写一条审计事件。
//
// 返回错误而不是吞掉：与业务写入同事务时（Outbox），失败必须让业务回滚，
// 否则就回到「状态已改、审计丢失」。需要 best-effort 的调用方显式忽略返回值。
func (a auditContext) emit(ctx context.Context, actor Actor, action, resourceType, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) error {
	eventID := ""
	if a.ids != nil {
		if generated, err := a.ids.NextString(); err == nil {
			eventID = generated
		}
	}
	if eventID == "" {
		// 生成失败也必须有事件 ID，否则整条审计会被 Validate 拒绝而静默丢失。
		eventID = "tenant-" + a.now().UTC().Format("20060102150405.000000000")
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actor.SubjectID,
		ActorType:    actor.ActorType,
		Channel:      defaultChannel(actor.Channel),
		MerchantID:   actor.MerchantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   a.now().UTC(),
		TraceID:      actor.TraceID,
		Service:      a.service,
		Reason:       truncate(reason, 255),
		Attrs:        attrs,
	}
	return a.emitter.Emit(ctx, event)
}

// Options 是用例参数。
type Options struct {
	// DefaultPageSize 列表默认每页条数。
	DefaultPageSize int
	// MaxPageSize 列表每页上限；超过按上限收敛而不是报错。
	MaxPageSize int
	// Service 服务名，写入审计事件。
	Service string
}

// Actor 是操作者信息，来自 Principal 与请求上下文。
type Actor struct {
	// SubjectID 操作者主体 ID。
	SubjectID string
	// ActorType 操作者类型（user / service）。
	ActorType string
	// Channel 端别（admin / biz）。
	Channel string
	// MerchantID B 端商户；A 端为空。
	MerchantID string
	// TraceID 请求追踪 ID。
	TraceID string
}

// CreateInput 是创建租户入参。
type CreateInput struct {
	// Name 租户名称。
	Name string
	// Actor 操作者。
	Actor Actor
}

// TransitionInput 是状态迁移入参。
type TransitionInput struct {
	// ID 租户 ID。
	ID string
	// Target 目标状态。
	Target domain.Status
	// Reason 迁移原因；停用与终止必填。
	Reason string
	// Actor 操作者。
	Actor Actor
}

// ListResult 是分页结果。
type ListResult struct {
	// Items 当页租户。
	Items []domain.Tenant
	// Total 满足条件的总数。
	Total int
}

// Service 是租户生命周期用例。
type Service struct {
	repo  domain.Repository
	ids   id.Generator
	audit auditContext
	now   func() time.Time
	opts  Options
}

// NewService 构造服务；repo / ids / emitter 必填。
//
// emitter 不接受 nil：状态迁移是审计必录动作（AUD-03），缺审计出口时
// 宁可启动失败，也不要出现「停用了但查不到是谁停的」。
func NewService(repo domain.Repository, ids id.Generator, emitter audit.Emitter, opts Options) (*Service, error) {
	if repo == nil {
		return nil, apperr.Internal("tenant service requires a repository", nil)
	}
	if ids == nil {
		return nil, apperr.Internal("tenant service requires an id generator", nil)
	}
	if emitter == nil {
		return nil, apperr.Internal("tenant service requires an audit emitter", nil)
	}
	if opts.DefaultPageSize <= 0 {
		opts.DefaultPageSize = 20
	}
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = 100
	}
	if opts.MaxPageSize < opts.DefaultPageSize {
		opts.MaxPageSize = opts.DefaultPageSize
	}
	now := time.Now
	return &Service{
		repo:  repo,
		ids:   ids,
		audit: auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		now:   now,
		opts:  opts,
	}, nil
}

// Create 创建待开通租户。
//
// 建租户与审计同事务：审计写不进去就不建租户，避免「有租户、没记录」。
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Tenant, error) {
	var tenant domain.Tenant
	err := s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		tenantID, idErr := s.ids.NextString()
		if idErr != nil {
			return apperr.Internal("tenant id generation failed", idErr)
		}
		built, buildErr := domain.NewTenant(tenantID, in.Name, s.now())
		if buildErr != nil {
			return buildErr
		}
		if createErr := s.repo.Create(txCtx, built); createErr != nil {
			return createErr
		}
		if emitErr := s.emit(txCtx, in.Actor, ActionCreate, built, audit.OutcomeSuccess, ""); emitErr != nil {
			return emitErr
		}
		tenant = built
		return nil
	})
	if err != nil {
		return domain.Tenant{}, translateIfNeeded(err)
	}
	return tenant, nil
}

// Get 读取租户。
func (s *Service) Get(ctx context.Context, tenantID string) (domain.Tenant, error) {
	tenant, err := s.repo.Get(ctx, tenantID)
	if err != nil {
		return domain.Tenant{}, translate(err)
	}
	return tenant, nil
}

// List 分页查询租户；limit 超过上限时按上限收敛。
func (s *Service) List(ctx context.Context, filter domain.Filter) (ListResult, error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return ListResult{}, apperr.BadRequest("status must be one of pending, active, suspended, terminated")
	}
	if filter.Limit <= 0 {
		filter.Limit = s.opts.DefaultPageSize
	}
	if filter.Limit > s.opts.MaxPageSize {
		filter.Limit = s.opts.MaxPageSize
	}
	items, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, translate(err)
	}
	return ListResult{Items: items, Total: total}, nil
}

// Transition 执行一次状态迁移。
//
// 成功路径：读当前状态、CAS 写入、写审计在**同一事务**内完成，任一步失败整体回滚。
// 失败路径：业务已回滚，拒绝事件在事务外 best-effort 记录（非法迁移本身有价值，
// 但不允许它把 409 变成 500）。
func (s *Service) Transition(ctx context.Context, in TransitionInput) (domain.Tenant, error) {
	if strings.TrimSpace(in.ID) == "" {
		return domain.Tenant{}, apperr.BadRequest("tenant id is required")
	}
	var next domain.Tenant
	err := s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		// 事务内重新读：锁外读到的状态可能已被并发请求改掉。
		current, getErr := s.repo.Get(txCtx, in.ID)
		if getErr != nil {
			return getErr
		}
		computed, transErr := current.Transition(in.Target, in.Reason, s.now())
		if transErr != nil {
			return transErr
		}
		if updateErr := s.repo.UpdateStatus(txCtx, computed); updateErr != nil {
			return updateErr
		}
		if emitErr := s.emit(txCtx, in.Actor, actionFor(current.Status, in.Target), computed,
			audit.OutcomeSuccess, in.Reason); emitErr != nil {
			return emitErr
		}
		next = computed
		return nil
	})
	if err != nil {
		s.emitRejected(ctx, in, err)
		return domain.Tenant{}, translateIfNeeded(err)
	}
	return next, nil
}

// emitRejected 记录被拒绝的迁移；事务已回滚，本事件必须独立落库。
//
// 只做 best-effort：拒绝事件丢失不应把「409 冲突」变成「500 故障」。
func (s *Service) emitRejected(ctx context.Context, in TransitionInput, cause error) {
	if errors.Is(cause, domain.ErrNotFound) {
		return // 没有资源可记，也不应凭空造一条事件。
	}
	current, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return
	}
	_ = s.emit(ctx, in.Actor, actionFor(current.Status, in.Target), current, audit.OutcomeFailure, cause.Error())
}

// actionFor 按「源状态 + 目标状态」区分开通与恢复。
//
// 两者目标都是 active，但审计语义不同：恢复意味着此前有一次停用，
// 合并成同一个 action 会让「停用了多久」无法从审计还原。
func actionFor(from, to domain.Status) string {
	if to == domain.StatusActive && from == domain.StatusSuspended {
		return ActionResume
	}
	switch to {
	case domain.StatusActive:
		return ActionActivate
	case domain.StatusSuspended:
		return ActionSuspend
	case domain.StatusTerminated:
		return ActionTerminate
	default:
		return ActionCreate
	}
}

// emit 写租户主数据审计事件。
func (s *Service) emit(ctx context.Context, actor Actor, action string, tenant domain.Tenant,
	outcome audit.Outcome, reason string) error {
	return s.audit.emit(ctx, actor, action, ResourceTypeTenant, tenant.ID, outcome, reason, map[string]string{
		"status": string(tenant.Status),
		"name":   tenant.Name,
	})
}

// defaultChannel 兜底端别，保证审计事件通过 Validate（channel 必填）。
func defaultChannel(channel string) string {
	trimmed := strings.TrimSpace(channel)
	if trimmed == "" {
		return "admin"
	}
	return trimmed
}

// truncate 按字节安全截断原因，避免超过审计 attrs 的可读长度。
func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// mutableTenant 读取租户并拒绝终态：已终止的租户不再接受任何变更。
//
// 只给写用例用。只读接口必须直接 tenants.Get —— 终止态租户的成员与配额
// 仍需要可查（取证、对账、客诉回溯），套上写守卫会让只读接口返回 409。
func mutableTenant(ctx context.Context, tenants domain.Repository, tenantID string) (domain.Tenant, error) {
	if strings.TrimSpace(tenantID) == "" {
		return domain.Tenant{}, apperr.BadRequest("tenant id is required")
	}
	tenant, err := tenants.Get(ctx, tenantID)
	if err != nil {
		return domain.Tenant{}, translate(err)
	}
	if tenant.Status.Terminal() {
		return domain.Tenant{}, apperr.Conflict("tenant is terminated and cannot be changed")
	}
	return tenant, nil
}

// translateIfNeeded 在已经带业务码时原样返回，避免二次包装成 500。
func translateIfNeeded(err error) error {
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		return err
	}
	return translate(err)
}

// translate 把领域错误翻译为统一错误码。
func translate(err error) error {
	var invalid *domain.InvalidTransitionError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound("tenant not found")
	case errors.Is(err, domain.ErrMemberNotFound):
		return apperr.NotFound("tenant member not found")
	case errors.As(err, &invalid):
		// 409：请求本身合法，但与当前资源状态冲突。
		return apperr.Conflict(invalid.Error())
	case errors.Is(err, domain.ErrTerminated):
		return apperr.Conflict("tenant is terminated and cannot be changed")
	case errors.Is(err, domain.ErrVersionConflict):
		return apperr.Conflict("tenant status changed concurrently, retry")
	case errors.Is(err, domain.ErrAlreadyExists):
		return apperr.Conflict("tenant already exists")
	case errors.Is(err, domain.ErrMemberAlreadyExists):
		return apperr.Conflict("member is already active in this tenant")
	case errors.Is(err, domain.ErrLastOwner):
		return apperr.Conflict(err.Error())
	case errors.Is(err, domain.ErrSubjectRequired),
		errors.Is(err, domain.ErrSubjectTooLong),
		errors.Is(err, domain.ErrResourceRequired),
		errors.Is(err, domain.ErrResourceTooLong):
		return apperr.BadRequest(err.Error())
	case errors.Is(err, domain.ErrNameRequired),
		errors.Is(err, domain.ErrNameTooLong),
		errors.Is(err, domain.ErrReasonRequired),
		errors.Is(err, domain.ErrReasonTooLong):
		return apperr.BadRequest(err.Error())
	case isInvalidEnum(err):
		return apperr.BadRequest(err.Error())
	default:
		return apperr.Internal("tenant operation failed", err)
	}
}
