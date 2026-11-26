package audit

import (
	"fmt"
	"strings"
	"time"
)

// Outcome 操作结果。
type Outcome string

// Outcome 取值。
const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Event 单条审计事件（AUD-04 必填字段）。
type Event struct {
	EventID      string            `json:"event_id"`
	Action       string            `json:"action"`
	ActorID      string            `json:"actor_id"`
	ActorType    string            `json:"actor_type,omitempty"`
	Channel      string            `json:"channel"`
	MerchantID   string            `json:"merchant_id,omitempty"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Outcome      Outcome           `json:"outcome"`
	OccurredAt   time.Time         `json:"occurred_at"`
	TraceID      string            `json:"trace_id"`
	Service      string            `json:"service"`
	Reason       string            `json:"reason,omitempty"`
	Attrs        map[string]string `json:"attrs,omitempty"`
}

// Validate 校验必填字段与枚举（AUD-04）。
func (e Event) Validate() error {
	if strings.TrimSpace(e.EventID) == "" {
		return fmt.Errorf("event_id is required")
	}
	if strings.TrimSpace(e.Action) == "" {
		return fmt.Errorf("action is required")
	}
	if strings.TrimSpace(e.ActorID) == "" {
		return fmt.Errorf("actor_id is required")
	}
	if strings.TrimSpace(e.Channel) == "" {
		return fmt.Errorf("channel is required")
	}
	switch e.Channel {
	case "admin", "biz", "c", "worker":
	default:
		return fmt.Errorf("unsupported channel: %s", e.Channel)
	}
	if strings.TrimSpace(e.ResourceType) == "" {
		return fmt.Errorf("resource_type is required")
	}
	if strings.TrimSpace(e.ResourceID) == "" {
		return fmt.Errorf("resource_id is required")
	}
	switch e.Outcome {
	case OutcomeSuccess, OutcomeFailure, OutcomeDenied:
	default:
		return fmt.Errorf("unsupported outcome: %s", e.Outcome)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if strings.TrimSpace(e.TraceID) == "" {
		return fmt.Errorf("trace_id is required")
	}
	if strings.TrimSpace(e.Service) == "" {
		return fmt.Errorf("service is required")
	}
	if err := rejectSensitive(e.Reason); err != nil {
		return err
	}
	for k, v := range e.Attrs {
		if err := rejectSensitive(k + "=" + v); err != nil {
			return fmt.Errorf("attrs: %w", err)
		}
	}
	return nil
}

func rejectSensitive(s string) error {
	lower := strings.ToLower(s)
	needles := []string{
		"bearer ",
		"password=",
		"passwd=",
		"secret=",
		"api_key=",
		// 必须带 '='：配置键名 auth.jwt.private_key 会出现在 attrs，
		// 不能把键名本身当成密钥明文（bootstrap CreateSecret 会误杀）。
		"private_key=",
		"-----begin",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return fmt.Errorf("audit payload must not contain secrets (AUD-08)")
		}
	}
	return nil
}
