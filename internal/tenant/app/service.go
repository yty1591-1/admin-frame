// Package app 编排租户生命周期用例（ENT-09）。
//
// 职责边界：状态机在 domain，持久化在 infrastructure；本包只做
// 「读 → 迁移 → CAS 写 → 审计」的编排，并把领域错误翻译为统一错误码。
package app

import (
	"context"
	"errors"
	"strconv"
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
	// ActionUpdate 变更租户资料（改名 + 元数据，T-11）。
	ActionUpdate = "tenant.update"
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
	// Revoker 会话撤销端口（D-02=① 立即撤销）；nil 表示本进程没有可用的
	// 撤销能力（单测 / 内存模式），停用与终止将只改状态不撤令牌——
	// 生产装配必须注入 identity 的实现，见 cmd/admin-api。
	Revoker SessionRevoker
	// Plans 套餐目录（T-24）；nil 时 Create 拒绝服务（无法解析默认配额）。
	Plans domain.PlanRepository
	// Members 成员仓储（T-12）：开通前置校验与建租户时落首名 owner。
	Members domain.MemberRepository
	// Quotas 配额仓储（T-24）：创建租户时物化套餐默认配额；T-13 席位与
	// T-14 邀请接受也复用。
	Quotas domain.QuotaRepository
	// InvitationTTL 邀请有效期（T-14）；<=0 使用领域默认值（7 天）。
	InvitationTTL time.Duration
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
	// Code 业务编码（T-11）；空时以生成的租户 ID 兜底（与迁移 000013 的
	// 存量回填策略一致）。创建后不可变，重复返回 409。
	Code string
	// PlanCode 套餐编码（T-24）；空使用默认套餐，未知编码返回 404。
	PlanCode string
	// OwnerSubjectID 首名 owner 的主体 ID（T-12）；可选——开通前必须补齐，
	// 但允许「先建租户、迁移成员、再开通」的两步操作。
	OwnerSubjectID string
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
	repo    domain.Repository
	ids     id.Generator
	audit   auditContext
	now     func() time.Time
	opts    Options
	members domain.MemberRepository
	quotas  domain.QuotaRepository
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
		repo:    repo,
		ids:     ids,
		audit:   auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		now:     now,
		opts:    opts,
		members: opts.Members,
		quotas:  opts.Quotas,
	}, nil
}

