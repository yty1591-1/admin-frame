package email

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const defaultMaxMemoryMessages = 200

// MemoryConfig 配置内存邮件驱动；用于本地联调与单测。
type MemoryConfig struct {
	// MaxMessages 是保留的最近邮件条数；0 使用 200。
	MaxMessages int
	// From 是默认发件人；Message.From 为空时使用。
	From Address
	// Limits 是校验上限；零值使用 DefaultLimits。
	Limits Limits
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// Observer 接收发送事件；可为 nil。
	Observer Observer
}

// Memory 是进程内邮件驱动：只记录消息、不出网；实现 Sender。
//
// 含互斥锁，不得在首次使用后复制。消息按发送顺序保留最近 MaxMessages 条。
type Memory struct {
	mu       sync.Mutex
	messages []Message
	max      int
	from     Address
	limits   Limits
	clock    clock.Clock
	observer Observer
	closed   atomic.Bool
}

func init() {
	Register("memory", openMemory)
}

func openMemory(_ context.Context, cfg Config) (Sender, error) {
	return NewMemory(MemoryConfig{
		From:     cfg.From,
		Limits:   cfg.Limits(),
		Clock:    cfg.Clock,
		Observer: cfg.Observer,
	}), nil
}

// NewMemory 构造内存邮件驱动；零值配置可用。
func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.Clock == nil {
		cfg.Clock = clock.RealClock()
	}
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = defaultMaxMemoryMessages
	}
	return &Memory{
		max:      cfg.MaxMessages,
		from:     cfg.From,
		limits:   cfg.Limits.normalized(),
		clock:    cfg.Clock,
		observer: cfg.Observer,
	}
}

// Provider 返回驱动名 "memory"。
func (m *Memory) Provider() string { return "memory" }

// Send 记录一封邮件；校验失败返回 ErrInvalidMessage / ErrInvalidRecipient / ErrRejected。
func (m *Memory) Send(ctx context.Context, msg Message) (Receipt, error) {
	start := m.clock.Now()
	if err := m.checkReady(ctx); err != nil {
		m.emit(ctx, start, err)
		return Receipt{}, err
	}
	if msg.From.Email == "" {
		msg.From = m.from
	}
	if err := msg.ValidateWith(m.limits); err != nil {
		m.emit(ctx, start, err)
		return Receipt{}, err
	}
	messageID, err := newMessageID(msg.From.Email)
	if err != nil {
		wrapped := NewProviderError(m.Provider(), "message_id", 0, fmt.Errorf("%w: %w", ErrInvalidMessage, err))
		m.emit(ctx, start, wrapped)
		return Receipt{}, wrapped
	}
	m.store(msg)
	receipt := Receipt{Provider: m.Provider(), MessageID: messageID, AcceptedAt: m.clock.Now()}
	m.emit(ctx, start, nil)
	return receipt, nil
}

// Messages 返回已记录邮件的深拷贝，按发送顺序排列。
func (m *Memory) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.messages))
	for i, msg := range m.messages {
		out[i] = cloneMessage(msg)
	}
	return out
}

// Len 返回当前保留的邮件条数。
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// Reset 清空已记录邮件，不影响关闭状态。
func (m *Memory) Reset() {
	m.mu.Lock()
	m.messages = nil
	m.mu.Unlock()
}

// Close 关闭驱动；幂等，之后 Send 返回包装 ErrClosed 的错误。
func (m *Memory) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *Memory) checkReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MapContextError(m.Provider(), err)
	}
	if m.closed.Load() {
		return fmt.Errorf("%w: %s", ErrClosed, m.Provider())
	}
	return nil
}

func (m *Memory) store(msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.max > 0 && len(m.messages) >= m.max {
		copy(m.messages, m.messages[1:])
		m.messages = m.messages[:len(m.messages)-1]
	}
	m.messages = append(m.messages, cloneMessage(msg))
}

func (m *Memory) emit(ctx context.Context, start time.Time, err error) {
	if m.observer == nil {
		return
	}
	ev := Event{
		Provider:  m.Provider(),
		Operation: "send",
		Result:    "success",
		Duration:  m.clock.Now().Sub(start),
	}
	if err != nil {
		ev.Result = "failure"
		ev.Code = CodeOf(err)
		ev.RetryAfter = RetryAfterOf(err)
	}
	observe(ctx, m.observer, ev)
}

func cloneMessage(msg Message) Message {
	msg.To = append([]Address(nil), msg.To...)
	msg.Cc = append([]Address(nil), msg.Cc...)
	msg.Bcc = append([]Address(nil), msg.Bcc...)
	if msg.ReplyTo != nil {
		replyTo := *msg.ReplyTo
		msg.ReplyTo = &replyTo
	}
	if msg.Headers != nil {
		headers := make(map[string]string, len(msg.Headers))
		for key, value := range msg.Headers {
			headers[key] = value
		}
		msg.Headers = headers
	}
	attachments := make([]Attachment, len(msg.Attachments))
	for i, attachment := range msg.Attachments {
		attachment.Data = append([]byte(nil), attachment.Data...)
		attachments[i] = attachment
	}
	msg.Attachments = attachments
	return msg
}
