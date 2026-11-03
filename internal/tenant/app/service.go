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

// emit 写一条审计事件；失败只降级为不写，不改变业务结果。
func (a auditContext) emit(ctx context.Context, actor Actor, action, resourceType, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) {
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
	// 审计失败不回滚业务：状态已落库，回滚会让「实际生效但无记录」更糟。
	_ = a.emitter.Emit(ctx, event)
}

// StatusInvalidator 在状态迁移成功后清除本地状态缓存。
//
// 跨副本无法即时失效（那需要共享缓存），验收口径是「停用 ≤ 60s 生效」，
// 由缓存 TTL 保证；同一进程内则通过本接口立即生效，避免刚停用又放行一个 TTL。
type StatusInvalidator interface {
	// Invalidate 清除指定租户的缓存。
	Invalidate(tenantID string)
}

// Options 是用例参数。
type Options struct {
	// DefaultPageSize 列表默认每页条数。
	DefaultPageSize int
	// MaxPageSize 列表每页上限；超过按上限收敛而不是报错。
	MaxPageSize int
	// Service 服务名，写入审计事件。
	Service string
	// Invalidator 状态缓存失效器；nil 表示本进程没有租户状态缓存。
	Invalidator StatusInvalidator
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
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Tenant, error) {
	tenantID, err := s.ids.NextString()
	if err != nil {
		return domain.Tenant{}, apperr.Internal("tenant id generation failed", err)
	}
	tenant, err := domain.NewTenant(tenantID, in.Name, s.now())
	if err != nil {
		return domain.Tenant{}, translate(err)
	}
	if err := s.repo.Create(ctx, tenant); err != nil {
		return domain.Tenant{}, translate(err)
	}
	s.emit(ctx, in.Actor, ActionCreate, tenant, audit.OutcomeSuccess, "")
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

// Transition 执行一次状态迁移；失败时不写审计成功事件，但拒绝动作记 failure。
func (s *Service) Transition(ctx context.Context, in TransitionInput) (domain.Tenant, error) {
	if strings.TrimSpace(in.ID) == "" {
		return domain.Tenant{}, apperr.BadRequest("tenant id is required")
	}
	current, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return domain.Tenant{}, translate(err)
	}
	next, err := current.Transition(in.Target, in.Reason, s.now())
	if err != nil {
		// 失败也留痕：非法迁移往往意味着有人在尝试绕过流程。
		s.emit(ctx, in.Actor, actionFor(current.Status, in.Target), current, audit.OutcomeFailure, err.Error())
		return domain.Tenant{}, translate(err)
	}
	if err := s.repo.UpdateStatus(ctx, next); err != nil {
		s.emit(ctx, in.Actor, actionFor(current.Status, in.Target), current, audit.OutcomeFailure, err.Error())
		return domain.Tenant{}, translate(err)
	}
	// 迁移成功即失效本地缓存：同进程内停用立刻生效，不等 TTL 过期。
	if s.opts.Invalidator != nil {
		s.opts.Invalidator.Invalidate(next.ID)
	}
	s.emit(ctx, in.Actor, actionFor(current.Status, in.Target), next, audit.OutcomeSuccess, in.Reason)
	return next, nil
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
	outcome audit.Outcome, reason string) {
	s.audit.emit(ctx, actor, action, ResourceTypeTenant, tenant.ID, outcome, reason, map[string]string{
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
