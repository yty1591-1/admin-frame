package sms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const defaultMaxMemoryMessages = 1000

// MemoryConfig 配置内存短信驱动；用于本地联调与单测。
type MemoryConfig struct {
	// MaxMessages 是保留的最近消息条数；0 使用 1000，负值表示不限制（不推荐）。
	MaxMessages int
	// AllowRawContent 是否允许裸内容；默认 false。
	AllowRawContent bool
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// Observer 接收发送事件；可为 nil。
	Observer Observer
}

// Memory 是进程内短信驱动：只记录消息、不出网；实现 Sender 与 BatchSender。
//
// 含互斥锁，不得在首次使用后复制。消息按发送顺序保留最近 MaxMessages 条。
type Memory struct {
	mu       sync.Mutex
	messages []Message
	max      int
	allowRaw bool
	clock    clock.Clock
	observer Observer
	closed   atomic.Bool
}

func init() {
	Register("memory", openMemory)
}

func openMemory(_ context.Context, cfg Config) (Sender, error) {
	return NewMemory(MemoryConfig{
		AllowRawContent: cfg.AllowRawContent,
		Clock:           cfg.Clock,
		Observer:        cfg.Observer,
	}), nil
}

// NewMemory 构造内存短信驱动；零值配置可用。
func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.Clock == nil {
		cfg.Clock = clock.RealClock()
	}
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = defaultMaxMemoryMessages
	}
	return &Memory{
		max:      cfg.MaxMessages,
		allowRaw: cfg.AllowRawContent,
		clock:    cfg.Clock,
		observer: cfg.Observer,
	}
}

// Provider 返回驱动名 "memory"。
func (m *Memory) Provider() string { return "memory" }

// Send 记录一条短信；校验失败返回 ErrInvalidMessage / ErrInvalidRecipient。
func (m *Memory) Send(ctx context.Context, msg Message) (Receipt, error) {
	start := m.clock.Now()
	if err := m.checkReady(ctx); err != nil {
		m.emit(ctx, msg, start, err)
		return Receipt{}, err
	}
	if err := msg.Validate(); err != nil {
		m.emit(ctx, msg, start, err)
		return Receipt{}, err
	}
	if msg.Content != "" && !m.allowRaw {
		err := rejected("memory: raw content is disabled")
		m.emit(ctx, msg, start, err)
		return Receipt{}, err
	}
	m.store(msg)
	receipt := Receipt{Provider: m.Provider(), MessageID: newMemoryID(), AcceptedAt: m.clock.Now()}
	m.emit(ctx, msg, start, nil)
	return receipt, nil
}

// SendBatch 逐条记录；返回与输入等长的结果。
func (m *Memory) SendBatch(ctx context.Context, msgs []Message) (BatchResult, error) {
	result := BatchResult{Receipts: make([]Receipt, len(msgs)), Errors: make([]error, len(msgs))}
	for i, msg := range msgs {
		receipt, err := m.Send(ctx, msg)
		if err != nil {
			result.Errors[i] = err
			continue
		}
		result.Receipts[i] = receipt
	}
	return result, nil
}

// Messages 返回已记录消息的深拷贝，按发送顺序排列。
func (m *Memory) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.messages))
	for i, msg := range m.messages {
		out[i] = cloneMessage(msg)
	}
	return out
}

// Len 返回当前保留的消息条数。
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// Reset 清空已记录消息，不影响关闭状态。
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

func (m *Memory) emit(ctx context.Context, msg Message, start time.Time, err error) {
	if m.observer == nil {
		return
	}
	ev := Event{
		Provider:  m.Provider(),
		Operation: "send",
		Template:  msg.TemplateID,
		Result:    "success",
		Duration:  m.clock.Now().Sub(start),
	}
	if err != nil {
		ev.Result = "failure"
		ev.Code = codeOf(err)
		ev.RetryAfter = retryAfterOf(err)
	}
	observe(ctx, m.observer, ev)
}

func cloneMessage(msg Message) Message {
	msg.Params = cloneParams(msg.Params)
	return msg
}

func newMemoryID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "mem-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}
