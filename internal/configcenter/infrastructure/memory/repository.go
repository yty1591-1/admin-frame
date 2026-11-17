// Package memory 是配置中心的进程内仓储，用于单测与 memory 模式。
//
// 语义与 MySQL 实现保持一致：唯一键冲突、乐观锁与指针 CAS 均返回
// domain 哨兵错误；不承担业务校验（由 domain.Validate 与应用层完成）。
package memory

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// Repositories 汇总三个持久化端口的进程内实现。
type Repositories struct {
	Items    domain.ItemRepository
	Releases domain.ReleaseRepository
	Pointers domain.PointerRepository
}

// New 构造空仓储集合。
func New() *Repositories {
	store := newStore()
	return &Repositories{
		Items:    &itemRepository{store: store},
		Releases: &releaseRepository{store: store},
		Pointers: &pointerRepository{store: store},
	}
}

type store struct {
	mu sync.RWMutex

	items      map[int64]domain.Item
	itemKeys   map[string]int64
	versions   map[int64][]domain.ItemVersion
	releases   map[int64]domain.Release
	releaseNos map[string]int64
	relItems   map[int64][]domain.ReleaseItem
	reviews    map[int64][]domain.ReleaseReview
	pointers   map[string]domain.Pointer
}

func newStore() *store {
	return &store{
		items:      make(map[int64]domain.Item),
		itemKeys:   make(map[string]int64),
		versions:   make(map[int64][]domain.ItemVersion),
		releases:   make(map[int64]domain.Release),
		releaseNos: make(map[string]int64),
		relItems:   make(map[int64][]domain.ReleaseItem),
		reviews:    make(map[int64][]domain.ReleaseReview),
		pointers:   make(map[string]domain.Pointer),
	}
}

func itemKey(env, namespace, key string) string {
	return env + "\x00" + namespace + "\x00" + key
}

func releaseNoKey(env, namespace string) string {
	return env + "\x00" + namespace
}

func releaseNoEntry(env, namespace string, releaseNo int64) string {
	return releaseNoKey(env, namespace) + "\x00" + strconv.FormatInt(releaseNo, 10)
}

// itemRepository 实现 domain.ItemRepository。
type itemRepository struct {
	store *store
}

// Create 写入配置键；ID 或三元组冲突返回 domain.ErrDuplicateKey。
func (r *itemRepository) Create(_ context.Context, item domain.Item) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, exists := r.store.items[item.ID]; exists {
		return domain.ErrDuplicateKey
	}
	key := itemKey(item.Env, item.Namespace, item.Key)
	if _, exists := r.store.itemKeys[key]; exists {
		return domain.ErrDuplicateKey
	}
	r.store.items[item.ID] = cloneItem(item)
	r.store.itemKeys[key] = item.ID
	return nil
}

// GetByID 按 ID 读取；不存在返回 domain.ErrNotFound。
func (r *itemRepository) GetByID(_ context.Context, id int64) (domain.Item, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	item, ok := r.store.items[id]
	if !ok {
		return domain.Item{}, domain.ErrNotFound
	}
	return cloneItem(item), nil
}

// GetByKey 按 (env, namespace, key) 读取；不存在返回 domain.ErrNotFound。
func (r *itemRepository) GetByKey(_ context.Context, env, namespace, key string) (domain.Item, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	id, ok := r.store.itemKeys[itemKey(env, namespace, key)]
	if !ok {
		return domain.Item{}, domain.ErrNotFound
	}
	return cloneItem(r.store.items[id]), nil
}

// List 按状态过滤列出配置键，按 key 升序稳定分页；status 为空表示全部。
func (r *itemRepository) List(_ context.Context, env, namespace string, status domain.ItemStatus, limit, offset int) ([]domain.Item, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	all := make([]domain.Item, 0, len(r.store.items))
	for _, item := range r.store.items {
		if item.Env != env || item.Namespace != namespace {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		all = append(all, cloneItem(item))
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Key == all[j].Key {
			return all[i].ID < all[j].ID
		}
		return all[i].Key < all[j].Key
	})
	return paginate(all, limit, offset)
}

// UpdateCAS 以 expectedVersion 做乐观锁更新；版本不匹配返回 domain.ErrVersionConflict。
func (r *itemRepository) UpdateCAS(_ context.Context, item domain.Item, expectedVersion int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	existing, ok := r.store.items[item.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return domain.ErrVersionConflict
	}
	oldKey := itemKey(existing.Env, existing.Namespace, existing.Key)
	newKey := itemKey(item.Env, item.Namespace, item.Key)
	if newKey != oldKey {
		if id, exists := r.store.itemKeys[newKey]; exists && id != item.ID {
			return domain.ErrDuplicateKey
		}
		delete(r.store.itemKeys, oldKey)
		r.store.itemKeys[newKey] = item.ID
	}
	r.store.items[item.ID] = cloneItem(item)
	return nil
}

