package cache

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryConfig 本地 LRU+TTL 缓存配置。
type MemoryConfig struct {
	// MaxEntries 最大条目数；≤0 表示 1024。超出时淘汰最久未用。
	MaxEntries int
	// Clock 可注入时钟。
	Clock Clock
}

// Memory 进程内 LRU + TTL 缓存；并发安全。
type Memory struct {
	mu         sync.Mutex
	maxEntries int
	clock      Clock
	ll         *list.List
	items      map[string]*list.Element
	closed     bool
}

type memEntry struct {
	key       string
	value     []byte
	expiresAt time.Time // zero = 不过期（本包 Set 仍强制 ttl>0，过期时间必有）
	negative  bool
}

// NewMemory 创建本地缓存。
func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 1024
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock()
	}
	return &Memory{
		maxEntries: cfg.MaxEntries,
		clock:      cfg.Clock,
		ll:         list.New(),
		items:      make(map[string]*list.Element),
	}
}

// Get 实现 Store。
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("%w: empty key", ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	el, ok := m.items[key]
	if !ok {
		return nil, ErrMiss
	}
	ent := el.Value.(*memEntry)
	if !ent.expiresAt.IsZero() && !m.clock.Now().Before(ent.expiresAt) {
		m.removeElement(el)
		return nil, ErrMiss
	}
	m.ll.MoveToFront(el)
	if ent.negative {
		return nil, ErrNegative
	}
	out := make([]byte, len(ent.value))
	copy(out, ent.value)
	return out, nil
}

// Set 实现 Store；ttl 必须 >0。
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	return m.set(key, value, ttl, false)
}

// SetNegative 写入空值占位（防穿透）；Get 返回 ErrNegative。
func (m *Memory) SetNegative(_ context.Context, key string, ttl time.Duration) error {
	return m.set(key, nil, ttl, true)
}

func (m *Memory) set(key string, value []byte, ttl time.Duration, negative bool) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: ttl must be > 0", ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	exp := m.clock.Now().Add(ttl)
	var valCopy []byte
	if !negative && value != nil {
		valCopy = make([]byte, len(value))
		copy(valCopy, value)
	}
	if el, ok := m.items[key]; ok {
		ent := el.Value.(*memEntry)
		ent.value = valCopy
		ent.expiresAt = exp
		ent.negative = negative
		m.ll.MoveToFront(el)
		return nil
	}
	ent := &memEntry{key: key, value: valCopy, expiresAt: exp, negative: negative}
	el := m.ll.PushFront(ent)
	m.items[key] = el
	for m.ll.Len() > m.maxEntries {
		m.removeElement(m.ll.Back())
	}
	return nil
}

// Delete 实现 Store。
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	for _, k := range keys {
		if el, ok := m.items[k]; ok {
			m.removeElement(el)
		}
	}
	return nil
}

// Len 返回当前条目数（含未惰性清理的过期项前的近似值；先扫头尾过期）。
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0
	}
	now := m.clock.Now()
	for m.ll.Len() > 0 {
		back := m.ll.Back()
		ent := back.Value.(*memEntry)
		if ent.expiresAt.IsZero() || now.Before(ent.expiresAt) {
			break
		}
		m.removeElement(back)
	}
	return m.ll.Len()
}

// Close 清空并拒绝后续操作。
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.ll.Init()
	m.items = make(map[string]*list.Element)
	return nil
}

func (m *Memory) removeElement(el *list.Element) {
	if el == nil {
		return
	}
	ent := el.Value.(*memEntry)
	delete(m.items, ent.key)
	m.ll.Remove(el)
}

// 编译期断言。
var _ Store = (*Memory)(nil)
