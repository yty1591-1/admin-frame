package configclient

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultRefreshInterval 是版本轮询默认间隔（CC-F-13 / 规划 §6.4）。
const DefaultRefreshInterval = 5 * time.Second

// Source 提供某 env + namespace 的当前生效快照（由 MySQL / 内存实现提供）。
type Source interface {
	Load(ctx context.Context, env, namespace string) (Snapshot, error)
}

// ClientConfig 是 Client 装配参数。
type ClientConfig struct {
	// Source 快照来源；必填。
	Source Source
	// Verifier 验签器；必填，未签名的快照一律拒绝装载。
	Verifier *Verifier
	// Env 环境；必填。
	Env string
	// Namespace 单命名空间；Namespaces 为空时使用。
	Namespace string
	// Namespaces 是按开关启用的命名空间列表；非空时优先于 Namespace，
	// 客户端逐个拉取、验签并合并取值（按 namespace 灰度接管）。
	Namespaces []string
	// PrimaryNamespace 是本地落盘与兼容读取使用的主 namespace；
	// 为空时取 Namespaces 首项。多 namespace 时建议显式指定 platform。
	PrimaryNamespace string
	// Store 本地快照仓库；可选。配置后刷新成功即原子落盘，中心不可用时
	// 冷启动回退到本地已验签快照。多 namespace 模式下只保存主 namespace。
	Store SnapshotStore
	// Now 时钟；测试注入。
	Now func() time.Time
	// OnRefresh 在每次刷新尝试后按 namespace 回调；用于运行期日志与指标，可为 nil。
	//
	// 回调在刷新 goroutine 内同步执行，必须快速返回，且不得调用 Refresh / Run。
	// 事件中的 Snapshot 为只读副本，调用方不得修改。
	OnRefresh func(RefreshEvent)
	// OnWatchDropped 在 Watch 订阅者积压、事件被丢弃时回调；可为 nil。
	OnWatchDropped func()
}

// RefreshEvent 描述一次刷新尝试的结果。
//
// 多 namespace 模式下每个 namespace 触发一次事件；Err 非 nil 表示该
// namespace 本次拉取或验签失败，此时 Loaded 为 true 说明仍在使用该
// namespace 的上一份已验签快照（CFG-12）。
type RefreshEvent struct {
	// At 是刷新完成时间。
	At time.Time
	// Namespace 是本次事件的 namespace。
	Namespace string
	// Snapshot 是当前可读快照；无可用快照时为零值，Loaded=false。
	Snapshot Snapshot
	// Loaded 表示当前是否存在可读快照。
	Loaded bool
	// Degraded 表示当前取值依赖本地快照（中心不可用）。
	Degraded bool
	// Err 是本次刷新失败原因；nil 表示成功。
	Err error
}

// Client 是进程内配置读取客户端：拉取 → 验签 → 原子替换 → 类型化读取。
//
// 多 namespace 模式下按 namespace 独立拉取与降级，取值合并到同一张表；
// 任一 namespace 失败不会清空其它 namespace 的已验签快照。
type Client struct {
	source     Source
	verifier   *Verifier
	env        string
	namespaces []string
	primary    string
	store      SnapshotStore
	now        func() time.Time
	onRefresh  func(RefreshEvent)

	current  atomic.Pointer[loaded]
	degraded atomic.Bool
	mu       sync.Mutex
	lastErr  error

	watchMu        sync.Mutex
	watchers       map[chan RefreshEvent]struct{}
	onWatchDropped func()
}

// loaded 是一次成功刷新的不可变结果；写入后不再修改。
type loaded struct {
	snapshots map[string]Snapshot
	values    map[string]string
	items     []Item
	// primary 是首个 namespace 的快照元数据，供单 namespace 兼容读取。
	primary Snapshot
	at      time.Time
}