// AppendVersion 追加不可变版本；(item_id, version) 冲突返回 domain.ErrDuplicateKey。
func (r *itemRepository) AppendVersion(_ context.Context, version domain.ItemVersion) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.items[version.ItemID]; !ok {
		return domain.ErrNotFound
	}
	for _, existing := range r.store.versions[version.ItemID] {
		if existing.Version == version.Version {
			return domain.ErrDuplicateKey
		}
	}
	r.store.versions[version.ItemID] = append(r.store.versions[version.ItemID], version)
	return nil
}

// GetVersion 读取指定版本；不存在返回 domain.ErrNotFound。
func (r *itemRepository) GetVersion(_ context.Context, itemID, version int64) (domain.ItemVersion, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, existing := range r.store.versions[itemID] {
		if existing.Version == version {
			return existing, nil
		}
	}
	return domain.ItemVersion{}, domain.ErrNotFound
}

// ListVersions 按版本升序列出并稳定分页。
func (r *itemRepository) ListVersions(_ context.Context, itemID int64, limit, offset int) ([]domain.ItemVersion, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	all := append([]domain.ItemVersion(nil), r.store.versions[itemID]...)
	sort.Slice(all, func(i, j int) bool { return all[i].Version < all[j].Version })
	return paginate(all, limit, offset)
}

// releaseRepository 实现 domain.ReleaseRepository。
type releaseRepository struct {
	store *store
}

// Create 写入发布头与快照条目；ID 或 release_no 冲突返回 domain.ErrDuplicateKey。
func (r *releaseRepository) Create(_ context.Context, release domain.Release, items []domain.ReleaseItem) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, exists := r.store.releases[release.ID]; exists {
		return domain.ErrDuplicateKey
	}
	noEntry := releaseNoEntry(release.Env, release.Namespace, release.ReleaseNo)
	if _, exists := r.store.releaseNos[noEntry]; exists {
		return domain.ErrDuplicateKey
	}
	for _, item := range items {
		if item.ReleaseID != release.ID {
			return domain.ErrStateConflict
		}
	}
	r.store.releases[release.ID] = cloneRelease(release)
	r.store.releaseNos[noEntry] = release.ID
	r.store.relItems[release.ID] = append([]domain.ReleaseItem(nil), items...)
	return nil
}

// Get 按 ID 读取发布；不存在返回 domain.ErrNotFound。
func (r *releaseRepository) Get(_ context.Context, id int64) (domain.Release, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	release, ok := r.store.releases[id]
	if !ok {
		return domain.Release{}, domain.ErrNotFound
	}
	return cloneRelease(release), nil
}

// GetByNo 按 (env, namespace, release_no) 读取；不存在返回 domain.ErrNotFound。
func (r *releaseRepository) GetByNo(_ context.Context, env, namespace string, releaseNo int64) (domain.Release, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	id, ok := r.store.releaseNos[releaseNoEntry(env, namespace, releaseNo)]
	if !ok {
		return domain.Release{}, domain.ErrNotFound
	}
	return cloneRelease(r.store.releases[id]), nil
}

// List 按过滤条件列出发布，按 release_no 降序稳定分页。
func (r *releaseRepository) List(_ context.Context, filter domain.ReleaseListFilter, limit, offset int) ([]domain.Release, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	owner := strings.TrimSpace(filter.Owner)
	all := make([]domain.Release, 0, len(r.store.releases))
	for _, release := range r.store.releases {
		if release.Env != filter.Env || release.Namespace != filter.Namespace {
			continue
		}
		if filter.Status != "" && release.Status != filter.Status {
			continue
		}
		if owner != "" && release.CreatedBy != owner {
			continue
		}
		if keyword != "" {
			name := strings.ToLower(release.Name)
			desc := strings.ToLower(release.Description)
			if !strings.Contains(name, keyword) && !strings.Contains(desc, keyword) {
				continue
			}
		}
		all = append(all, cloneRelease(release))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ReleaseNo > all[j].ReleaseNo })
	return paginate(all, limit, offset)
}

// NextReleaseNo 返回同 (env, namespace) 的下一个候选发布号。
func (r *releaseRepository) NextReleaseNo(_ context.Context, env, namespace string) (int64, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	prefix := releaseNoKey(env, namespace)
	var maxNo int64
	for entry, id := range r.store.releaseNos {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		if release, ok := r.store.releases[id]; ok && release.ReleaseNo > maxNo {
			maxNo = release.ReleaseNo
		}
	}
	return maxNo + 1, nil
}

