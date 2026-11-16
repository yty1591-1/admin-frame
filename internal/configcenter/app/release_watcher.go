package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// DefaultWatchInterval 是指针版本扫描间隔。
const DefaultWatchInterval = time.Second

// DefaultLongPollTimeout 是通知长轮询最长挂起时间。
const DefaultLongPollTimeout = 60 * time.Second

// NamespaceNotification 描述某 namespace 的生效版本变化。
type NamespaceNotification struct {
	Namespace      string `json:"namespace"`
	NotificationID string `json:"notification_id"`
}

// ReleaseWatcher 扫描 config_release_pointer 并唤醒挂起的长轮询。
type ReleaseWatcher struct {
	pointers domain.PointerRepository
	interval time.Duration

	mu       sync.Mutex
	versions map[string]map[string]int64 // env → namespace → version
	waiters  map[uint64]*watchWaiter
	nextID   uint64
}

type watchWaiter struct {
	env        string
	known      map[string]int64
	notify     chan []NamespaceNotification
	registered bool
}

// NewReleaseWatcher 构造扫描器；pointers 必填。
func NewReleaseWatcher(pointers domain.PointerRepository, interval time.Duration) (*ReleaseWatcher, error) {
	if pointers == nil {
		return nil, apperr.Internal("release watcher requires a pointer repository", nil)
	}
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	return &ReleaseWatcher{
		pointers: pointers,
		interval: interval,
		versions: make(map[string]map[string]int64),
		waiters:  make(map[uint64]*watchWaiter),
	}, nil
}

// Run 持续扫描直到 ctx 取消。
func (w *ReleaseWatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.scan(ctx); err != nil && ctx.Err() == nil {
			// 扫描失败不退出：下一轮重试；调用方可观察日志。
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Wait 挂起直到 known 中任一 namespace 版本变化，或超时。
//
// known 的 key 为 namespace，value 为客户端已知的 pointer version（notification_id 数字部分）。
// 超时返回 (nil, nil)；有变化返回通知列表。
func (w *ReleaseWatcher) Wait(ctx context.Context, env string, known map[string]int64,
	timeout time.Duration) ([]NamespaceNotification, error) {
	env = strings.TrimSpace(env)
	if !domain.ValidEnv(env) {
		return nil, apperr.BadRequest("invalid env")
	}
	if len(known) == 0 {
		return nil, apperr.BadRequest("namespaces query is required")
	}
	if timeout <= 0 {
		timeout = DefaultLongPollTimeout
	}

	if changed := w.diff(env, known); len(changed) > 0 {
		return changed, nil
	}

	waiter := &watchWaiter{
		env:    env,
		known:  copyVersions(known),
		notify: make(chan []NamespaceNotification, 1),
	}
	id := w.register(waiter)
	defer w.unregister(id)

	// 注册后再扫一次，避免注册窗口漏掉变更。
	_ = w.scan(ctx)
	if changed := w.diff(env, known); len(changed) > 0 {
		return changed, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	case notes := <-waiter.notify:
		return notes, nil
	}
}

// CurrentVersions 返回 env 下缓存的 namespace → version；未扫描过则读库。
func (w *ReleaseWatcher) CurrentVersions(ctx context.Context, env string) (map[string]int64, error) {
	if err := w.scanEnv(ctx, env); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return copyVersions(w.versions[env]), nil
}

func (w *ReleaseWatcher) register(waiter *watchWaiter) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	id := w.nextID
	waiter.registered = true
	w.waiters[id] = waiter
	return id
}

func (w *ReleaseWatcher) unregister(id uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.waiters, id)
}

func (w *ReleaseWatcher) scan(ctx context.Context) error {
	envs := w.watchedEnvs()
	var first error
	for _, env := range envs {
		if err := w.scanEnv(ctx, env); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (w *ReleaseWatcher) watchedEnvs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := make(map[string]struct{}, len(w.versions)+len(w.waiters))
	for env := range w.versions {
		seen[env] = struct{}{}
	}
	for _, waiter := range w.waiters {
		seen[waiter.env] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for env := range seen {
		out = append(out, env)
	}
	sort.Strings(out)
	return out
}

func (w *ReleaseWatcher) scanEnv(ctx context.Context, env string) error {
	pointers, err := w.pointers.ListByEnv(ctx, env)
	if err != nil {
		return err
	}
	next := make(map[string]int64, len(pointers))
	for _, pointer := range pointers {
		next[pointer.Namespace] = pointer.Version
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	prev := w.versions[env]
	w.versions[env] = next
	if prev == nil {
		prev = map[string]int64{}
	}
	for id, waiter := range w.waiters {
		if waiter.env != env {
			continue
		}
		notes := diffLocked(next, waiter.known)
		if len(notes) == 0 {
			continue
		}
		select {
		case waiter.notify <- notes:
		default:
		}
		delete(w.waiters, id)
	}
	_ = prev
	return nil
}

func (w *ReleaseWatcher) diff(env string, known map[string]int64) []NamespaceNotification {
	w.mu.Lock()
	defer w.mu.Unlock()
	return diffLocked(w.versions[env], known)
}

func diffLocked(current, known map[string]int64) []NamespaceNotification {
	if current == nil {
		return nil
	}
	notes := make([]NamespaceNotification, 0, len(known))
	for namespace, version := range known {
		cur, ok := current[namespace]
		if !ok {
			continue
		}
		if cur != version {
			notes = append(notes, NamespaceNotification{
				Namespace:      namespace,
				NotificationID: strconv.FormatInt(cur, 10),
			})
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].Namespace < notes[j].Namespace })
	return notes
}

func copyVersions(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ReleaseKey 由 release_id 与 pointer_version 组成，供客户端做 304 判定。
func ReleaseKey(releaseID, pointerVersion int64) string {
	return fmt.Sprintf("%d:%d", releaseID, pointerVersion)
}
