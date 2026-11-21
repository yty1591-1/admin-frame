package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// SendRequest 是一次渠道发送请求（渲染后瞬时构造，不落库）。
type SendRequest struct {
	// RequestID 所属通知单 ID。
	RequestID string
	// TenantID 租户隔离键。
	TenantID string
	// Channel 投递渠道。
	Channel domain.Channel
	// Address 渠道地址（明文，仅瞬时内存）。
	Address string
	// Rendered 渲染产物。
	Rendered domain.Rendered
	// Reference 业务引用（供应商侧排障对账）。
	Reference string
}

// SendResult 是渠道发送产物。
type SendResult struct {
	// Provider 实际供应商。
	Provider string
	// MessageID 渠道消息 ID（回执对账键）。
	MessageID string
}

// Sender 是渠道发送端口：infrastructure 包装 pkg/email|sms|push 实现，
// 编排层不接触具体驱动。驱动内绝不重试——超时 / 重试 / 熔断全部在编排层。
type Sender interface {
	// Provider 返回供应商标识（如 smtp / aliyun / apns）。
	Provider() string
	// Send 单次发送尝试。
	Send(ctx context.Context, req SendRequest) (SendResult, error)
}

// SendError 是编排层的归一化发送错误：语义码进投递记录与指标标签
// （低基数枚举），供应商原始码仅排障，Summary 必须已脱敏。
type SendError struct {
	// Semantic 语义码：timeout | rate_limited | invalid_address | auth_failed |
	// token_invalid | provider_error | internal。
	Semantic string
	// ProviderCode 供应商原始错误码（仅排障字段）。
	ProviderCode string
	// Summary 脱敏失败摘要（不含收件人 / 正文 / 验证码）。
	Summary string
	// Retryable 是否值得重试。
	Retryable bool
	// RetryAfter 供应商建议的等待时长（0 表示按默认退避）。
	RetryAfter time.Duration
	// Cause 底层驱动错误（保留因果链）。
	Cause error
}

