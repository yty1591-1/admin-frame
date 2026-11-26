package cache

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// 本地快照缓存默认参数（IDN-GAP-P1-02）。
const (
	// DefaultRevocationLocalCacheTTL 本地快照 TTL；撤销可见延迟上限即为该值。
	DefaultRevocationLocalCacheTTL = time.Second
	// DefaultRevocationLocalCacheCapacity 本地缓存单类条目的容量上限；超出按 LRU 淘汰。
	DefaultRevocationLocalCacheCapacity = 4096
)

// cachedRevocationStore 是 RevocationStore 的多级缓存装饰器：
// 账号 / 会话快照先读进程内 L1，未命中再读 Redis（inner），仍由检查器回源数据库。
//
// 设计约束：
//   - jti 黑名单永不缓存：缓存「未撤销」会让登出后的令牌在 TTL 内继续可用；
//   - 写操作先写 inner 再更新 / 删除 L1，保证本副本撤销立即生效；
//   - L1 TTL 独立于 state_ttl，默认 1s，跨副本仍由 Redis + state_ttl 收敛。
type cachedRevocationStore struct {
	inner    app.RevocationStore
	ttl      time.Duration
	capacity int
	observer app.RevocationCacheObserver
	l1       *revocationL1
}

// 编译期断言：装饰器必须保持 RevocationStore 契约。
var (
	_ app.RevocationStore  = (*cachedRevocationStore)(nil)
	_ app.ClientStateStore = (*cachedRevocationStore)(nil)
)

// NewCachedRevocationStore 构造 L1 装饰器；ttl <= 0 或 capacity <= 0 时直接返回 inner（关闭缓存）。
func NewCachedRevocationStore(
	inner app.RevocationStore,
	ttl time.Duration,
	capacity int,
	observer app.RevocationCacheObserver,
) app.RevocationStore {
	if inner == nil {
		return nil
	}
	if ttl <= 0 || capacity <= 0 {
		return inner
	}
	return &cachedRevocationStore{
		inner:    inner,
		ttl:      ttl,
		capacity: capacity,
		observer: observer,
		l1:       newRevocationL1(ttl, capacity),
	}
}

// LoadAccountState 先读 L1；未命中 / 过期时读 Redis 并回填 L1。
func (s *cachedRevocationStore) LoadAccountState(ctx context.Context, accountID string) (app.AccountState, bool, error) {
	if state, ok := s.l1.get(accountKind, accountID); ok {
		s.observeHit(ctx, accountKind)
		return state.(app.AccountState), true, nil
	}
	s.observeRedisCall(ctx, "account_load")
	state, found, err := s.inner.LoadAccountState(ctx, accountID)
	if err != nil || !found {
		return state, found, err
	}
	// 只缓存成功命中的快照；读取失败不做负缓存，避免故障期间把「未知」当作事实。
	s.l1.put(accountKind, accountID, state)
	return state, true, nil
}

// SaveAccountState 写入 Redis 后同步更新 L1；写入失败时不更新 L1（由调用方回退 DEL）。
func (s *cachedRevocationStore) SaveAccountState(ctx context.Context, accountID string, state app.AccountState, ttl time.Duration) error {
	s.observeRedisCall(ctx, "account_set")
	if err := s.inner.SaveAccountState(ctx, accountID, state, ttl); err != nil {
		return err
	}
	s.l1.put(accountKind, accountID, state)
	return nil
}

// DeleteAccountState 删除 Redis 与 L1；即使 Redis 删除失败也必须清空本地，避免本副本继续放行。
func (s *cachedRevocationStore) DeleteAccountState(ctx context.Context, accountID string) error {
	s.l1.delete(accountKind, accountID)
	s.observeRedisCall(ctx, "account_delete")
	return s.inner.DeleteAccountState(ctx, accountID)
}

// LoadSessionState 先读 L1；未命中 / 过期时读 Redis 并回填 L1。
func (s *cachedRevocationStore) LoadSessionState(ctx context.Context, sessionID string) (app.SessionState, bool, error) {
	if state, ok := s.l1.get(sessionKind, sessionID); ok {
		s.observeHit(ctx, sessionKind)
		return state.(app.SessionState), true, nil
	}
	s.observeRedisCall(ctx, "session_load")
	state, found, err := s.inner.LoadSessionState(ctx, sessionID)
	if err != nil || !found {
		return state, found, err
	}
	s.l1.put(sessionKind, sessionID, state)
	return state, true, nil
}

// SaveSessionState 写入 Redis 后同步更新 L1。
func (s *cachedRevocationStore) SaveSessionState(ctx context.Context, sessionID string, state app.SessionState, ttl time.Duration) error {
	s.observeRedisCall(ctx, "session_set")
	if err := s.inner.SaveSessionState(ctx, sessionID, state, ttl); err != nil {
		return err
	}
	s.l1.put(sessionKind, sessionID, state)
	return nil
}

// DeleteSessionState 删除 Redis 与 L1。
func (s *cachedRevocationStore) DeleteSessionState(ctx context.Context, sessionID string) error {
	s.l1.delete(sessionKind, sessionID)
	s.observeRedisCall(ctx, "session_delete")
	return s.inner.DeleteSessionState(ctx, sessionID)
}

