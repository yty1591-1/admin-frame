package outbox

import (
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/mq"
)

// Status 是 Outbox 事件状态。
//
// 状态机：pending → processing → succeeded；
// processing 投递失败回到 pending（attempts + 1）等待退避重试；
// 重试耗尽进入 dead，载荷永久不可投递进入 failed；两者都会写入 async_dlq。
type Status string

const (
	// StatusPending 待投递（含等待退避重试）。
	StatusPending Status = "pending"
	// StatusProcessing 已被某个 Relay 认领，投递中。
	StatusProcessing Status = "processing"
	// StatusSucceeded 投递成功。
	StatusSucceeded Status = "succeeded"
	// StatusFailed 永久不可投递（毒消息），不再重试。
	StatusFailed Status = "failed"
	// StatusDead 重试耗尽，进入死信。
	StatusDead Status = "dead"
)

// Validate 校验状态取值。
func (s Status) Validate() error {
	switch s {
	case StatusPending, StatusProcessing, StatusSucceeded, StatusFailed, StatusDead:
		return nil
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidEvent, string(s))
	}
}

// Event 是事务性 Outbox 中的一条待投递事件。
//
// 事件只承载事实，不承载业务写操作；Payload 必须是 JSON 对象。
type Event struct {
	// Envelope 事件信封（EventID / EventType / Version / TenantID / OccurredAt / TraceID / Payload）。
	mq.Envelope
	// Topic 消费路由主题；为空时回退 EventType。
	Topic string
	// AggregateType 聚合类型，用于排查与去重，可空。
	AggregateType string
	// AggregateID 聚合 ID，可空。
	AggregateID string
	// Status 当前投递状态。
	Status Status
	// Attempts 已投递失败次数。
	Attempts int
	// NextRetryAt 下次可投递时间；nil 表示立即可投递。
	NextRetryAt *time.Time
	// LastError 最近一次失败原因（截断到 512 字节）。
	LastError string
	// CreatedAt 入盒时间（UTC）。
	CreatedAt time.Time
	// UpdatedAt 状态最后更新时间（UTC）。
	UpdatedAt time.Time
}

// NewEventParams 是创建 Outbox 事件的入参。
type NewEventParams struct {
	// Topic 消费路由主题；为空时回退 EventType。
	Topic         string
	Envelope      mq.Envelope
	AggregateType string
	AggregateID   string
	// Now 入盒时间；零值取当前时间。
	Now time.Time
}

// NewEvent 创建处于 pending 状态的事件并校验信封。
func NewEvent(p NewEventParams) (*Event, error) {
	envelope := p.Envelope.Normalize()
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	return &Event{
		Envelope:      envelope,
		Topic:         normalizeTopic(p.Topic, envelope.EventType),
		AggregateType: p.AggregateType,
		AggregateID:   p.AggregateID,
		Status:        StatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// IsDue 报告事件在 now 时刻是否可被投递。
func (e *Event) IsDue(now time.Time) bool {
	if e == nil || e.Status != StatusPending {
		return false
	}
	return e.NextRetryAt == nil || !now.Before(*e.NextRetryAt)
}

// Claim 把 pending 事件置为 processing；非 pending 返回冲突。
func (e *Event) Claim(now time.Time) error {
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if e.Status != StatusPending {
		return fmt.Errorf("%w: event %s is %s", ErrInvalidState, e.EventID, string(e.Status))
	}
	e.Status = StatusProcessing
	e.UpdatedAt = now.UTC()
	return nil
}

// MarkSucceeded 标记投递成功；仅允许从 processing 迁移。
func (e *Event) MarkSucceeded(now time.Time) error {
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if e.Status != StatusProcessing {
		return fmt.Errorf("%w: event %s is %s", ErrInvalidState, e.EventID, string(e.Status))
	}
	e.Status = StatusSucceeded
	e.NextRetryAt = nil
	e.UpdatedAt = now.UTC()
	return nil
}

// NextAttempt 计算下一次重试参数。
//
// attempts 为本次失败后的累计次数；exhausted 为 true 时调用方应转入死信。
func (e *Event) NextAttempt(policy RetryPolicy, now time.Time) (attempts int, next time.Time, exhausted bool) {
	attempts = e.Attempts + 1
	if policy.Exhausted(attempts) {
		return attempts, time.Time{}, true
	}
	return attempts, now.UTC().Add(policy.NextRetry(attempts)), false
}

// MarkDead 标记重试耗尽；仅允许从 processing 迁移。
func (e *Event) MarkDead(now time.Time, cause string) error {
	return e.terminate(StatusDead, now, cause)
}

// MarkFailed 标记载荷永久不可投递；仅允许从 processing 迁移。
func (e *Event) MarkFailed(now time.Time, cause string) error {
	return e.terminate(StatusFailed, now, cause)
}

func (e *Event) terminate(status Status, now time.Time, cause string) error {
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if e.Status != StatusProcessing {
		return fmt.Errorf("%w: event %s is %s", ErrInvalidState, e.EventID, string(e.Status))
	}
	e.Status = status
	e.NextRetryAt = nil
	e.LastError = truncateCause(cause)
	e.UpdatedAt = now.UTC()
	return nil
}

// Clone 深拷贝事件，避免 Store 与 Relay 并发修改同一对象。
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	out := *e
	out.Payload = append([]byte(nil), e.Payload...)
	if e.NextRetryAt != nil {
		next := *e.NextRetryAt
		out.NextRetryAt = &next
	}
	return &out
}

// NormalizeTopic 返回路由主题；空时回退 eventType。
func NormalizeTopic(topic, eventType string) string {
	return normalizeTopic(topic, eventType)
}

func normalizeTopic(topic, eventType string) string {
	if trimmed := strings.TrimSpace(topic); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(eventType)
}

// maxCauseBytes 是 last_error 列宽（VARCHAR(512) 按字符计，留出截断余量）。
const maxCauseBytes = 512

func truncateCause(cause string) string {
	cause = strings.TrimSpace(cause)
	if len(cause) <= maxCauseBytes {
		return cause
	}
	return cause[:maxCauseBytes]
}
