package push

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"
)

// Platform 是目标推送平台。
type Platform string

const (
	// PlatformIOS 表示 Apple Push Notification service。
	PlatformIOS Platform = "ios"
	// PlatformAndroid 表示 Firebase Cloud Messaging。
	PlatformAndroid Platform = "android"
	// PlatformWeb 表示 Web Push（由具体驱动决定是否支持）。
	PlatformWeb Platform = "web"
)

// Valid 报告平台是否为已知值。
func (p Platform) Valid() bool {
	switch p {
	case PlatformIOS, PlatformAndroid, PlatformWeb:
		return true
	default:
		return false
	}
}

// Priority 是推送优先级；空值按 normal 处理。
type Priority string

const (
	// PriorityHigh 对应 APNs priority=10 / FCM HIGH。
	PriorityHigh Priority = "high"
	// PriorityNormal 对应 APNs priority=5 / FCM NORMAL。
	PriorityNormal Priority = "normal"
)

const (
	minTokenBytes       = 32
	maxTokenBytes       = 4096
	maxTitleRunes       = 200
	maxBodyRunes        = 1000
	maxDataEntries      = 20
	maxDataKeyBytes     = 64
	maxDataValueBytes   = 1024
	maxPayloadBytes     = 4096
	maxSoundBytes       = 64
	maxCollapseKeyBytes = 64
	maxIdempotencyBytes = 128
	maxReferenceBytes   = 128
	tokenPattern        = `^[A-Za-z0-9_\-:.=]{32,1000}$` //nolint:gosec // 正则模式，非凭据。
	dataKeyPattern      = `^[A-Za-z0-9_.-]+$`
	collapseKeyPattern  = `^[A-Za-z0-9_.~%-]+$`
	maxTTL              = 7 * 24 * time.Hour
)

var (
	tokenRE       = regexp.MustCompile(tokenPattern)
	dataKeyRE     = regexp.MustCompile(dataKeyPattern)
	collapseKeyRE = regexp.MustCompile(collapseKeyPattern)
)

// 平台保留键：业务 Data 不得覆盖 aps/notification 等结构。
var reservedDataKeys = map[string]struct{}{
	"aps": {}, "notification": {}, "data": {}, "token": {}, "android": {}, "apns": {},
}

// Message 是一条已渲染的推送；Token 与平台由调用方提供。
//
// 零值非法。Message 可安全复制，但 Data 为 map，调用方不得在 Send 期间并发修改。
type Message struct {
	// Token 是 APNs 设备 Token 或 FCM 注册 Token。
	Token string
	// Platform 决定载荷结构与可用字段。
	Platform Platform
	// Title 与 Body 至少一个非空。
	Title string
	Body  string
	// Data 是自定义键值；键不得与平台保留字冲突。
	Data map[string]string
	// Badge 是 iOS 角标；nil 表示不修改。
	Badge *int
	// Sound 是提示音；空表示默认或静音（由平台决定）。
	Sound string
	// CollapseKey 用于折叠同组通知；空表示不折叠。
	CollapseKey string
	// TTL 是离线保留时间；0 表示平台默认，最大 7 天。
	TTL time.Duration
	// Priority 为 high / normal；空值按 normal。
	Priority Priority
	// IdempotencyKey 由调用方提供，≤128 字节；APNs 驱动在 UUID 时用作 apns-id。
	IdempotencyKey string
	// Reference 为业务引用（如 order_id），只允许低基数、非 PII。
	Reference string
}

