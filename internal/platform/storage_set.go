package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// StorageSet 同进程多桶：Default 为 yaml 顶层 storage；named 来自 storage.buckets。
type StorageSet struct {
	Default storage.Store
	named   map[string]storage.Store
}

// Get 按名取 Store；"" / "default" 返回 Default。
func (s *StorageSet) Get(name string) (storage.Store, bool) {
	if s == nil || s.Default == nil {
		return nil, false
	}
	n := strings.TrimSpace(name)
	if n == "" || strings.EqualFold(n, "default") {
		return s.Default, true
	}
	st, ok := s.named[n]
	return st, ok
}

// MustGet 同 Get；不存在时 panic（仅装配期使用）。
func (s *StorageSet) MustGet(name string) storage.Store {
	st, ok := s.Get(name)
	if !ok {
		panic("platform: storage bucket not found: " + name)
	}
	return st
}

// Names 返回命名桶名（不含 default），已排序。
func (s *StorageSet) Names() []string {
	if s == nil || len(s.named) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.named))
	for k := range s.named {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Close 关闭 Default 与全部命名桶，返回首个错误。
func (s *StorageSet) Close() error {
	if s == nil {
		return nil
	}
	var first error
	closeOne := func(st storage.Store) {
		if st == nil {
			return
		}
		if err := st.Close(); err != nil && first == nil {
			first = err
		}
	}
	closeOne(s.Default)
	for _, name := range s.Names() {
		closeOne(s.named[name])
	}
	return first
}

// ReadyAll 对 Default + 命名桶做就绪探测；任一失败即返回。
func (s *StorageSet) ReadyAll(ctx context.Context) error {
	if s == nil || s.Default == nil {
		return storage.ErrClosed
	}
	if err := storage.Ready(ctx, s.Default); err != nil {
		return fmt.Errorf("storage default: %w", err)
	}
	for _, name := range s.Names() {
		if err := storage.Ready(ctx, s.named[name]); err != nil {
			return fmt.Errorf("storage %s: %w", name, err)
		}
	}
	return nil
}

// OpenStorageSet 打开默认桶与 storage.buckets 命名桶。
//
// 命名桶复用 StorageConfig（类型别名），字段映射只经 ToStorageConfig 一处，
// 避免新增字段时多处漏改；命名桶内的 buckets 段不被支持。
func OpenStorageSet(ctx context.Context, cfg StorageConfig) (*StorageSet, error) {
	primary := cfg
	primary.Buckets = nil
	def, err := OpenStorage(ctx, primary)
	if err != nil {
		return nil, err
	}
	set := &StorageSet{
		Default: def,
		named:   make(map[string]storage.Store, len(cfg.Buckets)),
	}
	for name, bcfg := range cfg.Buckets {
		n := strings.TrimSpace(name)
		if n == "" || strings.EqualFold(n, "default") {
			_ = set.Close()
			return nil, fmt.Errorf("storage.buckets name %q is reserved", name)
		}
		if len(bcfg.Buckets) > 0 {
			_ = set.Close()
			return nil, fmt.Errorf("storage.buckets.%s.buckets is not supported (nested buckets)", n)
		}
		st, err := OpenStorage(ctx, bcfg)
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("storage.buckets.%s: %w", n, err)
		}
		set.named[n] = st
	}
	if err := MaybeApplyLifecycle(ctx, set.Default, cfg.Lifecycle); err != nil {
		_ = set.Close()
		return nil, fmt.Errorf("storage.lifecycle: %w", err)
	}
	for name, bcfg := range cfg.Buckets {
		n := strings.TrimSpace(name)
		if err := MaybeApplyLifecycle(ctx, set.named[n], bcfg.Lifecycle); err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("storage.buckets.%s.lifecycle: %w", n, err)
		}
	}
	return set, nil
}

// MaybeApplyLifecycle 仅当 ApplyOnStart 时写桶策略；驱动不支持则返回错误。
func MaybeApplyLifecycle(ctx context.Context, store storage.Store, cfg storage.LifecycleConfig) error {
	if !cfg.ApplyOnStart {
		return nil
	}
	return storage.ApplyLifecycle(ctx, store, cfg.Rules)
}
