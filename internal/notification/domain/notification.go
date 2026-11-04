// Package domain 定义通知域实体、状态机与仓储契约（ENT-10）。
//
// 本包不依赖渠道驱动（pkg/sms 等）、HTTP 或 outbox：通知是「要发什么」的
// 领域表达，与「怎么发」（渠道路由、重试、供应商）解耦；状态流转规则必须
// 能脱离基础设施单测，否则规则会散落在编排管道与 SQL 里。
//
// 与租户域（internal/tenant）同款范式：状态机白名单 + 快照测试是唯一真相，
// 迁移 000016 的列宽与本包长度常量一一对齐。
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Channel 是投递渠道枚举。
type Channel string

// 渠道取值；inbox 为站内信预留位（规划开放问题 #2：v1 不实现）。
const (
	// ChannelEmail 电子邮件（pkg/email 驱动）。
	ChannelEmail Channel = "email"
	// ChannelSms 短信（pkg/sms 驱动）。
	ChannelSms Channel = "sms"
	// ChannelPush 移动推送（pkg/push 驱动）。
	ChannelPush Channel = "push"
	// ChannelInbox 站内信（预留，v1 不实现，出现即拒绝）。
	ChannelInbox Channel = "inbox"
)

// Valid 判断渠道是否已定义。
func (c Channel) Valid() bool {
	switch c {
	case ChannelEmail, ChannelSms, ChannelPush, ChannelInbox:
		return true
	default:
		return false
	}
}

// Implemented 判断渠道是否已有投递实现：inbox 只是契约预留，
// 编排层遇到它必须拒绝而不是静默丢弃。
func (c Channel) Implemented() bool {
	return c.Valid() && c != ChannelInbox
}

// Priority 是投递优先级；影响重试预算与频控豁免。
type Priority string

// 优先级取值。
const (
	// PriorityCritical 关键通知（验证码、安全告警）：最高重试预算，频控只做供应商保护。
	PriorityCritical Priority = "critical"
	// PriorityNormal 常规业务通知（邀请、状态变更）。
	PriorityNormal Priority = "normal"
	// PriorityLow 运营 / 营销类：最低预算，受静默时段约束。
	PriorityLow Priority = "low"
)

// Valid 判断优先级是否已定义。
func (p Priority) Valid() bool {
	switch p {
	case PriorityCritical, PriorityNormal, PriorityLow:
		return true
	default:
		return false
	}
}

// RequestStatus 是通知单（聚合根）状态；逐渠道明细见 Delivery。
type RequestStatus string

// 通知单状态取值。
const (
	// RequestPending 已受理（请求行已落库），尚未开始投递编排。
	RequestPending RequestStatus = "pending"
	// RequestDispatched 至少一个渠道已进入投递流程（存在投递记录）。
	RequestDispatched RequestStatus = "dispatched"
	// RequestSent 全部渠道被渠道侧受理（有回执渠道的最终确认未到）。
	RequestSent RequestStatus = "sent"
	// RequestDelivered 回执确认送达（仅限有回执的渠道，如短信回执）。
	RequestDelivered RequestStatus = "delivered"
	// RequestUndelivered 回执明确失败 / 送达超时（终态，渠道侧判定）。
	RequestUndelivered RequestStatus = "undelivered"
	// RequestFailed 永久失败（可重试错误耗尽预算或不可重试错误）；
	// 补偿走 DLQ 重放，重放生成新事件、保留原幂等键防重复。
	RequestFailed RequestStatus = "failed"
	// RequestRejected 投递前被拦（偏好 / 黑名单 / 过期 / 不可达），未触达渠道。
	RequestRejected RequestStatus = "rejected"
)

// Valid 判断状态是否已定义。
func (s RequestStatus) Valid() bool {
	switch s {
	case RequestPending, RequestDispatched, RequestSent, RequestDelivered,
		RequestUndelivered, RequestFailed, RequestRejected:
		return true
	default:
		return false
	}
}

// Terminal 判断是否为终态：终态通知单不再接受状态更新，
// DLQ 重放是「新事件走新请求」，不复活旧行。
func (s RequestStatus) Terminal() bool {
	switch s {
	case RequestDelivered, RequestUndelivered, RequestFailed, RequestRejected:
		return true
	default:
		return false
	}
}

