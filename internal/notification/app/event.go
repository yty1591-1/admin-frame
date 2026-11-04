package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// TopicNotification 是通知请求事件的消费主题。
const TopicNotification = "notification.requested"

// SchemaVersionV1 是通知请求载荷的当前 schema 版本；
// 演进规则（规划开放问题 #6）：只加字段不破变更，破变更升 major。
const SchemaVersionV1 = 1

// RequestPayload 是 notification.requested 的事件载荷契约。
//
// 与通知单表（PII 最小化）分工：明文地址与渲染参数只存在于
// 事件载荷与渲染瞬时内存——Outbox 事件随 retention 清理，
// 通知表是 90 天长期数据，两者不可混。
type RequestPayload struct {
	// SchemaVersion 载荷版本；消费端拒绝不认识的 major 版本。
	SchemaVersion int `json:"schema_version"`
	// RequestID 通知单 ID（雪花，消费端 Create 的主键）。
	RequestID string `json:"request_id"`
	// TenantID 租户隔离键；平台级通知传 domain.PlatformTenantID。
	TenantID string `json:"tenant_id"`
	// Scene 业务场景键。
	Scene string `json:"scene"`
	// SceneClass 场景分类（偏好判定的依据，与 domain.SceneClass 对齐）。
	SceneClass string `json:"scene_class"`
	// RecipientRef 收件人引用（subject_id 或 HMAC，落库值）。
	RecipientRef string `json:"recipient_ref"`
	// Recipient 收件人寻址（仅事件载荷，不落库）。
	Recipient Recipient `json:"recipient"`
	// Channels 期望渠道（按优先顺序）。
	Channels []string `json:"channels"`
	// TemplateKey 渲染模板键。
	TemplateKey string `json:"template_key"`
	// Locale 渲染语言；空取消费端默认。
	Locale string `json:"locale"`
	// Params 渲染参数（可含 PII，仅事件载荷与渲染内存）。
	Params map[string]string `json:"params"`
	// IdempotencyKey 幂等键（业务侧语义键，如 notify:tenant.invitation:inv-123）。
	IdempotencyKey string `json:"idempotency_key"`
	// Priority 优先级。
	Priority string `json:"priority"`
	// Actor 触发主体。
	Actor string `json:"actor"`
	// Reference 业务引用（低基数非 PII）。
	Reference string `json:"reference"`
	// ExpiresAt 过期时间（RFC3339；零值由消费端补默认时效）。
	ExpiresAt time.Time `json:"expires_at"`
	// ReplayOf 重放来源通知单 ID（补偿链路留痕；首投为空）。
	ReplayOf string `json:"replay_of,omitempty"`
}

// Recipient 是收件人寻址载体：渠道地址按需填充，
// 有效性由投递渠道自行判定（如 email 非空且含 @）。
type Recipient struct {
	// UserID 已注册主体（偏好判定的 subject_id；匿名目标为空）。
	UserID string `json:"user_id,omitempty"`
	// Email 邮箱地址（email 渠道）。
	Email string `json:"email,omitempty"`
	// Phone 手机号 E.164（sms 渠道）。
	Phone string `json:"phone,omitempty"`
	// PushToken 设备推送令牌（push 渠道）。
	PushToken string `json:"push_token,omitempty"`
}

// AddressFor 返回渠道对应的原始地址；无对应地址返回空串
// （编排层把「无地址」视为不可达终态，不重试）。
func (r Recipient) AddressFor(channel domain.Channel) string {
	switch channel {
	case domain.ChannelEmail:
		return r.Email
	case domain.ChannelSms:
		return r.Phone
	case domain.ChannelPush:
		return r.PushToken
	default:
		return ""
	}
}

