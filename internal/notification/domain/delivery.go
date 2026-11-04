package domain

import (
	"fmt"
	"strings"
	"time"
)

// DeliveryStatus 是单渠道投递的生命周期状态。
//
// 与通知单状态（RequestStatus）分层：一次编排内渠道可能各处不同阶段，
// 通知单状态只做单调收敛，明细状态承载全部中间态。
type DeliveryStatus string

// 投递状态取值。
const (
	// DeliveryPending 渠道明细已建立，尚未调用供应商。
	DeliveryPending DeliveryStatus = "pending"
	// DeliveryRetrying 可重试失败，等待 Outbox 退避后再次尝试；
	// attempt 计数随之递增，重试预算由编排层按优先级控制。
	DeliveryRetrying DeliveryStatus = "retrying"
	// DeliveryAccepted 渠道侧受理成功（SMTP 251 / 供应商 202 等）；
	// 对无回执渠道这是成功终态，通知单随之收敛 sent。
	DeliveryAccepted DeliveryStatus = "accepted"
	// DeliveryFailed 永久失败：不可重试错误，或可重试错误耗尽预算。
	// 记录保留语义码供排障，补偿走 DLQ 重放。
	DeliveryFailed DeliveryStatus = "failed"
	// DeliveryDelivered 回执确认送达（仅限有回执渠道）。
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryUndelivered 回执明确失败 / 送达超时（渠道侧判定，终态）。
	DeliveryUndelivered DeliveryStatus = "undelivered"
)

// Valid 判断状态是否已定义。
func (s DeliveryStatus) Valid() bool {
	switch s {
	case DeliveryPending, DeliveryRetrying, DeliveryAccepted,
		DeliveryFailed, DeliveryDelivered, DeliveryUndelivered:
		return true
	default:
		return false
	}
}

// Sent 判断是否渠道侧受理成功（accepted 及其后继终态）。
func (s DeliveryStatus) Sent() bool {
	switch s {
	case DeliveryAccepted, DeliveryDelivered, DeliveryUndelivered:
		// undelivered 渠道侧明确「未送达」，但受理动作已完成；
		// 通知单层面的失败语义由回执 Transition 显式处理。
		return true
	default:
		return false
	}
}

// Terminal 判断是否终态：终态明细不再接受更新。
func (s DeliveryStatus) Terminal() bool {
	switch s {
	case DeliveryAccepted, DeliveryFailed, DeliveryDelivered, DeliveryUndelivered:
		return true
	default:
		return false
	}
}