// transitions 是通知单状态机的唯一真相：只列合法迁移，未列出的一律拒绝。
//
// 迁移刻意保持单调（状态只进不退）：并发「回执送达 + 新渠道仍在编排」
// 不会把 delivered 打回 dispatched；渠道明细的中间态由 Delivery 自行表达。
var transitions = map[RequestStatus][]RequestStatus{
	RequestPending:    {RequestDispatched, RequestRejected},
	RequestDispatched: {RequestSent, RequestFailed, RequestRejected},
	RequestSent:       {RequestDelivered, RequestUndelivered},
}

// CanTransitionTo 判断从当前状态迁移到目标状态是否合法。
func (s RequestStatus) CanTransitionTo(target RequestStatus) bool {
	for _, allowed := range transitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// Transitions 返回迁移矩阵的快照（副本），供测试快照与门禁脚本消费。
//
// 矩阵变更必须同步更新 TestTransitionMatrixSnapshot 的快照并通过评审
// （对齐 check-tenant.sh TN-03 的门禁模式）。
func Transitions() map[RequestStatus][]RequestStatus {
	out := make(map[RequestStatus][]RequestStatus, len(transitions))
	for from, targets := range transitions {
		copied := make([]RequestStatus, len(targets))
		copy(copied, targets)
		out[from] = copied
	}
	return out
}

// 长度上限（与迁移 000016 列宽一一对齐）。
const (
	// MaxSceneLength 业务场景键上限（notification_request.scene）。
	MaxSceneLength = 64
	// MaxTemplateKeyLength 模板键上限（notification_request.template_key）。
	MaxTemplateKeyLength = 128
	// MaxIdempotencyKeyLength 幂等键上限（notification_request.idempotency_key）。
	MaxIdempotencyKeyLength = 128
	// MaxRecipientRefLength 收件人引用上限（notification_request.recipient_ref）。
	MaxRecipientRefLength = 128
	// MaxReferenceLength 业务引用上限（notification_request.reference）。
	MaxReferenceLength = 128
	// MaxActorLength 触发主体上限（notification_request.actor）。
	MaxActorLength = 64
	// MaxIDLength 通知单 / 投递 ID 上限（雪花字符串，与其他域 ID 同宽）。
	MaxIDLength = 64
	// MaxLocaleLength 语言标签上限（notification_request.locale）。
	MaxLocaleLength = 16
	// MaxRejectReasonLength 拒发原因上限。
	MaxRejectReasonLength = 255
	// DefaultNotificationTTL 通知默认时效：24 小时，过期即弃发。
	//
	// 迟到的通知大多是噪声（邀请已过期、事件已失效）；验证码类场景
	// 必须显式给更短的 expires_at。
	DefaultNotificationTTL = 24 * time.Hour
)

// PlatformTenantID 平台级通知的租户哨兵值（A 端代发、系统告警等
// 不属于任何业务租户的通知）。仓储不强制注入租户过滤的场景必须
// 用它显式声明，避免空串 tenant_id 混进业务隔离查询。
const PlatformTenantID = "platform"

// Notification 是一条通知请求（聚合根）。
//
// 正文不落库：params 只存摘要（params_digest），明文参数仅存在于
// Outbox 事件载荷与渲染瞬时内存——通知表是长期保留数据（90 天），
// 邮箱、验证码等 PII 不进这里。
type Notification struct {
	// ID 通知单 ID（雪花），由发起方（业务域 / 代发 API）生成。
	ID string
	// TenantID 租户隔离键；平台级通知取 PlatformTenantID。
	TenantID string
	// Scene 业务场景键（如 tenant.invitation、tenant.suspend_notice），
	// 是路由、偏好分类与配额分键的依据。
	Scene string
	// RecipientRef 收件人引用：已注册主体用 subject_id，匿名目标
	// （未注册邮箱 / 手机号）用加盐 HMAC——原始地址不落库。
	RecipientRef string
	// IdempotencyKey 幂等键（业务侧语义键，如 notify:tenant.invitation:inv-1）：
	// 全局唯一，重复事件 / 重放 / 双副本并发在此收敛为一次受理。
	IdempotencyKey string
	// Channels 期望渠道（按优先顺序）；编排层可按路由配置裁剪。
	Channels []Channel
	// TemplateKey 模板键；渲染所需模板必须存在且 enabled。
	TemplateKey string
	// Locale 渲染语言标签；空取默认 locale。
	Locale string
	// ParamsDigest 渲染参数的 SHA-256 摘要（JSON 规范化后）：
	// 用于对账与排障比对，明文参数不落库（只存在于事件载荷与渲染内存）。
	ParamsDigest string
	// Priority 投递优先级。
	Priority Priority
	// Status 当前状态。
	Status RequestStatus
	// RejectReason 拒发原因（rejected 终态必填；运营可读）。
	RejectReason string
	// Version 乐观锁版本；回执回写与投递进度并发更新靠 CAS 收敛。
	Version int64
	// Actor 触发主体（操作者 ID 或系统标识）。
	Actor string
	// Reference 业务引用（低基数、非 PII，如邀请 ID）。
	Reference string
	// ExpiresAt 过期时间；编排时已过期直接弃发（rejected）。
	ExpiresAt time.Time
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// validSceneKey 放行场景键 / 模板键的安全字符集：小写字母、数字与 `.` `_` `-`。
//
// 场景键会出现在指标标签、路由配置与审计 attrs 里；限定字符集保证
// 它不会成为标签基数爆炸或注入的入口（与租户 code、配额资源键同一口径）。
func validSceneKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// validateLocale 校验语言标签：放行字母数字与 `_` `-` `.`，空串合法
// （取默认 locale）。完整 BCP-47 校验需要 tzdata 类环境依赖，领域层
// 只拦「明显不可能」的输入（对齐租户域 contact_email 的从宽口径）。
func validateLocale(value string) error {
	if value == "" {
		return nil
	}
	if len([]rune(value)) > MaxLocaleLength {
		return ErrLocaleTooLong
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return &InvalidLocaleError{Locale: value}
		}
	}
	return nil
}

// NewNotification 构造 pending 状态的通知单。
//
// 收件人引用由调用方完成脱敏（subject_id 或 HMAC），本构造器只做
// 结构与取值校验；params 不经过领域层（见结构注释）。
func NewNotification(id, tenantID, scene, recipientRef, idempotencyKey string, channels []Channel,
	templateKey, locale string, priority Priority, expiresAt time.Time, now time.Time) (Notification, error) {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return Notification{}, fmt.Errorf("notification: id is required")
	}
	trimmedTenant := strings.TrimSpace(tenantID)
	if trimmedTenant == "" {
		return Notification{}, ErrTenantIDRequired
	}
	if len([]rune(trimmedTenant)) > MaxActorLength {
		return Notification{}, ErrTenantIDTooLong
	}
	trimmedScene := strings.TrimSpace(scene)
	if trimmedScene == "" {
		return Notification{}, ErrSceneRequired
	}
	if len([]rune(trimmedScene)) > MaxSceneLength {
		return Notification{}, ErrSceneTooLong
	}
	if !validSceneKey(trimmedScene) {
		return Notification{}, &InvalidSceneError{Scene: trimmedScene}
	}
	trimmedRef := strings.TrimSpace(recipientRef)
	if trimmedRef == "" {
		return Notification{}, ErrRecipientRequired
	}
	if len([]rune(trimmedRef)) > MaxRecipientRefLength {
		return Notification{}, ErrRecipientTooLong
	}
	trimmedIdem := strings.TrimSpace(idempotencyKey)
	if trimmedIdem == "" {
		return Notification{}, ErrIdempotencyKeyRequired
	}
	if len([]rune(trimmedIdem)) > MaxIdempotencyKeyLength {
		return Notification{}, ErrIdempotencyKeyTooLong
	}
	if len(channels) == 0 {
		return Notification{}, ErrChannelRequired
	}
	seen := make(map[Channel]struct{}, len(channels))
	normalized := make([]Channel, 0, len(channels))
	for _, c := range channels {
		if !c.Valid() {
			return Notification{}, &InvalidChannelError{Channel: string(c)}
		}
		if !c.Implemented() {
			return Notification{}, &ChannelNotImplementedError{Channel: c}
		}
		if _, dup := seen[c]; dup {
			continue // 重复渠道静默去重：调用方顺序保留语义不受影响。
		}
		seen[c] = struct{}{}
		normalized = append(normalized, c)
	}
	trimmedKey := strings.TrimSpace(templateKey)
	if trimmedKey == "" {
		return Notification{}, ErrTemplateKeyRequired
	}
	if len([]rune(trimmedKey)) > MaxTemplateKeyLength {
		return Notification{}, ErrTemplateKeyTooLong
	}
	if !validSceneKey(trimmedKey) {
		return Notification{}, &InvalidTemplateKeyError{Key: trimmedKey}
	}
	if !priority.Valid() {
		return Notification{}, &InvalidPriorityError{Priority: string(priority)}
	}
	trimmedLocale := strings.TrimSpace(locale)
	if err := validateLocale(trimmedLocale); err != nil {
		return Notification{}, err
	}
	if len([]rune(trimmedID)) > MaxIDLength {
		return Notification{}, ErrIDTooLong
	}

	return Notification{
		ID:             trimmedID,
		TenantID:       trimmedTenant,
		Scene:          trimmedScene,
		RecipientRef:   trimmedRef,
		IdempotencyKey: trimmedIdem,
		Channels:       normalized,
		TemplateKey:    trimmedKey,
		Locale:         trimmedLocale,
		Priority:       priority,
		Status:         RequestPending,
		Version:        1,
		ExpiresAt:      expiresAt.UTC(),
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
	}, nil
}