// Error 实现 error。
func (e *SendError) Error() string {
	if e.Summary != "" {
		return e.Summary
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "notification: send failed (" + e.Semantic + ")"
}

// Unwrap 保留因果链。
func (e *SendError) Unwrap() error { return e.Cause }

// 编排管道语义码（低基数，可入指标标签）。
const (
	SemanticTimeout        = "timeout"
	SemanticRateLimited    = "rate_limited"
	SemanticInvalidAddress = "invalid_address"
	SemanticAuthFailed     = "auth_failed"
	SemanticTokenInvalid   = "token_invalid"
	SemanticProviderError  = "provider_error"
	SemanticInternal       = "internal"
	SemanticOptedOut       = "opted_out"
	SemanticBlacklisted    = "blacklisted"
	SemanticUnreachable    = "unreachable"
	SemanticExpired        = "expired"
	// SemanticChannelUnavailable 熔断 Open 的快速失败：可重试，主备切换依据。
	SemanticChannelUnavailable = "channel_unavailable"
)

// ActionRequestRejected 拒发审计动作（EVENTS.md notification.* 段；编排层唯一自发审计）。
const ActionRequestRejected = "notification.request.rejected"

// 重试预算（按优先级）：耗尽后失败终态，补偿走 Replay（规划 §2.F）。
const (
	DefaultCriticalAttempts = 6
	DefaultNormalAttempts   = 4
	DefaultLowAttempts      = 2
)

// OrchestratorOptions 是编排器装配参数。
type OrchestratorOptions struct {
	// Options 基础参数（Service / Observer / DefaultLocale / TTL）。
	Options
	// Route 渠道 → 供应商列表（顺序即主备序）；必填。
	// 空渠道在运行时报 ErrNoRoute（事件进 DLQ，装配错误必须显性暴露）。
	Route map[domain.Channel][]Sender
	// Breakers 供应商熔断器（nil 时按供应商自动创建默认配置）。
	Breakers map[string]*circuitbreaker.Breaker
	// Limiter 供应商 / 收件人频控（可空：单测与内存模式）。
	Limiter *ratelimit.Keyed
	// MaxAttempts 按优先级的重试预算；缺省用 Default*Attempts。
	MaxAttempts map[domain.Priority]int
	// Emitter 审计出口（拒发等编排动作留痕）；必填——缺审计出口时
	// 宁可启动失败，也不要出现「拒发了但查不到为什么」。
	Emitter audit.Emitter
}

// Orchestrator 是投递编排器：notification.requested 事件的消费 Handler。
//
// 管道固定顺序（规划 §2.E）：幂等去重 → 过期判定 → 偏好 / 黑名单 →
// 渲染 → 频控 → 熔断包住发送 → 状态回写。至少一次语义由 Outbox 保证，
// 本 Handler 必须幂等：重复事件被幂等键吸收，重投事件续跑未完成渠道。
type Orchestrator struct {
	requests    domain.RequestRepository
	deliveries  domain.DeliveryRepository
	templates   domain.TemplateRepository
	preferences domain.PreferenceRepository
	blacklist   domain.BlacklistRepository
	route       map[domain.Channel][]Sender
	breakers    map[string]*circuitbreaker.Breaker
	limiter     *ratelimit.Keyed
	observer    Observer
	auditCtx    auditContext
	now         func() time.Time
	defaults    Options
	budgets     map[domain.Priority]int
}

// NewOrchestrator 构造编排器；仓储与路由必填。
func NewOrchestrator(requests domain.RequestRepository, deliveries domain.DeliveryRepository,
	templates domain.TemplateRepository, preferences domain.PreferenceRepository,
	blacklist domain.BlacklistRepository, opts OrchestratorOptions) (*Orchestrator, error) {
	switch {
	case requests == nil:
		return nil, apperr.Internal("notification orchestrator requires a request repository", nil)
	case deliveries == nil:
		return nil, apperr.Internal("notification orchestrator requires a delivery repository", nil)
	case templates == nil:
		return nil, apperr.Internal("notification orchestrator requires a template repository", nil)
	case preferences == nil:
		return nil, apperr.Internal("notification orchestrator requires a preference repository", nil)
	case blacklist == nil:
		return nil, apperr.Internal("notification orchestrator requires a blacklist repository", nil)
	case opts.Emitter == nil:
		return nil, apperr.Internal("notification orchestrator requires an audit emitter", nil)
	}
	// Route 允许为空：prod/staging 可将全部渠道显式设为 disabled（配置校验已放行）。
	// 此时投递无可用 Sender，按失败路径落终态，不阻塞 worker 启动。
	now := time.Now
	budgets := opts.MaxAttempts
	if budgets == nil {
		budgets = map[domain.Priority]int{}
	}
	setDefault := func(p domain.Priority, def int) {
		if budgets[p] <= 0 {
			budgets[p] = def
		}
	}
	setDefault(domain.PriorityCritical, DefaultCriticalAttempts)
	setDefault(domain.PriorityNormal, DefaultNormalAttempts)
	setDefault(domain.PriorityLow, DefaultLowAttempts)

	breakers := make(map[string]*circuitbreaker.Breaker, len(opts.Route)*2)
	for channel, senders := range opts.Route {
		if channel.Implemented() && len(senders) == 0 {
			return nil, apperr.Internal("notification route for channel "+string(channel)+" is empty", nil)
		}
		for _, s := range senders {
			if s == nil || strings.TrimSpace(s.Provider()) == "" {
				return nil, apperr.Internal("notification route sender requires a provider name", nil)
			}
			if _, exists := breakers[s.Provider()]; !exists {
				if opts.Breakers != nil {
					if b, ok := opts.Breakers[s.Provider()]; ok && b != nil {
						breakers[s.Provider()] = b
						continue
					}
				}
				breakers[s.Provider()] = circuitbreaker.MustNew(circuitbreaker.Config{Name: "notify-" + s.Provider()})
			}
		}
	}
	return &Orchestrator{
		requests:    requests,
		deliveries:  deliveries,
		templates:   templates,
		preferences: preferences,
		blacklist:   blacklist,
		route:       opts.Route,
		breakers:    breakers,
		limiter:     opts.Limiter,
		observer:    observerOrNoop(opts.Observer),
		auditCtx:    auditContext{emitter: opts.Emitter, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		now:         now,
		defaults:    opts.Options,
		budgets:     budgets,
	}, nil
}

// Handle 实现 outbox.Handler：处理一条 notification.requested 事件。
//
// 错误语义：
//   - 载荷非法 / 不认识的 schema → retry.Permanent（毒消息进 DLQ）；
//   - 可重试发送失败且预算未耗尽 → 返回错误（Outbox 退避重投，续跑幂等）；
//   - 预算耗尽 / 永久错误 → 落终态并返回 nil（补偿走 Replay，不占用 DLQ）。
func (o *Orchestrator) Handle(ctx context.Context, event *outbox.Event) error {
	payload, err := parseRequestPayload(event)
	if err != nil {
		return err
	}

	// ① 幂等去重：已终态直接吸收；未终态视为重投，续跑未完成渠道。
	n, err := o.requests.FindByIdempotencyKey(ctx, payload.IdempotencyKey)
	switch {
	case err == nil:
		if n.Status.Terminal() {
			return nil // 至少一次投递的重复事件：幂等吸收。
		}
	case errors.Is(err, domain.ErrNotFound):
		n, err = o.createRequest(ctx, payload)
		if err != nil {
			return err
		}
	default:
		return apperr.Internal("lookup notification by idempotency key failed", err)
	}

	// ② 过期判定：迟到的通知大多是噪声，弃发不重试。
	if n.Expired(o.now()) {
		return o.finalizeReject(ctx, n, SemanticExpired, "request expired before dispatch")
	}

	// ③ 偏好 / 黑名单 / 寻址过滤，得到本次要投的有效渠道。
	effective, skipReasons := o.filterChannels(ctx, n, payload)
	if len(effective) == 0 {
		reason := "no deliverable channel"
		if len(skipReasons) > 0 {
			reason = strings.Join(skipReasons, "; ")
		}
		return o.finalizeReject(ctx, n, SemanticUnreachable, reason)
	}

	// ④ 建渠道明细并推进 dispatched。
	now := o.now()
	for _, ch := range effective {
		if _, err := o.deliveries.Get(ctx, n.ID, ch); err == nil {
			continue // 重投 / 续跑：明细已存在。
		}
		d, derr := domain.NewDelivery(n.ID, n.ID, n.TenantID, ch, now)
		if derr != nil {
			return apperr.Internal("create delivery failed", derr)
		}
		if uerr := o.deliveries.Upsert(ctx, d); uerr != nil {
			return apperr.Internal("persist delivery failed", uerr)
		}
	}
	if n.Status == domain.RequestPending {
		next, terr := n.Transition(domain.RequestDispatched, "", now)
		if terr == nil {
			n = next
			if uerr := o.requests.UpdateStatus(ctx, n); uerr != nil && !errors.Is(uerr, domain.ErrVersionConflict) {
				return apperr.Internal("advance notification to dispatched failed", uerr)
			}
		}
	}

	// ⑤ 逐渠道投递；渠道间互不阻塞，失败不阻断其他渠道。
	// 可重试失败优先返回（Outbox 退避重投，续跑幂等）；
	// 全部渠道终态后若存在失败 → retry.Permanent 让事件进 DLQ，
	// 保留载荷供 Replay 补偿（重放走新请求，见 replay.go）。
	var retryableErr, permanentErr error
	for _, ch := range effective {
		if err := o.deliverChannel(ctx, n, payload, ch, event); err != nil {
			switch {
			case retry.IsPermanent(err):
				if permanentErr == nil {
					permanentErr = err
				}
			case retryableErr == nil:
				retryableErr = err
			}
		}
	}

	// ⑥ 收敛通知单状态（单调，CAS 冲突让位给回执路径）。
	if err := o.convergeRequest(ctx, n.ID); err != nil {
		return err
	}
	if retryableErr != nil {
		// 可重试失败交还 Outbox 退避重投（续跑语义吸收已完成渠道）。
		return retryableErr
	}
	if permanentErr != nil {
		// 通知单已落失败终态；事件进 DLQ 保留载荷（Replay 数据源）。
		return permanentErr
	}
	return nil
}

// createRequest 构造并落库通知单。
func (o *Orchestrator) createRequest(ctx context.Context, payload RequestPayload) (domain.Notification, error) {
	expiresAt := payload.ExpiresAt
	if expiresAt.IsZero() {
		ttl := o.defaults.DefaultNotificationTTL
		if ttl <= 0 {
			ttl = domain.DefaultNotificationTTL
		}
		expiresAt = o.now().Add(ttl)
	}
	channels := make([]domain.Channel, 0, len(payload.Channels))
	for _, raw := range payload.Channels {
		channels = append(channels, domain.Channel(strings.TrimSpace(raw)))
	}
	priority := domain.Priority(strings.TrimSpace(payload.Priority))
	if !priority.Valid() {
		priority = domain.PriorityNormal
	}
	n, err := domain.NewNotification(payload.RequestID, payload.TenantID, payload.Scene,
		payload.RecipientRef, payload.IdempotencyKey, channels, payload.TemplateKey,
		payload.Locale, priority, expiresAt, o.now())
	if err != nil {
		// 载荷字段过不了领域校验：构造缺陷，重试无意义。
		return domain.Notification{}, retry.Permanent(fmt.Errorf("notification: invalid payload: %w", err))
	}
	if err := o.requests.Create(ctx, n); err != nil {
		if errors.Is(err, domain.ErrDuplicateRequest) {
			// 并发双副本同时受理：重读即可，输家续跑幂等路径。
			existing, gerr := o.requests.FindByIdempotencyKey(ctx, payload.IdempotencyKey)
			if gerr != nil {
				return domain.Notification{}, apperr.Internal("reload duplicate notification failed", gerr)
			}
			return existing, nil
		}
		return domain.Notification{}, apperr.Internal("create notification failed", err)
	}
	o.observer.RequestObserved(n.Scene, n.Status)
	return n, nil
}

// filterChannels 偏好 / 黑名单 / 寻址过滤；返回有效渠道与跳过原因。
func (o *Orchestrator) filterChannels(ctx context.Context, n domain.Notification,
	payload RequestPayload) (effective []domain.Channel, reasons []string) {
	effective = make([]domain.Channel, 0, len(n.Channels))
	reasons = nil
	class := domain.SceneClass(strings.TrimSpace(payload.SceneClass))
	for _, ch := range n.Channels {
		address := payload.Recipient.AddressFor(ch)
		if address == "" {
			reasons = append(reasons, string(ch)+": no address")
			continue
		}
		blocked, err := o.blacklist.Exists(ctx, n.TenantID, ch, hashTarget(address))
		if err != nil {
			// 判定失败按 fail closed 处理：宁可漏发也不越过黑名单。
			reasons = append(reasons, string(ch)+": blacklist check failed")
			continue
		}
		if blocked {
			o.observer.DeliveryObserved(ch, "", "failed", SemanticBlacklisted)
			reasons = append(reasons, string(ch)+": blacklisted")
			continue
		}
		if payload.Recipient.UserID != "" && class.Valid() {
			allowed, err := o.preferences.Get(ctx, n.TenantID, domain.SubjectUser, payload.Recipient.UserID, class, ch)
			switch {
			case err == nil && !allowed.Enabled:
				o.observer.DeliveryObserved(ch, "", "failed", SemanticOptedOut)
				reasons = append(reasons, string(ch)+": opted out")
				continue
			case err != nil && !errors.Is(err, domain.ErrNotFound):
				reasons = append(reasons, string(ch)+": preference check failed")
				continue
			}
		}
		effective = append(effective, ch)
	}
	return effective, reasons
}

// deliverChannel 投递单渠道：频控 → 熔断包住发送 → 状态回写。
func (o *Orchestrator) deliverChannel(ctx context.Context, n domain.Notification,
	payload RequestPayload, ch domain.Channel, event *outbox.Event) error {
	now := o.now()
	d, err := o.deliveries.Get(ctx, n.ID, ch)
	if err != nil {
		if errors.Is(err, domain.ErrDeliveryNotFound) {
			return nil // 其他并发 worker 已收走或明细被清理；不重复建。
		}
		return apperr.Internal("load delivery failed", err)
	}
	if d.Status.Settled() {
		return nil // 续跑：该渠道业务已了结（accepted / 终态），回执推进走 ReceiptService。
	}

	// 频控：供应商配额 + 收件人配额；拒绝按可重试处理（退避后再试）。
	if o.limiter != nil {
		if result := o.limiter.Allow(providerKey(n.Priority, ch)); !result.Allowed {
			o.observer.RateLimited()
			return o.markRetrying(ctx, n, d, &SendError{
				Semantic: SemanticRateLimited, Summary: "provider quota exhausted",
				Retryable: true, RetryAfter: result.RetryAfter,
			}, event)
		}
		if result := o.limiter.Allow(recipientKey(n.TenantID, payload.Recipient.AddressFor(ch))); !result.Allowed {
			o.observer.RateLimited()
			return o.markRetrying(ctx, n, d, &SendError{
				Semantic: SemanticRateLimited, Summary: "recipient rate limited",
				Retryable: true, RetryAfter: result.RetryAfter,
			}, event)
		}
	}

	rendered, err := o.render(ctx, n, payload, ch)
	if err != nil {
		// 渲染失败是配置 / 输入缺陷：不可重试，落终态。
		return o.markFailed(ctx, n, d, &SendError{
			Semantic: SemanticInternal, Summary: "render failed: " + err.Error(),
		})
	}

	senders := o.route[ch]
	var lastErr *SendError
	for i, sender := range senders {
		breaker := o.breakers[sender.Provider()]
		var result SendResult
		sendErr := breaker.Do(func() error {
			var err error
			result, err = sender.Send(ctx, SendRequest{
				RequestID: n.ID, TenantID: n.TenantID, Channel: ch,
				Address: payload.Recipient.AddressFor(ch), Rendered: rendered,
				Reference: n.Reference,
			})
			return err
		})
		if sendErr == nil {
			accepted, terr := d.Transition(domain.DeliveryAccepted, now)
			if terr != nil {
				return apperr.Internal("advance delivery to accepted failed", terr)
			}
			accepted.Provider = sender.Provider()
			accepted.MessageID = result.MessageID
			if uerr := o.deliveries.Update(ctx, accepted); uerr != nil {
				return apperr.Internal("persist accepted delivery failed", uerr)
			}
			o.observer.DeliveryObserved(ch, sender.Provider(), "accepted", "")
			return nil
		}
		classified := classifySendError(sendErr)
		if !classified.Retryable && breaker.State() == circuitbreaker.StateOpen {
			// 熔断快速失败本身就是「渠道暂不可用」：按可重试分类，
			// 并满足主备切换条件（规划 §2.D）。
			classified = &SendError{
				Semantic:  SemanticChannelUnavailable,
				Summary:   "provider " + sender.Provider() + " breaker open",
				Retryable: true,
				Cause:     sendErr,
			}
		}
		lastErr = classified
		// 主备切换（规划 §2.D）：仅可重试错误且当前供应商熔断已 Open
		// 才切备用；永久错误切换没有意义。
		if classified.Retryable && i+1 < len(senders) && breaker.State() == circuitbreaker.StateOpen {
			o.observer.DeliveryObserved(ch, sender.Provider(), "degraded", classified.Semantic)
			continue
		}
		break
	}

	// 渠道内全部供应商失败：按预算决定重试还是终态。
	budget := o.budgets[n.Priority]
	if lastErr != nil && isRetryable(lastErr) && d.Attempt < budget {
		return o.markRetrying(ctx, n, d, lastErr, event)
	}
	return o.markFailed(ctx, n, d, lastErr)
}

// render 渲染指定渠道模板。
func (o *Orchestrator) render(ctx context.Context, n domain.Notification, payload RequestPayload, ch domain.Channel) (domain.Rendered, error) {
	tpl, err := o.templates.Find(ctx, n.TemplateKey, ch, n.Locale)
	if err != nil {
		return domain.Rendered{}, fmt.Errorf("template %s for %s: %w", n.TemplateKey, ch, err)
	}
	out, err := tpl.Render(payload.Params)
	if err != nil {
		return domain.Rendered{}, err
	}
	return out, nil
}

// markRetrying 记一次可重试失败；预算未耗尽时返回错误交 Outbox 退避重投。
func (o *Orchestrator) markRetrying(ctx context.Context, n domain.Notification, d domain.Delivery,
	sendErr *SendError, event *outbox.Event) error {
	budget := o.budgets[n.Priority]
	if d.Attempt >= budget {
		// 预算耗尽：落终态并吸收（通知侧补偿走 Replay）。
		return o.markFailed(ctx, n, d, sendErr)
	}
	next, err := d.Transition(domain.DeliveryRetrying, o.now())
	if err != nil {
		return apperr.Internal("advance delivery to retrying failed", err)
	}
	next.Attempt = d.Attempt + 1
	if sendErr != nil {
		failed, ferr := next.WithFailure(sendErr.Semantic, sendErr.ProviderCode, sendErr.Summary)
		if ferr == nil {
			next = failed
		}
	}
	if event != nil && event.NextRetryAt != nil {
		moment := *event.NextRetryAt
		next.NextRetryAt = &moment
	}
	if err := o.deliveries.Update(ctx, next); err != nil {
		return apperr.Internal("persist retrying delivery failed", err)
	}
	semantic := ""
	if sendErr != nil {
		semantic = sendErr.Semantic
	}
	o.observer.DeliveryObserved(d.Channel, d.Provider, "retrying", semantic)
	if sendErr != nil {
		return fmt.Errorf("notification delivery retrying: %w", sendErr)
	}
	return nil
}

// markFailed 落失败终态；返回 retry.Permanent 让事件进 DLQ
// （载荷是 Replay 补偿的唯一数据源），调用方不得吞掉。
func (o *Orchestrator) markFailed(ctx context.Context, n domain.Notification, d domain.Delivery, sendErr *SendError) error {
	semantic, providerCode, summary := SemanticInternal, "", "send failed"
	if sendErr != nil {
		semantic, providerCode, summary = sendErr.Semantic, sendErr.ProviderCode, sendErr.Summary
	}
	failed, err := d.Transition(domain.DeliveryFailed, o.now())
	if err != nil {
		if errors.Is(err, domain.ErrDeliveryTerminal) {
			// 已终态（并发收口），幂等吸收。
			return nil
		}
		return apperr.Internal("advance delivery to failed failed", err)
	}
	failed.Attempt = d.Attempt + 1
	if annotated, ferr := failed.WithFailure(semantic, providerCode, summary); ferr == nil {
		failed = annotated
	}
	if err := o.deliveries.Update(ctx, failed); err != nil {
		return apperr.Internal("persist failed delivery failed", err)
	}
	o.observer.DeliveryObserved(d.Channel, d.Provider, "failed", semantic)
	return retry.Permanent(fmt.Errorf("notification %s channel %s failed (%s): %s",
		n.ID, d.Channel, semantic, summary))
}

// finalizeReject 通知单整体拒发（黑名单 / 退订全命中 / 过期 / 无地址）。
func (o *Orchestrator) finalizeReject(ctx context.Context, n domain.Notification, semantic, reason string) error {
	next, err := n.Transition(domain.RequestRejected, reason, o.now())
	if err != nil {
		if errors.Is(err, domain.ErrRequestTerminal) {
			return nil
		}
		return apperr.Internal("reject notification failed", err)
	}
	if err := o.requests.UpdateStatus(ctx, next); err != nil && !errors.Is(err, domain.ErrVersionConflict) {
		return apperr.Internal("persist rejected notification failed", err)
	}
	o.observer.RequestObserved(n.Scene, domain.RequestRejected)
	// 拒发是安全控制生效的证明，需要审计留痕（规划 §2.K）。
	if err := o.auditCtx.emit(ctx, Actor{SubjectID: "system", ActorType: "system", Channel: "system"},
		ActionRequestRejected, "notification_request", n.ID,
		audit.OutcomeSuccess, reason, map[string]string{"scene": n.Scene, "semantic": semantic}); err != nil {
		return apperr.Internal("emit reject audit failed", err)
	}
	return nil
}

// convergeRequest 依据渠道明细收敛通知单状态（dispatched → sent / failed）。
func (o *Orchestrator) convergeRequest(ctx context.Context, requestID string) error {
	deliveries, err := o.deliveries.ListByRequest(ctx, requestID)
	if err != nil {
		return apperr.Internal("list deliveries failed", err)
	}
	n, err := o.requests.Get(ctx, requestID)
	if err != nil {
		return apperr.Internal("reload notification failed", err)
	}
	want := domain.RecomputeStatus(deliveries)
	if want == n.Status {
		return nil
	}
	next, err := n.Transition(want, "", o.now())
	if err != nil {
		// 回退类迁移（如 delivered 后重算出 sent）被状态机拒绝是正常的。
		return nil
	}
	if err := o.requests.UpdateStatus(ctx, next); err != nil && !errors.Is(err, domain.ErrVersionConflict) {
		return apperr.Internal("converge notification status failed", err)
	}
	o.observer.RequestObserved(n.Scene, next.Status)
	return nil
}

// classifySendError 把驱动错误归一化为 SendError：
// 已是 SendError 原样返回；其余按 internal（不可重试）处理——
// 驱动未知错误默认失败关闭，驱动实现应主动返回 SendError。
func classifySendError(err error) *SendError {
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		return sendErr
	}
	return &SendError{Semantic: SemanticInternal, Summary: err.Error(), Cause: err}
}

func isRetryable(err error) bool {
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		return sendErr.Retryable
	}
	return false
}

// providerKey / recipientKey 生成频控键；收件人键只含摘要（不落 PII）。
func providerKey(priority domain.Priority, channel domain.Channel) string {
	return "notify:" + string(priority) + ":" + string(channel)
}

func recipientKey(tenantID, address string) string {
	return "notify:rcpt:" + hashTarget(tenantID+"|"+address)
}

// hashTarget 与黑名单口径一致：SHA-256（黑名单加盐由 TargetHasher 负责，
// 这里的键只用于频控计数，不承担防枚举职责）。
func hashTarget(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