// Transition 按前置状态迁移发布单；前置状态不符返回 domain.ErrStateConflict。
func (r *releaseRepository) Transition(_ context.Context, id int64, from, to domain.ReleaseStatus, actor string, at time.Time) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	release, ok := r.store.releases[id]
	if !ok {
		return domain.ErrNotFound
	}
	if release.Status != from || !from.CanTransitionTo(to) {
		return domain.ErrStateConflict
	}
	release.Status = to
	release.UpdatedAt = at
	switch to {
	case domain.ReleaseStatusPendingReview:
		release.SubmittedBy = actor
	case domain.ReleaseStatusApproved:
		release.ApprovedBy = actor
		release.ApprovedAt = timePtr(at)
	case domain.ReleaseStatusActive:
		release.PublishedAt = timePtr(at)
	}
	r.store.releases[id] = release
	return nil
}

// UpdateSnapshotSignature 写入发布快照签名；发布不存在返回 domain.ErrNotFound。
func (r *releaseRepository) UpdateSnapshotSignature(_ context.Context, id int64, signature, keyID string, at time.Time) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	release, ok := r.store.releases[id]
	if !ok {
		return domain.ErrNotFound
	}
	release.SnapshotSignature = signature
	release.SnapshotKeyID = keyID
	release.UpdatedAt = at
	r.store.releases[id] = release
	return nil
}

// ListItems 返回发布快照条目（按 item_id 升序）。
func (r *releaseRepository) ListItems(_ context.Context, releaseID int64) ([]domain.ReleaseItem, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	items := append([]domain.ReleaseItem(nil), r.store.relItems[releaseID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].ItemID < items[j].ItemID })
	return items, nil
}

// AppendReview 追加审批记录；ID 冲突返回 domain.ErrDuplicateKey。
func (r *releaseRepository) AppendReview(_ context.Context, review domain.ReleaseReview) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.reviews[review.ReleaseID] {
		if existing.ID == review.ID {
			return domain.ErrDuplicateKey
		}
	}
	r.store.reviews[review.ReleaseID] = append(r.store.reviews[review.ReleaseID], review)
	return nil
}

// ListReviews 返回审批记录（按 created_at、id 升序）。
func (r *releaseRepository) ListReviews(_ context.Context, releaseID int64) ([]domain.ReleaseReview, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	reviews := append([]domain.ReleaseReview(nil), r.store.reviews[releaseID]...)
	sort.Slice(reviews, func(i, j int) bool {
		if reviews[i].CreatedAt.Equal(reviews[j].CreatedAt) {
			return reviews[i].ID < reviews[j].ID
		}
		return reviews[i].CreatedAt.Before(reviews[j].CreatedAt)
	})
	return reviews, nil
}

// pointerRepository 实现 domain.PointerRepository。
type pointerRepository struct {
	store *store
}

// Get 读取生效指针；不存在返回 domain.ErrNotFound。
func (r *pointerRepository) Get(_ context.Context, env, namespace string) (domain.Pointer, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	pointer, ok := r.store.pointers[releaseNoKey(env, namespace)]
	if !ok {
		return domain.Pointer{}, domain.ErrNotFound
	}
	return pointer, nil
}

// ListByEnv 返回某环境下全部生效指针。
func (r *pointerRepository) ListByEnv(_ context.Context, env string) ([]domain.Pointer, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	out := make([]domain.Pointer, 0, 8)
	for _, pointer := range r.store.pointers {
		if pointer.Env == env {
			out = append(out, pointer)
		}
	}
	return out, nil
}

// Create 初始化指针；已存在返回 domain.ErrDuplicateKey。
func (r *pointerRepository) Create(_ context.Context, pointer domain.Pointer) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	key := releaseNoKey(pointer.Env, pointer.Namespace)
	if _, exists := r.store.pointers[key]; exists {
		return domain.ErrDuplicateKey
	}
	r.store.pointers[key] = pointer
	return nil
}

// CompareAndSwap 以 expectedVersion 切换生效发布；失败返回 domain.ErrPointerConflict。
func (r *pointerRepository) CompareAndSwap(_ context.Context, env, namespace string, expectedVersion, activeReleaseID int64, now time.Time) (domain.Pointer, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	key := releaseNoKey(env, namespace)
	pointer, ok := r.store.pointers[key]
	if !ok {
		return domain.Pointer{}, domain.ErrNotFound
	}
	if pointer.Version != expectedVersion {
		return domain.Pointer{}, domain.ErrPointerConflict
	}
	pointer.ActiveReleaseID = activeReleaseID
	pointer.Version++
	pointer.UpdatedAt = now
	r.store.pointers[key] = pointer
	return pointer, nil
}

func paginate[T any](all []T, limit, offset int) (page []T, total int, err error) {
	total = len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []T{}, total, nil
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

func cloneItem(item domain.Item) domain.Item {
	item.ExpiresAt = cloneTime(item.ExpiresAt)
	return item
}

func cloneRelease(release domain.Release) domain.Release {
	release.BasedOnReleaseID = cloneInt64(release.BasedOnReleaseID)
	release.ApprovedAt = cloneTime(release.ApprovedAt)
	release.PublishedAt = cloneTime(release.PublishedAt)
	return release
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copied := *t
	return &copied
}

func cloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
}

func timePtr(t time.Time) *time.Time { return &t }