// Expired 判断在给定时刻是否已过期；零值 ExpiresAt 视为永不过期
// （防御历史数据），但构造器总是写入明确时效。
func (n Notification) Expired(now time.Time) bool {
	if n.ExpiresAt.IsZero() {
		return false
	}
	return !now.UTC().Before(n.ExpiresAt)
}

// Validate 校验仓储回读的实体自洽性（防御库中脏数据）。
func (n Notification) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return fmt.Errorf("notification: id is required")
	}
	if strings.TrimSpace(n.TenantID) == "" {
		return ErrTenantIDRequired
	}
	if strings.TrimSpace(n.IdempotencyKey) == "" {
		return ErrIdempotencyKeyRequired
	}
	if !n.Status.Valid() {
		return fmt.Errorf("notification: unsupported status %q", n.Status)
	}
	if n.Version <= 0 {
		return fmt.Errorf("notification: version must be positive, got %d", n.Version)
	}
	if n.Status == RequestPending && len(n.Channels) == 0 {
		return fmt.Errorf("notification: pending request %s has no channels", n.ID)
	}
	return nil
}

// RecomputeStatus 依据逐渠道投递明细推导通知单应有的状态。
//
// 规则（单调不回退）：
//   - 存在渠道进入编排（有明细）→ 至少 dispatched；
//   - 全部渠道终态成功（accepted 及以后）→ sent；
//   - sent 之后若有回执确认 → delivered / undelivered 由回执驱动，
//     本函数不推导（回执路径显式走 Transition）；
//   - 全部渠道永久失败 → failed；
//   - 渠道数与明细数不一致（部分渠道被路由裁剪 / 未建明细）→ dispatched。
//
// 调用方拿推导结果走 Transition 做 CAS 写入；非法回退会被状态机拒绝。
func RecomputeStatus(deliveries []Delivery) RequestStatus {
	if len(deliveries) == 0 {
		return RequestPending
	}
	allTerminalSuccess := true
	allFailed := true
	for _, d := range deliveries {
		switch {
		case d.Status.Sent():
			allFailed = false
		case d.Status == DeliveryFailed:
			allTerminalSuccess = false
		default:
			// pending / retrying：仍在编排，不能收敛到终态。
			return RequestDispatched
		}
	}
	switch {
	case allTerminalSuccess:
		return RequestSent
	case allFailed:
		return RequestFailed
	default:
		return RequestDispatched
	}
}

// Transition 迁移到目标状态并递增版本号。
//
// rejected 必须给出原因（运营要能从记录里直接看出为什么没发）；
// 返回副本，调用方交给仓储做 CAS 写入。
func (n Notification) Transition(target RequestStatus, reason string, now time.Time) (Notification, error) {
	if !target.Valid() {
		return Notification{}, fmt.Errorf("notification: unsupported status %q", target)
	}
	if n.Status.Terminal() {
		return Notification{}, ErrRequestTerminal
	}
	if !n.Status.CanTransitionTo(target) {
		return Notification{}, &InvalidTransitionError{From: n.Status, To: target}
	}
	trimmedReason := strings.TrimSpace(reason)
	if target == RequestRejected && trimmedReason == "" {
		return Notification{}, ErrRejectReasonRequired
	}
	if len([]rune(trimmedReason)) > MaxRejectReasonLength {
		return Notification{}, ErrRejectReasonTooLong
	}
	next := n
	next.Status = target
	next.RejectReason = trimmedReason
	next.Version = n.Version + 1
	next.UpdatedAt = now.UTC()
	return next, nil
}
