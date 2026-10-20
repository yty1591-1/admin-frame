package push

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

// MemoryConfig 配置内存推送驱动；用于本地联调与单测。
type MemoryConfig struct {
	// MaxMessages 是保留的最近消息条数；0 使用 1000。
	MaxMessages int
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// Observer 接收发送事件；可为 nil。
	Observer Observer
}

// Memory 是进程内推送驱动：只记录消息、不出网；实现 Sender。
//
// 含互斥锁，不得在首次使用后复制。消息按发送顺序保留最近 MaxMessages 条。
type Memory struct {
	mu       sync.Mutex
	messages []Message
	max      int
	clock    clock.Clock
	observer Observer
	closed   atomic.Bool
}

func init() {
	Register("memory", openMemory)
}

func openMemory(_ context.Context, cfg Config) (Sender, error) {
	return NewMemory(MemoryConfig{Clock: cfg.Clock, Observer: cfg.Observer}), nil
}

// NewMemory 构造内存推送驱动；零值配置可用。
func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.Clock == nil {
		cfg.Clock = clock.RealClock()
	}
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = defaultMaxMemoryMessages
	}
	return &Memory{
		max:      cfg.MaxMessages,
		clock:    cfg.Clock,
		observer: cfg.Observer,
	}
}

// Provider 返回驱动名 "memory"。
func (m *Memory) Provider() string { return "memory" }

// Send 记录一条推送；校验失败返回 ErrInvalidMessage / ErrInvalidRecipient。
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
	m.store(msg)
	receipt := Receipt{Provider: m.Provider(), MessageID: newMemoryID(), AcceptedAt: m.clock.Now()}
	m.emit(ctx, msg, start, nil)
	return receipt, nil
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
		Platform:  string(msg.Platform),
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
	msg.Data = cloneData(msg.Data)
	if msg.Badge != nil {
		badge := *msg.Badge
		msg.Badge = &badge
	}
	return msg
}

func newMemoryID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "mem-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}