// LoadClientState 先读 L1；未命中 / 过期时读 inner 并回填 L1。
//
// inner 未实现 ClientStateStore 时退化为「不缓存」，由检查器直接回源数据库。
func (s *cachedRevocationStore) LoadClientState(ctx context.Context, clientID string) (app.ClientState, bool, error) {
	inner, ok := s.inner.(app.ClientStateStore)
	if !ok {
		return app.ClientState{}, false, nil
	}
	if state, hit := s.l1.get(clientKind, clientID); hit {
		s.observeHit(ctx, clientKind)
		return state.(app.ClientState), true, nil
	}
	s.observeRedisCall(ctx, "client_load")
	state, found, err := inner.LoadClientState(ctx, clientID)
	if err != nil || !found {
		return state, found, err
	}
	s.l1.put(clientKind, clientID, state)
	return state, true, nil
}

// SaveClientState 写入 inner 后同步更新 L1。
func (s *cachedRevocationStore) SaveClientState(ctx context.Context, clientID string, state app.ClientState, ttl time.Duration) error {
	inner, ok := s.inner.(app.ClientStateStore)
	if !ok {
		return nil
	}
	s.observeRedisCall(ctx, "client_set")
	if err := inner.SaveClientState(ctx, clientID, state, ttl); err != nil {
		return err
	}
	s.l1.put(clientKind, clientID, state)
	return nil
}

// DeleteClientState 删除 inner 与 L1；本地必须清空，避免本副本继续放行。
func (s *cachedRevocationStore) DeleteClientState(ctx context.Context, clientID string) error {
	s.l1.delete(clientKind, clientID)
	inner, ok := s.inner.(app.ClientStateStore)
	if !ok {
		return nil
	}
	s.observeRedisCall(ctx, "client_delete")
	return inner.DeleteClientState(ctx, clientID)
}

// RevokeToken 写入 jti 黑名单；jti 永不经过 L1。
func (s *cachedRevocationStore) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	s.observeRedisCall(ctx, "token_revoke")
	return s.inner.RevokeToken(ctx, jti, ttl)
}

// IsTokenRevoked 直接读 Redis；禁止本地缓存「未撤销」结果。
func (s *cachedRevocationStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	s.observeRedisCall(ctx, "token_load")
	return s.inner.IsTokenRevoked(ctx, jti)
}

// observeHit 上报一次 L1 命中；kind=account|session。
func (s *cachedRevocationStore) observeHit(ctx context.Context, kind string) {
	if s.observer == nil {
		return
	}
	s.observer.OnRevocationCacheHit(ctx, kind)
}

// observeRedisCall 上报一次 inner（Redis）调用。
func (s *cachedRevocationStore) observeRedisCall(ctx context.Context, op string) {
	if s.observer == nil {
		return
	}
	s.observer.OnRevocationRedisCall(ctx, op)
}

// 缓存条目类型；用于同一 LRU 链表中区分账号 / 会话 / 客户端。
const (
	accountKind = "account"
	sessionKind = "session"
	clientKind  = "client"
)

// revocationL1 是并发安全的 LRU + TTL 本地缓存。
//
// 使用 container/list 维护最近使用顺序：命中时移到队首，写入超容量时淘汰队尾。
type revocationL1 struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	clock    func() time.Time
	items    map[string]*list.Element
	order    *list.List
}

type l1Entry struct {
	key       string
	value     any
	expiresAt time.Time
}

// newRevocationL1 创建本地缓存；clock 便于单测注入时间。
func newRevocationL1(ttl time.Duration, capacity int) *revocationL1 {
	return &revocationL1{
		ttl:      ttl,
		capacity: capacity,
		clock:    time.Now,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// get 读取条目；过期条目在命中时惰性删除。
func (c *revocationL1) get(kind, id string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := kind + ":" + id
	element, ok := c.items[key]
	if !ok {
		return nil, false
	}
	entry := element.Value.(*l1Entry)
	if !c.clock().Before(entry.expiresAt) {
		c.removeElement(element)
		return nil, false
	}
	c.order.MoveToFront(element)
	return entry.value, true
}

// put 写入条目并刷新过期时间；超容量时淘汰最久未使用条目。
func (c *revocationL1) put(kind, id string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := kind + ":" + id
	if element, ok := c.items[key]; ok {
		entry := element.Value.(*l1Entry)
		entry.value = value
		entry.expiresAt = c.clock().Add(c.ttl)
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(&l1Entry{
		key:       key,
		value:     value,
		expiresAt: c.clock().Add(c.ttl),
	})
	c.items[key] = element
	for len(c.items) > c.capacity {
		c.removeElement(c.order.Back())
	}
}

// delete 删除条目；不存在视为成功。
func (c *revocationL1) delete(kind, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := kind + ":" + id
	if element, ok := c.items[key]; ok {
		c.removeElement(element)
	}
}

// removeElement 从链表与索引中移除；调用方须持有写锁。
func (c *revocationL1) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*l1Entry)
	c.order.Remove(element)
	delete(c.items, entry.key)
}
