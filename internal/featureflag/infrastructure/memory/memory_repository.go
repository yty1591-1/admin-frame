// Package memory 是 featureflag 域的进程内仓储（单测与内存模式）。
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
)

// Repository 是进程内仓储（单测与内存模式）。
type Repository struct {
	mu     sync.RWMutex
	byKey  map[string]domain.Flag
	layout []string // 创建序，稳定分页
}

// NewMemoryRepository 构造空仓储。
func NewMemoryRepository() *Repository {
	return &Repository{byKey: make(map[string]domain.Flag)}
}

// Create 写入新 flag；key 冲突返回 domain.ErrDuplicateKey。
func (r *Repository) Create(_ context.Context, f domain.Flag) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKey[f.Key]; exists {
		return domain.ErrDuplicateKey
	}
	r.byKey[f.Key] = f
	r.layout = append(r.layout, f.Key)
	return nil
}

// Get 按 key 读取；不存在返回 domain.ErrNotFound。
func (r *Repository) Get(_ context.Context, key string) (domain.Flag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.byKey[strings.TrimSpace(key)]
	if !ok {
		return domain.Flag{}, domain.ErrNotFound
	}
	return f, nil
}

// List 按 state 过滤列出（按 key 字典序，稳定分页）。
func (r *Repository) List(_ context.Context, state string, limit, offset int) ([]domain.Flag, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := append([]string(nil), r.layout...)
	sort.Strings(keys)
	all := make([]domain.Flag, 0, len(keys))
	for _, key := range keys {
		f := r.byKey[key]
		if state != "" && string(f.Status) != state {
			continue
		}
		all = append(all, f)
	}
	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []domain.Flag{}, total, nil
	}
	if limit <= 0 {
		limit = 20
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

// Update 以 CAS 更新（expectedVersion 不匹配返回 domain.ErrVersionConflict）。
func (r *Repository) Update(_ context.Context, f domain.Flag, expectedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.byKey[f.Key]
	if !ok {
		return domain.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return domain.ErrVersionConflict
	}
	r.byKey[f.Key] = f
	return nil
}

// Delete 物理删除；不存在返回 domain.ErrNotFound。
func (r *Repository) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byKey, key)
	for i, k := range r.layout {
		if k == key {
			r.layout = append(r.layout[:i], r.layout[i+1:]...)
			break
		}
	}
	return nil
}

// ListExpired 列出到期时刻仍为 enabled 的 flag。
func (r *Repository) ListExpired(_ context.Context, now time.Time, limit int) ([]domain.Flag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []domain.Flag
	for _, key := range r.layout {
		f := r.byKey[key]
		if f.Status != domain.StatusEnabled || f.Permanent {
			continue
		}
		if !f.ExpiresAt.IsZero() && !f.ExpiresAt.After(now) {
			out = append(out, f)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
