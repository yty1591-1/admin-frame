package configclient

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
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
	// Namespace 命名空间；必填。
	Namespace string
	// Store 本地快照仓库；可选。配置后刷新成功即原子落盘，中心不可用时
	// 冷启动回退到本地已验签快照。
	Store SnapshotStore
	// Now 时钟；测试注入。
	Now func() time.Time
	// OnRefresh 在每次刷新尝试后回调；用于运行期日志与指标，可为 nil。
	//
	// 回调在刷新 goroutine 内同步执行，必须快速返回，且不得调用 Refresh / Run。
	// 事件中的 Snapshot 为只读副本，调用方不得修改。
	OnRefresh func(RefreshEvent)
}

// RefreshEvent 描述一次刷新尝试的结果。
//
// Err 非 nil 表示本次拉取或验签失败；此时 Loaded 为 true 说明仍在服务
// 上一份已验签快照（CFG-12）。事件只携带快照元数据与取值，敏感项本就不
// 在快照内，调用方仍不得把取值写入日志。
type RefreshEvent struct {
	// At 是刷新完成时间。
	At time.Time
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
// 失败语义（CFG-12）：刷新失败保留上一份已验签快照继续服务，错误只记录
// 在 LastError / 告警链路；不存在可用快照时 Get 返回未命中，由调用方
// 回退代码默认值。
type Client struct {
	source    Source
	verifier  *Verifier
	env       string
	namespace string
	store     SnapshotStore
	now       func() time.Time
	onRefresh func(RefreshEvent)

	current  atomic.Pointer[loaded]
	degraded atomic.Bool
	mu       sync.Mutex
	lastErr  error
}

// loaded 是一次成功刷新的不可变结果；写入后不再修改。
type loaded struct {
	snapshot Snapshot
	values   map[string]string
	at       time.Time
}

// NewClient 构造客户端；Source / Verifier / Env / Namespace 必填。
func NewClient(cfg ClientConfig) (*Client, error) {
	switch {
	case cfg.Source == nil:
		return nil, fmt.Errorf("configclient: client requires a snapshot source")
	case cfg.Verifier == nil:
		return nil, fmt.Errorf("configclient: client requires a snapshot verifier")
	case strings.TrimSpace(cfg.Env) == "":
		return nil, fmt.Errorf("configclient: client requires env")
	case strings.TrimSpace(cfg.Namespace) == "":
		return nil, fmt.Errorf("configclient: client requires namespace")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		source:    cfg.Source,
		verifier:  cfg.Verifier,
		env:       cfg.Env,
		namespace: cfg.Namespace,
		store:     cfg.Store,
		now:       now,
		onRefresh: cfg.OnRefresh,
	}, nil
}

// Refresh 拉取并验签一份快照；成功原子替换缓存，失败保留旧快照。
//
// 无可用内存快照时先尝试从本地文件快照恢复（再验签），仍失败则保持空缓存。
func (c *Client) Refresh(ctx context.Context) error {
	err := c.refresh(ctx)
	c.observe(err)
	return err
}

// refresh 执行一次拉取、验签与装载；不触发刷新回调。
func (c *Client) refresh(ctx context.Context) error {
	snapshot, err := c.source.Load(ctx, c.env, c.namespace)
	if err != nil {
		c.degraded.Store(true)
		c.setLastError(err)
		c.loadFallback(ctx)
		return fmt.Errorf("configclient: load snapshot: %w", err)
	}
	if err := c.verify(snapshot); err != nil {
		c.degraded.Store(true)
		c.setLastError(err)
		c.loadFallback(ctx)
		return err
	}
	c.install(snapshot)
	c.degraded.Store(false)
	c.setLastError(nil)
	if c.store != nil {
		// 落盘失败不影响本次读取；下次成功刷新会重试。
		_ = c.store.Save(ctx, snapshot)
	}
	return nil
}

// observe 把本次刷新结果交给回调；未配置回调时为空操作。
func (c *Client) observe(err error) {
	if c.onRefresh == nil {
		return
	}
	event := RefreshEvent{At: c.now().UTC(), Degraded: c.degraded.Load(), Err: err}
	if snapshot, ok := c.Snapshot(); ok {
		event.Snapshot = snapshot
		event.Loaded = true
	}
	c.onRefresh(event)
}

// install 原子替换当前快照。
func (c *Client) install(snapshot Snapshot) {
	values := make(map[string]string, len(snapshot.Values))
	for key, value := range snapshot.Values {
		values[key] = value
	}
	c.current.Store(&loaded{snapshot: snapshot, values: values, at: c.now().UTC()})
}

// loadFallback 在无内存快照时装载本地文件快照；文件也无效则保持空缺。
func (c *Client) loadFallback(ctx context.Context) {
	if c.store == nil || c.current.Load() != nil {
		return
	}
	snapshot, err := c.store.Load(ctx)
	if err != nil {
		return
	}
	if err := c.verify(snapshot); err != nil {
		return
	}
	c.install(snapshot)
}

// verify 校验快照归属与签名，拒绝跨环境文件或未签名数据。
func (c *Client) verify(snapshot Snapshot) error {
	if snapshot.Env != c.env || snapshot.Namespace != c.namespace {
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

// Get 返回键值；未加载或未命中时 ok=false。
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

// Values 返回当前取值的副本；不含敏感项。
func (c *Client) Values() map[string]string {
	current := c.current.Load()
	if current == nil {
		return map[string]string{}
	}
	copied := make(map[string]string, len(current.values))
	for key, value := range current.values {
		copied[key] = value
	}
	return copied
}

// Snapshot 返回当前快照副本；未加载时 ok=false。
func (c *Client) Snapshot() (Snapshot, bool) {
	current := c.current.Load()
	if current == nil {
		return Snapshot{}, false
	}
	snapshot := current.snapshot
	snapshot.Items = append([]Item(nil), snapshot.Items...)
	snapshot.Values = make(map[string]string, len(current.values))
	for key, value := range current.values {
		snapshot.Values[key] = value
	}
	return snapshot, true
}

// Age 返回当前快照在本进程的存活时长；未加载时返回 0。
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