// Create 创建待开通租户。
//
// 建租户、物化套餐配额、落首名 owner、写审计全部同事务：任一步失败整体回滚，
// 不存在「有租户没配额」「有租户没记录」的中间态。
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Tenant, error) {
	// 编码与套餐都在事务外解析：输入校验与目录读失败都不应开始写事务。
	// 未提供编码时使用 NewTenant 的默认值（code = id，与迁移 000013 同源）。
	explicitCode := strings.TrimSpace(in.Code)
	if explicitCode != "" {
		if _, err := domain.NormalizeCode(explicitCode); err != nil {
			return domain.Tenant{}, translate(err)
		}
	}

	plan, err := s.resolvePlan(ctx, in.PlanCode)
	if err != nil {
		return domain.Tenant{}, err
	}

	var tenant domain.Tenant
	err = s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		tenantID, idErr := s.ids.NextString()
		if idErr != nil {
			return apperr.Internal("tenant id generation failed", idErr)
		}
		built, buildErr := domain.NewTenant(tenantID, in.Name, s.now())
		if buildErr != nil {
			return buildErr
		}
		if explicitCode != "" {
			built.Code = explicitCode
		}
		built.PlanID = plan.ID
		if createErr := s.repo.Create(txCtx, built); createErr != nil {
			return createErr
		}
		if err := s.materializeQuotas(txCtx, built.ID, plan); err != nil {
			return err
		}
		if err := s.seedOwner(txCtx, built.ID, in.OwnerSubjectID, in.Actor); err != nil {
			return err
		}
		if emitErr := s.emitWithAttrs(txCtx, in.Actor, ActionCreate, built, audit.OutcomeSuccess, "",
			map[string]string{"code": built.Code, "plan": plan.Code}); emitErr != nil {
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

// UpdateProfileInput 是资料变更入参（T-11）。
//
// 用指针区分「未提供」与「显式清空」：只改名称的请求不能顺手把
// 联系人信息抹掉；显式传空串才表示清空该字段。
type UpdateProfileInput struct {
	// ID 租户 ID。
	ID string
	// Name 名称；nil 不变。
	Name *string
	// ContactName 联系人姓名；nil 不变，空串清空。
	ContactName *string
	// ContactEmail 联系邮箱；nil 不变，空串清空。
	ContactEmail *string
	// Industry 行业；nil 不变，空串清空。
	Industry *string
	// Timezone 时区；nil 不变，空串清空。
	Timezone *string
	// Locale 语言标签；nil 不变，空串清空。
	Locale *string
	// ExternalID 外部系统 ID；nil 不变，空串清空。
	ExternalID *string
	// Actor 操作者。
	Actor Actor
}

// UpdateProfile 变更租户资料（改名 + 元数据，T-11）。
//
// 读当前资料、合并入参、CAS 写入、写审计同事务；编码不可变——
// 它是对账引用，修改编码应走数据订正流程而不是管理接口。
func (s *Service) UpdateProfile(ctx context.Context, in UpdateProfileInput) (domain.Tenant, error) {
	if strings.TrimSpace(in.ID) == "" {
		return domain.Tenant{}, apperr.BadRequest("tenant id is required")
	}

	var next domain.Tenant
	var changed []string
	err := s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		current, err := mutableTenant(txCtx, s.repo, in.ID)
		if err != nil {
			return err
		}
		profile := domain.Profile{
			Name:         current.Name,
			ContactName:  current.ContactName,
			ContactEmail: current.ContactEmail,
			Industry:     current.Industry,
			Timezone:     current.Timezone,
			Locale:       current.Locale,
			ExternalID:   current.ExternalID,
		}
		changed = changed[:0]
		if in.Name != nil {
			profile.Name = *in.Name
			changed = append(changed, "name")
		}
		if in.ContactName != nil {
			profile.ContactName = *in.ContactName
			changed = append(changed, "contact_name")
		}
		if in.ContactEmail != nil {
			profile.ContactEmail = *in.ContactEmail
			changed = append(changed, "contact_email")
		}
		if in.Industry != nil {
			profile.Industry = *in.Industry
			changed = append(changed, "industry")
		}
		if in.Timezone != nil {
			profile.Timezone = *in.Timezone
			changed = append(changed, "timezone")
		}
		if in.Locale != nil {
			profile.Locale = *in.Locale
			changed = append(changed, "locale")
		}
		if in.ExternalID != nil {
			profile.ExternalID = *in.ExternalID
			changed = append(changed, "external_id")
		}
		if len(changed) == 0 {
			return apperr.BadRequest("at least one field is required")
		}

		built, buildErr := current.WithProfile(profile, s.now())
		if buildErr != nil {
			return buildErr
		}
		if updateErr := s.repo.UpdateProfile(txCtx, built); updateErr != nil {
			return updateErr
		}
		if emitErr := s.emitWithAttrs(txCtx, in.Actor, ActionUpdate, built, audit.OutcomeSuccess, "",
			map[string]string{"fields": strings.Join(changed, ",")}); emitErr != nil {
			return emitErr
		}
		next = built
		return nil
	})
	if err != nil {
		return domain.Tenant{}, translateIfNeeded(err)
	}
	return next, nil
}

// resolvePlan 解析开账套餐：显式编码优先，否则默认套餐。
func (s *Service) resolvePlan(ctx context.Context, planCode string) (domain.Plan, error) {
	if s.opts.Plans == nil {
		return domain.Plan{}, apperr.Internal("tenant plan catalog is not configured", nil)
	}
	if code := strings.TrimSpace(planCode); code != "" {
		plan, err := s.opts.Plans.FindByCode(ctx, code)
		if err != nil {
			if errors.Is(err, domain.ErrPlanNotFound) {
				return domain.Plan{}, apperr.NotFound("plan not found: " + code)
			}
			return domain.Plan{}, apperr.Internal("load tenant plan failed", err)
		}
		return plan, nil
	}
	plan, err := s.opts.Plans.DefaultPlan(ctx)
	if err != nil {
		// 目录没有默认套餐属部署事故（迁移 000012 保证存在），按内部错误暴露。
		return domain.Plan{}, apperr.Internal("tenant default plan is missing", err)
	}
	return plan, nil
}

