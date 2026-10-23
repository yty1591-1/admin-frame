package outbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/pkg/retry"
)

// ErrUnknownTopic 表示事件主题没有注册 handler；Relay 会将其视为永久失败并写入死信。
var ErrUnknownTopic = errors.New("outbox: no handler registered for topic")

// Router 是按 topic 分发事件的 handler 注册表。
//
// 业务域只通过 Register 声明「主题 + handler」，平台核心不做业务分支；
// 未知主题直接返回 retry.Permanent，避免毒消息无限重试。
type Router struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRouter 创建空路由表。
func NewRouter() *Router {
	return &Router{handlers: make(map[string]Handler)}
}

// Register 注册 topic 对应的 handler；topic 为空、handler 为 nil 或重复注册返回错误。
func (r *Router) Register(topic string, handler Handler) error {
	if r == nil {
		return fmt.Errorf("%w: router is nil", ErrInvalidEvent)
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return fmt.Errorf("%w: topic is required", ErrInvalidEvent)
	}
	if handler == nil {
		return fmt.Errorf("%w: handler for topic %q is required", ErrInvalidEvent, topic)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = make(map[string]Handler)
	}
	if _, exists := r.handlers[topic]; exists {
		return fmt.Errorf("%w: handler for topic %q already registered", ErrInvalidEvent, topic)
	}
	r.handlers[topic] = handler
	return nil
}

// Has 报告 topic 是否已注册 handler。
func (r *Router) Has(topic string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[strings.TrimSpace(topic)]
	return ok
}

// Topics 返回已注册主题（按字典序，便于断言与排查）。
func (r *Router) Topics() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	topics := make([]string, 0, len(r.handlers))
	for topic := range r.handlers {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}

// Dispatch 按事件主题查找 handler；未知主题返回带 retry.Permanent 的错误。
//
// 主题为空时回退 EventType，与入盒时的 NormalizeTopic 语义一致。
func (r *Router) Dispatch(ctx context.Context, event *Event) error {
	if event == nil {
		return retry.Permanent(fmt.Errorf("%w: nil event", ErrInvalidEvent))
	}
	topic := NormalizeTopic(event.Topic, event.EventType)
	if r == nil {
		return retry.Permanent(fmt.Errorf("%w: %q", ErrUnknownTopic, topic))
	}
	r.mu.RLock()
	handler := r.handlers[topic]
	r.mu.RUnlock()
	if handler == nil {
		return retry.Permanent(fmt.Errorf("%w: %q", ErrUnknownTopic, topic))
	}
	return handler(ctx, event)
}

// Handler 返回可直接装配到 RelayOptions.Handler 的分发函数。
func (r *Router) Handler() Handler {
	return r.Dispatch
}