// NewClient 构造客户端；Source / Verifier / Env 与至少一个 namespace 必填。
func NewClient(cfg ClientConfig) (*Client, error) {
	switch {
	case cfg.Source == nil:
		return nil, fmt.Errorf("configclient: client requires a snapshot source")
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("configclient: client requires a snapshot verifier")
	case strings.TrimSpace(cfg.Env) == "":
		return nil, fmt.Errorf("configclient: client requires env")
	}
	namespaces, err := normalizeNamespaces(cfg.Namespace, cfg.Namespaces)
	if err != nil {
		return nil, err
	}
	primary := strings.TrimSpace(cfg.PrimaryNamespace)
	if primary == "" {
		primary = namespaces[0]
	}
	if !containsNamespace(namespaces, primary) {
		namespaces = append([]string{primary}, namespaces...)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		source:         cfg.Source,
		verifier:       cfg.Verifier,
		env:            cfg.Env,
		namespaces:     namespaces,
		primary:        primary,
		store:          cfg.Store,
		now:            now,
		onRefresh:      cfg.OnRefresh,
		watchers:       make(map[chan RefreshEvent]struct{}),
		onWatchDropped: cfg.OnWatchDropped,
	}, nil
}

// containsNamespace 判断列表是否包含指定 namespace。
func containsNamespace(namespaces []string, target string) bool {
	for _, namespace := range namespaces {
		if namespace == target {
			return true
		}
	}
	return false
}

// normalizeNamespaces 归一化 namespace 列表：去空白、去重、保序。
func normalizeNamespaces(single string, list []string) ([]string, error) {
	if len(list) == 0 {
		list = []string{single}
	}
	seen := make(map[string]struct{}, len(list))
	namespaces := make([]string, 0, len(list))
	for _, namespace := range list {
		trimmed := strings.TrimSpace(namespace)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		namespaces = append(namespaces, trimmed)
	}
	if len(namespaces) == 0 {
		return nil, fmt.Errorf("configclient: client requires at least one namespace")
	}
	return namespaces, nil
}

// Namespaces 返回消费的 namespace 列表副本。
func (c *Client) Namespaces() []string {
	return append([]string(nil), c.namespaces...)
}

// Refresh 逐个 namespace 拉取并验签；成功原子替换，失败保留旧快照。
//
// 返回所有 namespace 失败原因的合并错误；部分成功时仍返回错误，但成功
// namespace 的新快照已生效。
func (c *Client) Refresh(ctx context.Context) error {
	type result struct {
		namespace string
		snapshot  Snapshot
		install   bool
		err       error
	}
	results := make([]result, 0, len(c.namespaces))
	var errs []error
	for _, namespace := range c.namespaces {
		snapshot, install, err := c.refreshNamespace(ctx, namespace)
		results = append(results, result{namespace: namespace, snapshot: snapshot, install: install, err: err})
		if err != nil {
			errs = append(errs, err)
		}
	}
	joined := errors.Join(errs...)
	if joined != nil {
		c.degraded.Store(true)
		c.setLastError(joined)
	} else {
		c.degraded.Store(false)
		c.setLastError(nil)
	}
	for _, item := range results {
		if item.install {
			c.installNamespace(item.namespace, item.snapshot)
		}
		c.observeNamespace(item.namespace, item.err)
	}
	return joined
}

// refreshNamespace 拉取并验签单个 namespace；主 namespace 失败时尝试本地快照。
//
// 返回的 install 表示是否应装载快照；使用本地兜底时仍返回原始错误，供
// 告警与指标记录降级。
func (c *Client) refreshNamespace(ctx context.Context, namespace string) (Snapshot, bool, error) {
	snapshot, err := c.source.Load(ctx, c.env, namespace)
	if err == nil {
		if verifyErr := c.verify(snapshot, namespace); verifyErr != nil {
			err = verifyErr
		} else {
			if c.store != nil && namespace == c.primary {
				// 落盘失败不影响本次读取；下次成功刷新会重试。
				_ = c.store.Save(ctx, snapshot)
			}
			return snapshot, true, nil
		}
	}
	wrapped := fmt.Errorf("configclient: load snapshot for %s: %w", namespace, err)
	if namespace == c.primary {
		if fallback, ok := c.loadFallback(ctx); ok {
			return fallback, true, wrapped
		}
	}
	return Snapshot{}, false, wrapped
}

