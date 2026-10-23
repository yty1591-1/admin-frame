package mq

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// MaxEventPayloadBytes 是单个事件载荷上限，防止把大对象塞进消息总线 / outbox。
const MaxEventPayloadBytes = 32 * 1024

// Envelope 是跨驱动统一的事件信封（ENT-03 T1）。
//
// 字段语义：
//   - EventID 全局唯一事件 ID，同时作为消费幂等键的默认值；
//   - EventType 事件类型（如 iam.audit.recorded），消费方按此路由；
//   - Version schema 版本，如 v1；不兼容变更必须发布新版本；
//   - TenantID 租户隔离键，可空；
//   - OccurredAt 业务发生时间（UTC）；
//   - TraceID 与 OBS / RequestID 对齐，可空；
//   - IdempotencyKey 可选的业务幂等键，空时消费方回退 EventID；
//   - Payload 必须是单个 JSON 对象。
type Envelope struct {
	EventID        string
	EventType      string
	Version        string
	TenantID       string
	OccurredAt     time.Time
	TraceID        string
	IdempotencyKey string
	Payload        []byte
}

// Normalize 补齐可空字段：OccurredAt 为零值时取当前时间，Version 为空时取 v1。
//
// 返回拷贝，不修改接收者。
func (e Envelope) Normalize() Envelope {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	} else {
		e.OccurredAt = e.OccurredAt.UTC()
	}
	if strings.TrimSpace(e.Version) == "" {
		e.Version = DefaultEventVersion
	}
	return e
}

// Validate 校验信封必填项、载荷形状与大小。
func (e Envelope) Validate() error {
	if strings.TrimSpace(e.EventID) == "" {
		return fmt.Errorf("%w: event_id is required", ErrInvalidMessage)
	}
	if strings.TrimSpace(e.EventType) == "" {
		return fmt.Errorf("%w: event_type is required", ErrInvalidMessage)
	}
	if strings.TrimSpace(e.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidMessage)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidMessage)
	}
	return validateEventPayload(e.Payload)
}

// ToMessage 把信封转换为可发布消息；topic 为空时使用 EventType。
func (e Envelope) ToMessage(topic string) Message {
	if strings.TrimSpace(topic) == "" {
		topic = e.EventType
	}
	headers := map[string]string{
		HeaderEventID:        e.EventID,
		HeaderEventType:      e.EventType,
		HeaderEventVersion:   e.Version,
		HeaderOccurredAt:     e.OccurredAt.UTC().Format(time.RFC3339Nano),
		HeaderSchemaVersion:  e.Version,
		HeaderContentType:    "application/json",
		HeaderMessageID:      e.EventID,
		HeaderIdempotencyKey: e.idempotencyKey(),
	}
	if e.TenantID != "" {
		headers[HeaderTenantID] = e.TenantID
	}
	if e.TraceID != "" {
		headers[HeaderTraceID] = e.TraceID
	}
	return Message{
		ID:        e.EventID,
		Topic:     topic,
		Headers:   headers,
		Body:      append([]byte(nil), e.Payload...),
		Timestamp: e.OccurredAt.UTC(),
	}
}

// EnvelopeFromMessage 从消息还原信封；缺少事件 Header 时返回错误。
func EnvelopeFromMessage(msg Message) (Envelope, error) {
	e := Envelope{
		EventID:        HeaderGet(msg.Headers, HeaderEventID),
		EventType:      HeaderGet(msg.Headers, HeaderEventType),
		Version:        HeaderGet(msg.Headers, HeaderEventVersion),
		TenantID:       HeaderGet(msg.Headers, HeaderTenantID),
		TraceID:        HeaderGet(msg.Headers, HeaderTraceID),
		IdempotencyKey: HeaderGet(msg.Headers, HeaderIdempotencyKey),
		Payload:        append([]byte(nil), msg.Body...),
	}
	if e.Version == "" {
		e.Version = HeaderGet(msg.Headers, HeaderSchemaVersion)
	}
	if raw := HeaderGet(msg.Headers, HeaderOccurredAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Envelope{}, fmt.Errorf("%w: invalid occurred_at %q", ErrInvalidMessage, raw)
		}
		e.OccurredAt = parsed.UTC()
	} else {
		e.OccurredAt = msg.Timestamp.UTC()
	}
	if err := e.Validate(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// IdempotencyKeyOrEventID 返回消费幂等键：优先业务键，缺省回退 EventID。
func (e Envelope) IdempotencyKeyOrEventID() string {
	if key := strings.TrimSpace(e.IdempotencyKey); key != "" {
		return key
	}
	return e.EventID
}

func (e Envelope) idempotencyKey() string {
	if e.IdempotencyKey != "" {
		return e.IdempotencyKey
	}
	return e.EventID
}

// validateEventPayload 校验载荷为单个 JSON 对象且不超过上限。
func validateEventPayload(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: payload is required", ErrInvalidMessage)
	}
	if len(payload) > MaxEventPayloadBytes {
		return fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidMessage, MaxEventPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return fmt.Errorf("%w: payload must be a JSON object: %v", ErrInvalidMessage, err)
	}
	if object == nil {
		return fmt.Errorf("%w: payload must be a JSON object", ErrInvalidMessage)
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: payload must contain exactly one JSON object", ErrInvalidMessage)
	}
	return nil
}
