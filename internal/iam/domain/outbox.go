package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// OutboxEventType 是 outbox 事件的投递类型。
type OutboxEventType string

const (
	// OutboxEventAudit 审计事件（载荷为 audit.Event 的 JSON）。
	OutboxEventAudit OutboxEventType = "audit.event"
	// OutboxEventCacheInvalidate 缓存失效事件（载荷为失效目标）。
	OutboxEventCacheInvalidate OutboxEventType = "cache.invalidate"
)

// Validate 校验事件类型取值。
func (t OutboxEventType) Validate() error {
	switch t {
	case OutboxEventAudit, OutboxEventCacheInvalidate:
		return nil
	default:
		return errInvalid(ErrInvalidOutboxEvent, "unsupported outbox event type %q", string(t))
	}
}

// OutboxStatus 是 outbox 事件状态。
//
// 状态机：pending → processing → succeeded；
// processing 投递失败时回到 pending（attempts + 1）等待退避重试，
// 重试耗尽进入 dead；载荷永久不可解析进入 failed（毒消息，不再重试）。
type OutboxStatus string

const (
	// OutboxPending 待投递（含等待退避重试）。
	OutboxPending OutboxStatus = "pending"
	// OutboxProcessing 已被某个 worker 认领，投递中。
	OutboxProcessing OutboxStatus = "processing"
	// OutboxSucceeded 投递成功。
	OutboxSucceeded OutboxStatus = "succeeded"
	// OutboxFailed 毒消息：载荷永久不可投递，不再重试。
	OutboxFailed OutboxStatus = "failed"
	// OutboxDead 重试耗尽，进入死信，需人工处理。
	OutboxDead OutboxStatus = "dead"
)

// Validate 校验状态取值。
func (s OutboxStatus) Validate() error {
	switch s {
	case OutboxPending, OutboxProcessing, OutboxSucceeded, OutboxFailed, OutboxDead:
		return nil
	default:
		return errInvalid(ErrInvalidOutboxEvent, "unsupported outbox status %q", string(s))
	}
}

// MaxOutboxPayloadBytes 是单条事件载荷上限（防止把大对象塞进 outbox）。
const MaxOutboxPayloadBytes = 32 * 1024

// OutboxProcessingLease 是 processing 状态的租约。
//
// 投递成功后若标记成功时进程崩溃 / DB 抖动，事件会滞留在 processing；
// 超过租约仍未更新的事件视为「认领者已死」，由发布器回收为 pending 重新投递。
// 语义为至少一次：回收后可能重复投递，审计出口按 event_id 去重。
const OutboxProcessingLease = 2 * time.Minute

// OutboxEvent 是事务性 Outbox 中的一条待投递事件。
//
// 事件只承载「事实」（审计事件 / 缓存失效目标），不承载业务写操作；
// 载荷必须是 JSON 对象，且不得包含 Token / 密钥（由 audit.Event.Validate 兜底）。
type OutboxEvent struct {
	// ID 事件主键（雪花 ID），同时是投递顺序依据。
	ID string
	// Type 事件类型。
	Type OutboxEventType
	// AggregateType 聚合类型（iam_role / iam_binding / iam_breakglass）。
	AggregateType string
	// AggregateID 聚合 ID，用于缓存失效与按聚合排查投递情况。
	AggregateID string
	// Payload 事件载荷（JSON 对象）。
	Payload []byte
	// Status 当前投递状态。
	Status OutboxStatus
	// Attempts 已投递失败次数，指数退避依据。
	Attempts int
	// NextRetryAt 下次可投递时间；nil 表示立即可投递。
	NextRetryAt *time.Time
	// CreatedAt 事件创建时间（UTC），与业务写入同一次请求。
	CreatedAt time.Time
	// UpdatedAt 状态最后更新时间（UTC）。
	UpdatedAt time.Time
}

// NewOutboxEventParams 是创建 outbox 事件的入参。
type NewOutboxEventParams struct {
	ID            string
	Type          OutboxEventType
	AggregateType string
	AggregateID   string
	Payload       []byte
	Now           time.Time
}