// observeNamespace 把单个 namespace 的刷新结果交给回调与 Watch 订阅者。
func (c *Client) observeNamespace(namespace string, err error) {
	event := RefreshEvent{
		At: c.now().UTC(), Namespace: namespace,
		Degraded: c.degraded.Load(), Err: err,
	}
	if snapshot, ok := c.SnapshotFor(namespace); ok {
		event.Snapshot = snapshot
		event.Loaded = true
	}
	if c.onRefresh != nil {
		c.onRefresh(event)
	}
	c.broadcast(event)
}

// Watch 订阅后续刷新事件，直到 ctx 取消。
//
// Watch 只订阅，不驱动拉取；调用方仍需 Run 或周期性 Refresh。订阅通道
// 缓冲 16 个事件，消费过慢时丢弃最新事件并触发 OnWatchDropped，避免阻塞
// 刷新 goroutine。
func (c *Client) Watch(ctx context.Context) <-chan RefreshEvent {
	events := make(chan RefreshEvent, 16)
	c.watchMu.Lock()
	c.watchers[events] = struct{}{}
	c.watchMu.Unlock()
	go func() {
		<-ctx.Done()
		c.watchMu.Lock()
		if _, ok := c.watchers[events]; ok {
			delete(c.watchers, events)
			close(events)
		}
		c.watchMu.Unlock()
	}()
	return events
}

// broadcast 非阻塞分发事件；慢订阅者只丢事件，不拖慢刷新。
func (c *Client) broadcast(event RefreshEvent) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	for events := range c.watchers {
		select {
		case events <- event:
		default:
			if c.onWatchDropped != nil {
				c.onWatchDropped()
			}
		}
	}
}

// installNamespace 替换单个 namespace 的快照并重算合并取值。
func (c *Client) installNamespace(namespace string, snapshot Snapshot) {
	previous := c.current.Load()
	snapshots := make(map[string]Snapshot, len(c.namespaces))
	if previous != nil {
		for key, value := range previous.snapshots {
			snapshots[key] = value
		}
	}
	snapshots[namespace] = snapshot
	c.storeSnapshots(snapshots)
}

// storeSnapshots 按 namespace 顺序合并取值与条目，并原子发布。
func (c *Client) storeSnapshots(snapshots map[string]Snapshot) {
	values := make(map[string]string)
	var items []Item
	for _, namespace := range c.namespaces {
		snapshot, ok := snapshots[namespace]
		if !ok {
			continue
		}
		for key, value := range snapshot.Values {
			values[key] = value
		}
		items = append(items, snapshot.Items...)
	}
	primary := snapshots[c.primary]
	primary.Values = copyValues(values)
	primary.Items = append([]Item(nil), items...)
	c.current.Store(&loaded{
		snapshots: snapshots,
		values:    values,
		items:     items,
		primary:   primary,
		at:        c.now().UTC(),
	})
}

// loadFallback 在无内存快照时装载本地文件快照；文件也无效则返回 false。
func (c *Client) loadFallback(ctx context.Context) (Snapshot, bool) {
	if c.store == nil {
		return Snapshot{}, false
	}
	snapshot, err := c.store.Load(ctx)
	if err != nil {
		return Snapshot{}, false
	}
	if err := c.verify(snapshot, c.primary); err != nil {
		return Snapshot{}, false
	}
	return snapshot, true
}

// verify 校验快照归属与签名，拒绝跨环境 / 跨 namespace 或未签名数据。
func (c *Client) verify(snapshot Snapshot, namespace string) error {
	if snapshot.Env != c.env || snapshot.Namespace != namespace {
		return fmt.Errorf("configclient: snapshot env/namespace does not match client")
	}
	return c.verifier.Verify(snapshot)
}