// deliveryTransitions 是投递明细状态机的唯一真相。
//
// accepted 之后只允许回执类迁移；failed 是吸收态（重试耗尽或永久错误），
// 重新投递必须走 DLQ 重放生成新事件，不允许把 failed 改回 retrying。
var deliveryTransitions = map[DeliveryStatus][]DeliveryStatus{
	DeliveryPending:  {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
	DeliveryRetrying: {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
	DeliveryAccepted: {DeliveryDelivered, DeliveryUndelivered},
}

// CanTransitionTo 判断投递状态迁移是否合法。
func (s DeliveryStatus) CanTransitionTo(target DeliveryStatus) bool {
	for _, allowed := range deliveryTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// DeliveryTransitions 返回投递状态机快照（副本），供测试与门禁消费。
func DeliveryTransitions() map[DeliveryStatus][]DeliveryStatus {
	out := make(map[DeliveryStatus][]DeliveryStatus, len(deliveryTransitions))
	for from, targets := range deliveryTransitions {
		copied := make([]DeliveryStatus, len(targets))
		copy(copied, targets)
		out[from] = copied
	}
	return out
}

// 投递明细长度上限（与迁移 000016 列宽对齐）。
const (
	// MaxProviderLength 供应商标识上限（如 aliyun / smtp / apns）。
	MaxProviderLength = 32
	// MaxSemanticCodeLength 语义码上限（编排层归一化的错误分类）。
	MaxSemanticCodeLength = 64
	// MaxProviderCodeLength 供应商原始码上限（仅排障用，非 PII）。
	MaxProviderCodeLength = 64
	// MaxMessageIDLength 渠道消息 ID 上限（回执对账键）。
	MaxMessageIDLength = 128
	// MaxDeliveryErrorLength 失败摘要上限（脱敏后的一句话）。
	MaxDeliveryErrorLength = 255
)

// Delivery 是一个渠道上的一次投递记录（request_id, channel 唯一）。
//
// attempt 与 Outbox 重试计数分离：同一事件可被认领多次，但一次认领
// 内多个渠道各有一条明细；attempt 是渠道维度的累计尝试次数。
type Delivery struct {
	// ID 投递明细 ID。
	ID string
	// RequestID 所属通知单 ID。
	RequestID string
	// TenantID 租户隔离键（与通知单一致，冗余存储便于直查）。
	TenantID string
	// Channel 投递渠道。
	Channel Channel
	// Provider 实际使用的供应商标识（路由主备切换后记录实际值）。
	Provider string
	// Status 当前状态。
	Status DeliveryStatus
	// Attempt 累计尝试次数（含失败尝试）。
	Attempt int
	// SemanticCode 编排层归一化的失败语义码（如 rate_limited、timeout）。
	SemanticCode string
	// ProviderCode 供应商原始错误码（仅排障，不入指标标签）。
	ProviderCode string
	// LastError 脱敏后的失败摘要（不含收件人与正文）。
	LastError string
	// MessageID 渠道消息 ID（回执对账键）。
	MessageID string
	// NextRetryAt 下次重试时间（retrying 状态非空）。
	NextRetryAt *time.Time
	// FinishedAt 终态时间。
	FinishedAt *time.Time
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// NewDelivery 构造 pending 投递明细。
func NewDelivery(id, requestID, tenantID string, channel Channel, now time.Time) (Delivery, error) {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return Delivery{}, fmt.Errorf("notification: delivery id is required")
	}
	trimmedRequest := strings.TrimSpace(requestID)
	if trimmedRequest == "" {
		return Delivery{}, fmt.Errorf("notification: request id is required")
	}
	trimmedTenant := strings.TrimSpace(tenantID)
	if trimmedTenant == "" {
		return Delivery{}, ErrTenantIDRequired
	}
	if !channel.Implemented() {
		return Delivery{}, &ChannelNotImplementedError{Channel: channel}
	}
	return Delivery{
		ID:        trimmedID,
		RequestID: trimmedRequest,
		TenantID:  trimmedTenant,
		Channel:   channel,
		Status:    DeliveryPending,
		Attempt:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// Validate 校验仓储回读的明细自洽性。
func (d Delivery) Validate() error {
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.RequestID) == "" {
		return fmt.Errorf("notification: delivery requires id and request id")
	}
	if !d.Status.Valid() {
		return fmt.Errorf("notification: unsupported delivery status %q", d.Status)
	}
	if d.Attempt <= 0 {
		return fmt.Errorf("notification: delivery attempt must be positive, got %d", d.Attempt)
	}
	if d.Status == DeliveryRetrying && d.NextRetryAt == nil {
		return fmt.Errorf("notification: retrying delivery %s has no next_retry_at", d.ID)
	}
	if d.Status.Terminal() && d.FinishedAt == nil {
		return fmt.Errorf("notification: terminal delivery %s has no finished_at", d.ID)
	}
	return nil
}

// Transition 迁移投递状态；attempt 增量由调用方在重试路径显式累加。
//
// nextRetryAt 仅 retrying 需要；终态自动回填 finished_at。
func (d Delivery) Transition(target DeliveryStatus, at time.Time) (Delivery, error) {
	if !target.Valid() {
		return Delivery{}, fmt.Errorf("notification: unsupported delivery status %q", target)
	}
	if d.Status.Terminal() {
		return Delivery{}, ErrDeliveryTerminal
	}
	if !d.Status.CanTransitionTo(target) {
		return Delivery{}, &InvalidDeliveryTransitionError{From: d.Status, To: target}
	}
	next := d
	next.Status = target
	next.UpdatedAt = at.UTC()
	switch {
	case target == DeliveryRetrying:
		moment := at.UTC()
		next.NextRetryAt = &moment
	case target.Terminal():
		moment := at.UTC()
		next.FinishedAt = &moment
		next.NextRetryAt = nil
	}
	return next, nil
}

// WithFailure 以失败结果更新明细的可观测字段（语义码 / 供应商码 / 摘要）。
//
// 调用方负责先做 PII 脱敏；这里只做长度约束。
func (d Delivery) WithFailure(semanticCode, providerCode, summary string) (Delivery, error) {
	if len([]rune(semanticCode)) > MaxSemanticCodeLength {
		return d, ErrSemanticCodeTooLong
	}
	if len([]rune(providerCode)) > MaxProviderCodeLength {
		return d, ErrProviderCodeTooLong
	}
	if len([]rune(summary)) > MaxDeliveryErrorLength {
		summary = string([]rune(summary)[:MaxDeliveryErrorLength])
	}
	next := d
	next.SemanticCode = strings.TrimSpace(semanticCode)
	next.ProviderCode = strings.TrimSpace(providerCode)
	next.LastError = summary
	return next, nil
}

// WithProvider 记录实际使用的供应商（路由 / 主备切换后）。
func (d Delivery) WithProvider(provider string) (Delivery, error) {
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return d, ErrProviderRequired
	}
	if len([]rune(trimmed)) > MaxProviderLength {
		return d, ErrProviderTooLong
	}
	next := d
	next.Provider = trimmed
	return next, nil
}