// materializeQuotas 把套餐默认配额物化进 tenant_quota。
//
// 幂等：已存在的租户级覆盖不被覆盖（回填迁移与重放同一语义）。
func (s *Service) materializeQuotas(ctx context.Context, tenantID string, plan domain.Plan) error {
	if s.quotas == nil {
		return nil
	}
	for _, quota := range plan.SortedQuotas() {
		built, err := domain.NewQuota(tenantID, quota.Resource, quota.Limit, s.now())
		if err != nil {
			return apperr.Internal("materialize plan quota failed", err)
		}
		if err := s.quotas.UpsertQuota(ctx, built); err != nil {
			return translate(err)
		}
	}
	return nil
}

// seedOwner 落首名 owner；主体为空时跳过（允许两步开通）。
//
// 建租户与落 owner 在同一事务：owner 落库前同样过席位校验（T-13）——
// 虽然刚物化的套餐配额几乎不可能在第一个成员上就超限，但校验与
// 成员添加共用同一段逻辑，套餐配置错误（user limit=0）会在创建期
// 暴露，而不是留一个开不出成员的租户。
func (s *Service) seedOwner(ctx context.Context, tenantID, subjectID string, actor Actor) error {
	trimmed := strings.TrimSpace(subjectID)
	if trimmed == "" || s.members == nil {
		return nil
	}
	if err := checkSeat(ctx, s.quotas, s.members, tenantID); err != nil {
		return err
	}
	member, err := domain.NewMember(tenantID, trimmed, domain.MemberRoleOwner, s.now())
	if err != nil {
		return translate(err)
	}
	if err := s.members.UpsertMember(ctx, member); err != nil {
		return translate(err)
	}
	return s.audit.emit(ctx, actor, ActionMemberAdd, ResourceTypeMember, tenantID,
		audit.OutcomeSuccess, "", map[string]string{
			"subject_id": member.SubjectID,
			"role":       string(member.Role),
			"seeded":     "true",
		})
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
//
// 停用 / 终止会先撤销该租户全部会话（D-02=①），再执行状态迁移：
//   - 撤销失败 → 整体失败，状态不变（不允许「已停用、令牌还能换新」）；
//   - 撤销成功但迁移失败 → 出现「会话已撤、租户未停」——方向是 fail-closed
//     （用户重新登录即可恢复），好于反过来。identity 事务不与租户域共享，
//     这是当前装配下的诚实取舍。
func (s *Service) Transition(ctx context.Context, in TransitionInput) (domain.Tenant, error) {
	if strings.TrimSpace(in.ID) == "" {
		return domain.Tenant{}, apperr.BadRequest("tenant id is required")
	}
	// 预检：非法迁移直接拒绝，避免为注定失败的请求撤销会话。
	current, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return domain.Tenant{}, translate(err)
	}
	computed, err := current.Transition(in.Target, in.Reason, s.now())
	if err != nil {
		s.emitRejected(ctx, in, err)
		return domain.Tenant{}, translate(err)
	}

	revoked := 0
	if revocationRequired(computed.Status) && s.opts.Revoker != nil {
		revoked, err = s.opts.Revoker.RevokeByTenant(ctx, in.ID)
		if err != nil {
			var appErr *apperr.AppError
			if errors.As(err, &appErr) {
				return domain.Tenant{}, err
			}
			return domain.Tenant{}, apperr.Internal("tenant session revocation failed", err)
		}
	}

	var next domain.Tenant
	err = s.repo.WithinTx(ctx, func(txCtx context.Context) error {
		// 事务内重新读：锁外读到的状态可能已被并发请求改掉。
		locked, getErr := s.repo.Get(txCtx, in.ID)
		if getErr != nil {
			return getErr
		}
		// T-12 开通前置：0 成员的租户开出来也无法自服务，宁可让管理员先补 owner。
		// 校验放在事务内：与迁移之间没有窗口期，并发添加 owner 也能被看到。
		if in.Target == domain.StatusActive && locked.Status == domain.StatusPending && s.members != nil {
			owners, ownerErr := s.members.CountActiveByRole(txCtx, locked.ID, domain.MemberRoleOwner)
			if ownerErr != nil {
				return ownerErr
			}
			if owners == 0 {
				return domain.ErrNoOwner
			}
		}
		migrated, transErr := locked.Transition(in.Target, in.Reason, s.now())
		if transErr != nil {
			return transErr
		}
		if updateErr := s.repo.UpdateStatus(txCtx, migrated); updateErr != nil {
			return updateErr
		}
		if emitErr := s.emitWithAttrs(txCtx, in.Actor, actionFor(locked.Status, in.Target), migrated,
			audit.OutcomeSuccess, in.Reason, revocationAttrs(revoked)); emitErr != nil {
			return emitErr
		}
		next = migrated
		return nil
	})
	if err != nil {
		s.emitRejected(ctx, in, err)
		return domain.Tenant{}, translateIfNeeded(err)
	}
	return next, nil
}