// Run 按 interval（±20% 抖动）持续刷新，直到 ctx 取消。
//
// 首次立即刷新一次；后续失败只记录，不终止循环（配置读路径不因中心
// 短暂不可用而退出）。
func (c *Client) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	_ = c.Refresh(ctx)
	for {
		timer := time.NewTimer(jitter(interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			_ = c.Refresh(ctx)
		}
	}
}

// Get 返回合并后的键值；未加载或未命中时 ok=false。
func (c *Client) Get(key string) (string, bool) {
	current := c.current.Load()
	if current == nil {
		return "", false
	}
	value, ok := current.values[key]
	return value, ok
}

// String 返回字符串值；未命中或为空时返回默认值。
func (c *Client) String(key, defaultValue string) string {
	value, ok := c.Get(key)
	if !ok || value == "" {
		return defaultValue
	}
	return value
}

// Bool 返回布尔值；解析失败返回默认值。
func (c *Client) Bool(key string, defaultValue bool) bool {
	value, ok := c.Get(key)
	if !ok {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Int 返回整数值；解析失败返回默认值。
func (c *Client) Int(key string, defaultValue int64) int64 {
	value, ok := c.Get(key)
	if !ok {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Duration 返回时长值；解析失败返回默认值。
func (c *Client) Duration(key string, defaultValue time.Duration) time.Duration {
	value, ok := c.Get(key)
	if !ok {
		return defaultValue
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// Values 返回合并取值的副本；不含敏感项。
func (c *Client) Values() map[string]string {
	current := c.current.Load()
	if current == nil {
		return map[string]string{}
	}
	return copyValues(current.values)
}

// Snapshot 返回主 namespace 的快照副本，Values 为全部 namespace 合并结果。
func (c *Client) Snapshot() (Snapshot, bool) {
	current := c.current.Load()
	if current == nil {
		return Snapshot{}, false
	}
	snapshot := current.primary
	snapshot.Values = copyValues(current.values)
	snapshot.Items = append([]Item(nil), current.items...)
	return snapshot, true
}

// SnapshotFor 返回指定 namespace 的已验签快照副本。
func (c *Client) SnapshotFor(namespace string) (Snapshot, bool) {
	current := c.current.Load()
	if current == nil {
		return Snapshot{}, false
	}
	snapshot, ok := current.snapshots[namespace]
	if !ok {
		return Snapshot{}, false
	}
	snapshot.Values = copyValues(snapshot.Values)
	snapshot.Items = append([]Item(nil), snapshot.Items...)
	return snapshot, true
}

// Age 返回最近一次成功装载在本进程的存活时长；未加载时返回 0。
func (c *Client) Age() time.Duration {
	current := c.current.Load()
	if current == nil {
		return 0
	}
	age := c.now().UTC().Sub(current.at)
	if age < 0 {
		return 0
	}
	return age
}

// LastError 返回最近一次刷新失败原因；成功后清空。
func (c *Client) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Degraded 表示当前取值来自本地文件快照（中心刷新失败）。
func (c *Client) Degraded() bool {
	return c.degraded.Load()
}

func (c *Client) setLastError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = err
}

// copyValues 复制字符串映射；nil 安全。
func copyValues(values map[string]string) map[string]string {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

// jitter 在 [0.8, 1.2) 区间抖动，摊平多实例同时拉取（R-05）。
//
// 使用 crypto/rand 避免安全扫描把抖动随机源误判为弱随机；读取失败时退回原间隔。
func jitter(interval time.Duration) time.Duration {
	var buffer [8]byte
	if _, err := cryptorand.Read(buffer[:]); err != nil {
		return interval
	}
	ratio := float64(binary.BigEndian.Uint64(buffer[:])%1000) / 1000
	factor := 0.8 + 0.4*ratio
	return time.Duration(float64(interval) * factor)
}
