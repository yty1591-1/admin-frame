package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"sync/atomic"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	defaultWatchInterval = 2 * time.Second
	minWatchInterval     = 10 * time.Millisecond
	maxWatchInterval     = 10 * time.Minute
)

// WatchOptions 是 Watcher 的构造参数。
type WatchOptions[T any] struct {
	// Loader 必填，且必须声明至少一个 Files。
	Loader *Loader[T]
	// Interval 是文件轮询间隔；0 取 2s，允许 [10ms,10m]。
	//
	// 轮询读取文件内容并比较 SHA-256，避免依赖 mtime 精度；
	// 配置文件通常很小，默认间隔下开销可忽略。
	Interval time.Duration
}

// Event 描述一次加载/重载结果。
//
// Err 非 nil 时表示本次加载失败，New 为 nil，Current 仍返回上一次成功快照。
// 事件可能同时被多个订阅者读取，调用方不得修改 Old/New 指向的对象。
type Event[T any] struct {
	// At 是事件发生时间。
	At time.Time
	// Duration 是本次加载耗时。
	Duration time.Duration
	// Err 是加载/校验错误；nil 表示成功。
	Err error
	// Old 是重载前的快照；首次构造时即为初始快照。
	Old *T
	// New 是重载后的快照；失败时为 nil。
	New *T
}

// Changed 报告本次事件是否用新快照替换了旧快照。
func (e Event[T]) Changed() bool {
	return e.Err == nil && e.New != nil && e.New != e.Old
}

// Watcher 轮询配置文件并在校验通过后原子替换快照。
//
// 构造时完成首次加载，因此 Current 在 NewWatcher 成功后始终非 nil。
// 重载失败保留旧快照（fail safe），并通过订阅者上报错误；相同错误只在
// 首次出现时上报，避免轮询刷屏。并发安全；Close 会等待监视 goroutine 退出。
type Watcher[T any] struct {
	loader   *Loader[T]
	interval time.Duration
	files    []string
	now      func() time.Time

	snapshot atomic.Pointer[T]

	// reloadMu 串行化轮询与手动重载，并保护 signatures/lastErr。
	reloadMu   sync.Mutex
	signatures []string
	lastErr    string

	// mu 保护生命周期与订阅者。
	mu          sync.Mutex
	subscribers []subscriber[T]
	nextID      uint64
	started     bool
	closed      bool
	stop        chan struct{}
	done        chan struct{}
}

type subscriber[T any] struct {
	id uint64
	fn func(Event[T])
}

// NewWatcher 构造监视器并完成首次加载。
//
// 参数非法或首次加载失败时返回错误，不创建后台 goroutine。
func NewWatcher[T any](opts WatchOptions[T]) (watcher *Watcher[T], err error) {
	if opts.Loader == nil {
		return nil, invalidConfig("watcher requires a loader")
	}
	files := opts.Loader.files()
	if len(files) == 0 {
		return nil, invalidConfig("watcher requires at least one configuration file")
	}
	interval := opts.Interval
	if interval == 0 {
		interval = defaultWatchInterval
	}
	if interval < minWatchInterval || interval > maxWatchInterval {
		return nil, invalidConfig("watch interval must be in [%s,%s]", minWatchInterval, maxWatchInterval)
	}
	initial, err := opts.Loader.Load()
	if err != nil {
		return nil, err
	}
	w := &Watcher[T]{
		loader:     opts.Loader,
		interval:   interval,
		files:      files,
		now:        time.Now,
		signatures: fileSignatures(files),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	w.snapshot.Store(initial)
	return w, nil
}

// Current 返回当前生效的快照；NewWatcher 成功后始终非 nil。
//
// 返回对象为只读共享快照，调用方不得修改；需要长期持有可变副本时自行深拷贝。
func (w *Watcher[T]) Current() *T {
	if w == nil {
		return nil
	}
	return w.snapshot.Load()
}

// Subscribe 注册重载回调，返回取消订阅函数。
//
// 回调在监视 goroutine 内同步执行，必须快速返回；阻塞会推迟后续轮询。
// 回调 panic 会终止进程（与其他后台 goroutine 一致），实现方需自行防御。
// 回调内不得调用 Close（等待自身退出而死锁）或 Reload（重入 reloadMu）；
// 需要重载请另起 goroutine。
// 对 nil 回调返回空操作函数；取消函数可重复调用。
func (w *Watcher[T]) Subscribe(fn func(Event[T])) (cancel func()) {
	if w == nil || fn == nil {
		return func() { // #nosec G104 -- 空操作函数
		}
	}
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	w.subscribers = append(w.subscribers, subscriber[T]{id: id, fn: fn})
	w.mu.Unlock()

	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		for i := range w.subscribers {
			if w.subscribers[i].id == id {
				w.subscribers = append(w.subscribers[:i], w.subscribers[i+1:]...)
				return
			}
		}
	}
}