// revocationRequired 报告目标状态是否要求先撤销会话：停用与终止都要，
// 恢复（resume）与开通不需要——那里没有「必须失效的存量令牌」。
func revocationRequired(target domain.Status) bool {
	return target == domain.StatusSuspended || target == domain.StatusTerminated
}

// revocationAttrs 撤销数写进审计 attrs；未撤销（未配置 Revoker）时不写字段，
// 避免审计里出现语义含糊的 0。
func revocationAttrs(revoked int) map[string]string {
	if revoked <= 0 {
		return nil
	}
	return map[string]string{"revoked_sessions": strconv.Itoa(revoked)}
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
	return s.emitWithAttrs(ctx, actor, action, tenant, outcome, reason, nil)
}

// emitWithAttrs 写租户主数据审计事件并附加额外属性。
func (s *Service) emitWithAttrs(ctx context.Context, actor Actor, action string, tenant domain.Tenant,
	outcome audit.Outcome, reason string, extra map[string]string) error {
	attrs := map[string]string{
		"status": string(tenant.Status),
		"name":   tenant.Name,
	}
	for key, value := range extra {
		attrs[key] = value
	}
	return s.audit.emit(ctx, actor, action, ResourceTypeTenant, tenant.ID, outcome, reason, attrs)
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
	case errors.Is(err, domain.ErrNoOwner):
		return apperr.Conflict(err.Error())
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
		errors.Is(err, domain.ErrResourceTooLong),
		errors.Is(err, domain.ErrCodeRequired),
		errors.Is(err, domain.ErrCodeTooLong):
		return apperr.BadRequest(err.Error())
	case errors.Is(err, domain.ErrCodeExists):
		// 409：编码是运营输入，重复是可恢复冲突，与「ID 撞车」分开提示。
		return apperr.Conflict("tenant code already exists")
	case errors.Is(err, domain.ErrSeatLimitExceeded):
		// 保留原始错误链：调用方可 errors.Is 识别席位超限；
		// 消息携带「active N, limit M」，409 响应体直接可用。
		return apperr.New(apperr.CodeConflict, err.Error(), err)
	case errors.Is(err, domain.ErrInvitationNotFound):
		return apperr.NotFound("invitation not found")
	case errors.Is(err, domain.ErrInvitationPendingExists):
		// 冲突类邀请错误统一保留因果链：调用方可 errors.Is 区分细分语义
		// （幂等接受 / 换人抢受 / 重复 pending 都依赖哨兵判断）。
		return apperr.New(apperr.CodeConflict, "a pending invitation already exists for this email, resend instead", err)
	case errors.Is(err, domain.ErrInvitationAlreadyAccepted):
		return apperr.New(apperr.CodeConflict, "invitation already accepted by another subject", err)
	case errors.Is(err, domain.ErrInvitationExpired):
		return apperr.New(apperr.CodeConflict, "invitation is expired", err)
	case errors.Is(err, domain.ErrInvitationNotPending):
		return apperr.New(apperr.CodeConflict, "invitation is not pending", err)
	case errors.Is(err, domain.ErrInvitationEmailRequired),
		errors.Is(err, domain.ErrInvitationEmailTooLong):
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