// NewOutboxEvent 创建处于 pending 状态的事件，并校验载荷形状与大小。
func NewOutboxEvent(p NewOutboxEventParams) (*OutboxEvent, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox event id is required")
	}
	if err := p.Type.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.AggregateType) == "" {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox aggregate type is required")
	}
	if strings.TrimSpace(p.AggregateID) == "" {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox aggregate id is required")
	}
	payload, err := normalizeOutboxPayload(p.Payload)
	if err != nil {
		return nil, err
	}
	now := p.Now
	if now.IsZero() {
		now = nowUTC()
	}
	now = now.UTC()
	return &OutboxEvent{
		ID:            p.ID,
		Type:          p.Type,
		AggregateType: p.AggregateType,
		AggregateID:   p.AggregateID,
		Payload:       payload,
		Status:        OutboxPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// IsDue 报告事件在 now 时刻是否可被投递。
func (e *OutboxEvent) IsDue(now time.Time) bool {
	if e.Status != OutboxPending {
		return false
	}
	return e.NextRetryAt == nil || !now.Before(*e.NextRetryAt)
}

// Claim 把 pending 事件置为 processing；非 pending 返回冲突。
func (e *OutboxEvent) Claim(now time.Time) error {
	if e.Status != OutboxPending {
		return errConflict(ErrInvalidOutboxEvent, "outbox event %s is %s", e.ID, string(e.Status))
	}
	e.Status = OutboxProcessing
	e.UpdatedAt = now.UTC()
	return nil
}

// MarkSucceeded 标记投递成功。
func (e *OutboxEvent) MarkSucceeded(now time.Time) error {
	if e.Status != OutboxProcessing {
		return errConflict(ErrInvalidOutboxEvent, "outbox event %s is %s", e.ID, string(e.Status))
	}
	e.Status = OutboxSucceeded
	e.NextRetryAt = nil
	e.UpdatedAt = now.UTC()
	return nil
}

// MarkDead 标记重试耗尽，进入死信。
func (e *OutboxEvent) MarkDead(now time.Time) error {
	e.Status = OutboxDead
	e.NextRetryAt = nil
	e.UpdatedAt = now.UTC()
	return nil
}

// MarkPoison 标记载荷永久不可投递（不重试）。
func (e *OutboxEvent) MarkPoison(now time.Time) error {
	e.Status = OutboxFailed
	e.NextRetryAt = nil
	e.UpdatedAt = now.UTC()
	return nil
}

// Reschedule 记录一次投递失败并计算下次重试时间。
//
// attempts 使用事件自身计数：多次并发认领不会互相覆盖（认领是原子操作）。
func (e *OutboxEvent) Reschedule(now time.Time, policy OutboxRetryPolicy) error {
	if e.Status != OutboxProcessing {
		return errConflict(ErrInvalidOutboxEvent, "outbox event %s is %s", e.ID, string(e.Status))
	}
	e.Attempts++
	if policy.Exhausted(e.Attempts) {
		return e.MarkDead(now)
	}
	next := now.UTC().Add(policy.NextRetry(e.Attempts))
	e.Status = OutboxPending
	e.NextRetryAt = &next
	e.UpdatedAt = now.UTC()
	return nil
}

// OutboxRetryPolicy 是 outbox 投递的退避策略。
type OutboxRetryPolicy struct {
	// Base 首次重试间隔；≤0 时取 1s。
	Base time.Duration
	// Max 单次重试间隔上限；≤0 时取 5m。
	Max time.Duration
	// MaxAttempts 最大投递次数；≤0 时取 8。
	MaxAttempts int
}

// DefaultOutboxRetryPolicy 返回默认退避策略（1s 起，封顶 5m，最多 8 次）。
func DefaultOutboxRetryPolicy() OutboxRetryPolicy {
	return OutboxRetryPolicy{Base: time.Second, Max: 5 * time.Minute, MaxAttempts: 8}
}

// Normalize 补齐非法 / 零值字段，避免调用方漏配置导致无限快速重试。
func (p OutboxRetryPolicy) Normalize() OutboxRetryPolicy {
	if p.Base <= 0 {
		p.Base = time.Second
	}
	if p.Max <= 0 {
		p.Max = 5 * time.Minute
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 8
	}
	return p
}

// Exhausted 报告已用次数是否达到上限。
func (p OutboxRetryPolicy) Exhausted(attempts int) bool {
	p = p.Normalize()
	return attempts >= p.MaxAttempts
}

// NextRetry 返回第 attempts 次失败后的退避间隔（指数增长，封顶 Max）。
func (p OutboxRetryPolicy) NextRetry(attempts int) time.Duration {
	p = p.Normalize()
	if attempts <= 1 {
		return p.Base
	}
	delay := p.Base
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.Max {
			return p.Max
		}
	}
	if delay > p.Max {
		return p.Max
	}
	return delay
}

// normalizeOutboxPayload 校验载荷为单个 JSON 对象且不超过上限。
func normalizeOutboxPayload(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox payload is required")
	}
	if len(payload) > MaxOutboxPayloadBytes {
		return nil, errInvalid(ErrInvalidOutboxEvent,
			"outbox payload exceeds %d bytes", MaxOutboxPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox payload must be a JSON object: %v", err)
	}
	if object == nil {
		return nil, errInvalid(ErrInvalidOutboxEvent, "outbox payload must be a JSON object")
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return nil, errInvalid(ErrInvalidOutboxEvent,
			"outbox payload must contain exactly one JSON object")
	}
	return append([]byte(nil), payload...), nil
}