// Start 启动后台轮询；重复启动返回 ErrWatcherStarted，已关闭返回 ErrWatcherClosed。
//
// 首次加载已在 NewWatcher 完成，因此 Start 只负责变更检测。
func (w *Watcher[T]) Start() error {
	if w == nil {
		return invalidConfig("nil watcher")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return configError(ErrWatcherClosed, pkgerrors.Unavailable("watcher is closed"))
	}
	if w.started {
		return configError(ErrWatcherStarted, pkgerrors.FailedPrecondition("watcher already started"))
	}
	w.started = true
	go w.loop()
	return nil
}

// Close 停止轮询并等待 goroutine 退出；可重复调用，未启动时为空操作。
func (w *Watcher[T]) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	started := w.started
	if started {
		close(w.stop)
	}
	w.mu.Unlock()
	if started {
		<-w.done
	}
}

// Reload 立即重新加载，不等待轮询间隔；成功则替换快照并通知订阅者。
//
// 关闭后返回 ErrWatcherClosed。失败时旧快照保持不变，错误同时上报订阅者。
func (w *Watcher[T]) Reload() error {
	if w == nil {
		return invalidConfig("nil watcher")
	}
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return configError(ErrWatcherClosed, pkgerrors.Unavailable("watcher is closed"))
	}
	w.reloadMu.Lock()
	defer w.reloadMu.Unlock()
	return w.reloadLocked(false)
}

// loop 是唯一的轮询 goroutine，退出时关闭 done 供 Close 汇合。
func (w *Watcher[T]) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

// poll 检测文件内容签名变化后重载；相同错误只上报一次。
func (w *Watcher[T]) poll() {
	w.reloadMu.Lock()
	defer w.reloadMu.Unlock()
	if !w.signaturesChanged() {
		return
	}
	_ = w.reloadLocked(true) // 错误已通过订阅者上报，轮询继续。
}

// signaturesChanged 比较当前文件内容哈希与上次成功加载时记录的哈希。
func (w *Watcher[T]) signaturesChanged() bool {
	current := fileSignatures(w.files)
	if len(current) != len(w.signatures) {
		return true
	}
	for i := range current {
		if current[i] != w.signatures[i] {
			return true
		}
	}
	return false
}

// reloadLocked 执行一次加载并广播结果；调用方必须持有 reloadMu。
//
// dedupe 为 true 时，与上次完全相同的错误不再广播（轮询场景）。
func (w *Watcher[T]) reloadLocked(dedupe bool) error {
	start := w.now()
	old := w.snapshot.Load()
	next, err := w.loader.Load()
	event := Event[T]{At: start, Duration: w.now().Sub(start), Old: old}
	if err != nil {
		event.Err = err
		message := err.Error()
		if dedupe && message == w.lastErr {
			return err
		}
		w.lastErr = message
		w.notify(event)
		return err
	}
	w.snapshot.Store(next)
	w.signatures = fileSignatures(w.files)
	w.lastErr = ""
	event.New = next
	w.notify(event)
	return nil
}

// notify 在锁外同步调用订阅者，避免回调重入死锁。
func (w *Watcher[T]) notify(event Event[T]) {
	w.mu.Lock()
	handlers := make([]func(Event[T]), len(w.subscribers))
	for i := range w.subscribers {
		handlers[i] = w.subscribers[i].fn
	}
	w.mu.Unlock()
	for _, handler := range handlers {
		handler(event)
	}
}

// fileSignatures 计算文件内容哈希；读取失败时记录错误特征以触发重载尝试。
//
// 使用内容哈希而非 mtime，避免编辑器保留时间戳或时间精度不足导致漏检。
func fileSignatures(files []string) []string {
	signatures := make([]string, len(files))
	for i, path := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- 监视目标来自运维配置，非请求输入。
		if err != nil {
			signatures[i] = "!" + err.Error()
			continue
		}
		sum := sha256.Sum256(data)
		signatures[i] = hex.EncodeToString(sum[:])
	}
	return signatures
}