// ParamsDigest 计算渲染参数摘要（与通知单 params_digest 列对齐）：
// 规范化 JSON 后 SHA-256，仅用于对账比对，不含明文。
func (p RequestPayload) ParamsDigest() string {
	if len(p.Params) == 0 {
		return ""
	}
	normalized, err := json.Marshal(p.Params)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

// Validate 校验载荷契约；错误一律 retry.Permanent 包装
// （载荷非法是毒消息，重试无意义，直接进 DLQ）。
func (p RequestPayload) Validate() error {
	if p.SchemaVersion != SchemaVersionV1 {
		return retry.Permanent(fmt.Errorf("notification: unsupported payload schema_version %d", p.SchemaVersion))
	}
	if strings.TrimSpace(p.RequestID) == "" {
		return retry.Permanent(fmt.Errorf("notification: payload request_id is required"))
	}
	if strings.TrimSpace(p.TenantID) == "" {
		return retry.Permanent(fmt.Errorf("notification: payload tenant_id is required"))
	}
	if strings.TrimSpace(p.Scene) == "" {
		return retry.Permanent(fmt.Errorf("notification: payload scene is required"))
	}
	if strings.TrimSpace(p.IdempotencyKey) == "" {
		return retry.Permanent(fmt.Errorf("notification: payload idempotency_key is required"))
	}
	if len(p.Channels) == 0 {
		return retry.Permanent(fmt.Errorf("notification: payload channels is required"))
	}
	return nil
}

// parseRequestPayload 从 Outbox 事件解析并校验载荷。
func parseRequestPayload(event *outbox.Event) (RequestPayload, error) {
	if event == nil {
		return RequestPayload{}, retry.Permanent(fmt.Errorf("notification: nil outbox event"))
	}
	var payload RequestPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return RequestPayload{}, retry.Permanent(fmt.Errorf("notification: decode payload: %w", err))
	}
	if err := payload.Validate(); err != nil {
		return RequestPayload{}, err
	}
	return payload, nil
}

// OutboxDispatcher 是生产侧出口：业务域在自己事务内调用 Dispatch，
// 把通知请求写入 Outbox（对齐 audit.OutboxEmitter 的「事务内追加」模式）。
//
// 铁律（规划 5.1.3）：业务域禁止在事务里同步调渠道；Dispatch 只落
// Outbox，投递由通知域异步完成。调用方必须保证 Dispatch 与业务写入
// 同事务（pkg/db.WithinUnitOfWork），业务回滚则通知一并回滚。
type OutboxDispatcher struct {
	// Topic 消费主题；空取 TopicNotification。
	Topic string
	// Events 通用 Outbox Store；nil 时 Dispatch 报错（禁止静默丢弃）。
	Events outbox.Store
	// Now 时钟注入（测试）；nil 取 time.Now。
	Now func() time.Time
}

// NewOutboxDispatcher 构造生产侧出口；Store 必填。
func NewOutboxDispatcher(events outbox.Store) (*OutboxDispatcher, error) {
	if events == nil {
		return nil, apperr.Internal("notification dispatcher requires an outbox store", nil)
	}
	return &OutboxDispatcher{Topic: TopicNotification, Events: events, Now: time.Now}, nil
}

// Dispatch 校验载荷并追加一条 pending Outbox 事件。
//
// 事件主键 = 请求 ID：同 ID 重复 Dispatch 幂等失败（调用方视为已受理）。
func (d *OutboxDispatcher) Dispatch(ctx context.Context, payload RequestPayload) error {
	if d.Events == nil {
		return apperr.Internal("notification dispatcher is not configured", nil)
	}
	if payload.SchemaVersion == 0 {
		payload.SchemaVersion = SchemaVersionV1
	}
	if err := payload.Validate(); err != nil {
		return apperr.BadRequest(err.Error())
	}
	envelope, err := d.encodeEnvelope(payload)
	if err != nil {
		return apperr.BadRequest(err.Error())
	}
	event := &outbox.Event{
		Envelope:      envelope,
		Topic:         d.Topic,
		AggregateType: "notification",
		AggregateID:   payload.RequestID,
		Status:        outbox.StatusPending,
	}
	return d.Events.Append(ctx, event)
}

// encodeEnvelope 构造事件信封；Payload 携带 ID / Tenant / Trace 上下文。
func (d *OutboxDispatcher) encodeEnvelope(payload RequestPayload) (mq.Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return mq.Envelope{}, fmt.Errorf("notification: encode payload: %w", err)
	}
	nowFn := d.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return mq.Envelope{
		EventID:        payload.RequestID,
		EventType:      TopicNotification,
		Version:        "1",
		TenantID:       payload.TenantID,
		OccurredAt:     nowFn().UTC(),
		IdempotencyKey: payload.IdempotencyKey,
		Payload:        body,
	}, nil
}
