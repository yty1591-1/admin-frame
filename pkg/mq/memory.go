package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryConfig 进程内 Broker 配置（测试 / 本地脚手架）。
type MemoryConfig struct {
	// Buffer 每主题投递缓冲；≤0 表示 64。
	Buffer int
}

// Memory 是进程内消息总线，同时提供 Producer 与 Consumer。
//
// 语义：至少一次；Subscribe 扇出（每条消息送给该主题全部 handler）；
// 无订阅者时 Publish 仍成功（消息丢弃，便于与 Outbox 联调前占位）。
type Memory struct {
	buffer int
	mu     sync.RWMutex
	subs   map[string][]Handler
	inbox  chan queued
	closed atomic.Bool
	once   sync.Once
}

type queued struct {
	msg Message
}

// NewMemory 创建内存 Broker。
func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.Buffer <= 0 {
		cfg.Buffer = 64
	}
	m := &Memory{
		buffer: cfg.Buffer,
		subs:   make(map[string][]Handler),
		inbox:  make(chan queued, cfg.Buffer),
	}
	return m
}

// Producer 返回发布端（同一实例）。
func (m *Memory) Producer() Producer { return (*memoryProducer)(m) }

// Consumer 返回消费端（同一实例）。
func (m *Memory) Consumer() Consumer { return (*memoryConsumer)(m) }

type memoryProducer Memory

// Publish 将消息投递到内存队列；ctx 取消时返回 ctx.Err()。
func (p *memoryProducer) Publish(ctx context.Context, msg Message) error {
	m := (*Memory)(p)
	if m.closed.Load() {
		return ErrClosed
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	msg = preparePublish(msg)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.inbox <- queued{msg: msg}:
		return nil
	}
}

// Close 关闭底层 broker。
func (p *memoryProducer) Close() error {
	return (*Memory)(p).Close()
}

type memoryConsumer Memory

// Subscribe 注册主题 handler；同一主题可注册多个（扇出）。
func (c *memoryConsumer) Subscribe(topic string, h Handler) error {
	m := (*Memory)(c)
	if m.closed.Load() {
		return ErrClosed
	}
	if topic == "" || h == nil {
		return fmt.Errorf("%w: topic and handler required", ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[topic] = append(m.subs[topic], h)
	return nil
}

// Run 循环消费直到 ctx 取消或 broker 关闭。
func (c *memoryConsumer) Run(ctx context.Context) error {
	m := (*Memory)(c)
	if m.closed.Load() {
		return ErrClosed
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case q, ok := <-m.inbox:
			if !ok {
				return ErrClosed
			}
			m.dispatch(ctx, q.msg)
		}
	}
}

// Close 关闭底层 broker。
func (c *memoryConsumer) Close() error {
	return (*Memory)(c).Close()
}

// Close 关闭 inbox 并拒绝后续 Publish/Subscribe。
func (m *Memory) Close() error {
	m.once.Do(func() {
		m.closed.Store(true)
		close(m.inbox)
	})
	return nil
}

func (m *Memory) dispatch(ctx context.Context, msg Message) {
	handlers := m.handlersFor(msg.Topic)
	for _, h := range handlers {
		m.deliver(ctx, h, msg.Clone())
	}
}

func (m *Memory) handlersFor(topic string) []Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.subs[topic]
	if len(src) == 0 {
		return nil
	}
	out := make([]Handler, len(src))
	copy(out, src)
	return out
}

func (m *Memory) deliver(ctx context.Context, h Handler, msg Message) {
	if msg.Attempt < 1 {
		msg.Attempt = 1
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		err := h(ctx, msg)
		if err == nil || !isRetrySignal(err) {
			return
		}
		msg.Attempt++
		msg.Headers = HeaderSet(msg.Headers, HeaderAttempt, fmt.Sprintf("%d", msg.Attempt))
	}
}

func preparePublish(msg Message) Message {
	out := msg.Clone()
	if out.ID == "" {
		out.ID = newMessageID()
	}
	if out.Timestamp.IsZero() {
		out.Timestamp = time.Now().UTC()
	}
	if out.Attempt < 1 {
		out.Attempt = 1
	}
	out.Headers = HeaderSet(out.Headers, HeaderMessageID, out.ID)
	out.Headers = HeaderSet(out.Headers, HeaderAttempt, fmt.Sprintf("%d", out.Attempt))
	return out
}

func newMessageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