// Validate 校验消息结构；不访问网络。
//
// Token 问题返回 ErrInvalidRecipient，其余结构问题返回 ErrInvalidMessage。
func (m Message) Validate() error {
	if len(m.Token) < minTokenBytes || len(m.Token) > maxTokenBytes || !tokenRE.MatchString(m.Token) {
		return invalidRecipient("token must be 32-4096 url-safe characters")
	}
	if !m.Platform.Valid() {
		return invalidMessage("platform must be ios, android or web")
	}
	if m.Title == "" && m.Body == "" {
		return invalidMessage("title or body is required")
	}
	if utf8.RuneCountInString(m.Title) > maxTitleRunes {
		return invalidMessage("title exceeds %d runes", maxTitleRunes)
	}
	if utf8.RuneCountInString(m.Body) > maxBodyRunes {
		return invalidMessage("body exceeds %d runes", maxBodyRunes)
	}
	if containsControl(m.Title) || containsControl(m.Body) {
		return invalidMessage("title or body contains control characters")
	}
	if err := validateData(m.Data); err != nil {
		return err
	}
	if m.Badge != nil && *m.Badge < 0 {
		return invalidMessage("badge must not be negative")
	}
	if len(m.Sound) > maxSoundBytes || containsControl(m.Sound) {
		return invalidMessage("sound is invalid")
	}
	if m.CollapseKey != "" && (!collapseKeyRE.MatchString(m.CollapseKey) || len(m.CollapseKey) > maxCollapseKeyBytes) {
		return invalidMessage("collapse key is invalid")
	}
	if m.TTL < 0 || m.TTL > maxTTL {
		return invalidMessage("ttl must be in [0,7d]")
	}
	switch m.Priority {
	case "", PriorityHigh, PriorityNormal:
	default:
		return invalidMessage("priority must be high or normal")
	}
	if len(m.IdempotencyKey) > maxIdempotencyBytes || containsControl(m.IdempotencyKey) {
		return invalidMessage("idempotency key is invalid")
	}
	if len(m.Reference) > maxReferenceBytes || containsControl(m.Reference) {
		return invalidMessage("reference is invalid")
	}
	if payloadSize(m) > maxPayloadBytes {
		return invalidMessage("payload exceeds %d bytes", maxPayloadBytes)
	}
	return nil
}

// String 返回脱敏摘要，不包含 Token、标题、正文与 Data。
func (m Message) String() string {
	return fmt.Sprintf("push.Message{platform=%s, data=%d, reference=%q}", m.Platform, len(m.Data), m.Reference)
}

// IsHighPriority 报告是否高优先级。
func (m Message) IsHighPriority() bool { return m.Priority == PriorityHigh }

func validateData(data map[string]string) error {
	if len(data) > maxDataEntries {
		return invalidMessage("data exceeds %d entries", maxDataEntries)
	}
	for key, value := range data {
		if len(key) > maxDataKeyBytes || !dataKeyRE.MatchString(key) {
			return invalidMessage("data key %q is invalid", key)
		}
		if _, reserved := reservedDataKeys[key]; reserved {
			return invalidMessage("data key %q is reserved", key)
		}
		if len(value) > maxDataValueBytes || containsControl(value) {
			return invalidMessage("data value for %q is invalid", key)
		}
	}
	return nil
}

// MarshalPayload 返回 APNs 风格 JSON 载荷（aps + 自定义 Data）。
//
// APNs 驱动直接使用；FCM 驱动可复用其中的 aps 字段。返回值不包含 Token。
func MarshalPayload(msg Message) ([]byte, error) {
	payload := map[string]any{"aps": apsPayload(msg)}
	for key, value := range msg.Data {
		payload[key] = value
	}
	return json.Marshal(payload)
}

// payloadSize 估算 APNs 风格载荷大小，用于触网前拒绝超限消息。
func payloadSize(msg Message) int {
	encoded, err := MarshalPayload(msg)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func apsPayload(msg Message) map[string]any {
	aps := make(map[string]any, 4)
	if msg.Title != "" || msg.Body != "" {
		alert := make(map[string]string, 2)
		if msg.Title != "" {
			alert["title"] = msg.Title
		}
		if msg.Body != "" {
			alert["body"] = msg.Body
		}
		aps["alert"] = alert
	}
	if msg.Badge != nil {
		aps["badge"] = *msg.Badge
	}
	if msg.Sound != "" {
		aps["sound"] = msg.Sound
	}
	return aps
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func cloneData(data map[string]string) map[string]string {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]string, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}
