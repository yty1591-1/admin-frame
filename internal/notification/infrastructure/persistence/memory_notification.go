// Package persistence 提供通知域的仓储实现；内存实现服务于单测与本地开发。
//
// 内存实现不追求事务原子性（与租户域 memory_tenant 同一口径），
// 但必须保住与 SQL 实现一致的唯一性约束与幂等语义——否则单测绿的
// 上线红的，约束形同虚设。
package persistence

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

// MemoryRequestRepository 是通知单仓储的内存实现。
type MemoryRequestRepository struct {
	mu       sync.RWMutex
	requests map[string]domain.Notification // id -> 通知单
	byKey    map[string]string              // idempotency_key -> id
	order    []string                       // 插入序，稳定列表输出
}

// NewMemoryRequestRepository 构造内存仓储。
func NewMemoryRequestRepository() *MemoryRequestRepository {
	return &MemoryRequestRepository{
		requests: make(map[string]domain.Notification),
		byKey:    make(map[string]string),
	}
}

// Create 落库；幂等键冲突返回 ErrDuplicateRequest。
func (r *MemoryRequestRepository) Create(_ context.Context, n domain.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKey[n.ID]; exists {
		return domain.ErrDuplicateRequest
	}
	key := n.IdempotencyKey
	if key != "" {
		if _, exists := r.byKey[key]; exists {
			return domain.ErrDuplicateRequest
		}
		r.byKey[key] = n.ID
	}
	r.requests[n.ID] = n
	r.order = append(r.order, n.ID)
	return nil
}

// Get 按 ID 读取。
func (r *MemoryRequestRepository) Get(_ context.Context, id string) (domain.Notification, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.requests[id]
	if !ok {
		return domain.Notification{}, domain.ErrNotFound
	}
	return n, nil
}

// FindByIdempotencyKey 按幂等键查找。
func (r *MemoryRequestRepository) FindByIdempotencyKey(_ context.Context, key string) (domain.Notification, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byKey[key]
	if !ok {
		return domain.Notification{}, domain.ErrNotFound
	}
	return r.requests[id], nil
}

// UpdateStatus CAS 写入：以 n.Version-1 为预期旧版本。
func (r *MemoryRequestRepository) UpdateStatus(_ context.Context, n domain.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.requests[n.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if current.Version != n.Version-1 {
		return domain.ErrVersionConflict
	}
	r.requests[n.ID] = n
	return nil
}

// List 分页查询（tenant_id 强制过滤；场景 / 状态可选）。
func (r *MemoryRequestRepository) List(_ context.Context, filter domain.RequestFilter) ([]domain.Notification, int, error) {
	filter = filter.Normalize()
	r.mu.RLock()
	defer r.mu.RUnlock()
	matched := make([]domain.Notification, 0, len(r.order))
	for _, id := range r.order {
		n := r.requests[id]
		if n.TenantID != filter.TenantID {
			continue
		}
		if filter.Scene != "" && n.Scene != filter.Scene {
			continue
		}
		if filter.Status != "" && n.Status != filter.Status {
			continue
		}
		matched = append(matched, n)
	}
	total := len(matched)
	if filter.Offset >= len(matched) {
		return []domain.Notification{}, total, nil
	}
	end := filter.Offset + filter.Limit
	if end > len(matched) {
		end = len(matched)
	}
	out := make([]domain.Notification, end-filter.Offset)
	copy(out, matched[filter.Offset:end])
	return out, total, nil
}

// WithinTx 顺序执行（内存实现不提供事务语义）。
func (r *MemoryRequestRepository) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// MemoryDeliveryRepository 是投递明细仓储的内存实现。
type MemoryDeliveryRepository struct {
	mu         sync.Mutex
	deliveries map[string]domain.Delivery // request_id|channel -> 明细
}

// NewMemoryDeliveryRepository 构造内存仓储。
func NewMemoryDeliveryRepository() *MemoryDeliveryRepository {
	return &MemoryDeliveryRepository{
		deliveries: make(map[string]domain.Delivery),
	}
}

func deliveryKey(requestID string, channel domain.Channel) string {
	return requestID + "|" + string(channel)
}

// Upsert 幂等写入。
func (r *MemoryDeliveryRepository) Upsert(_ context.Context, d domain.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries[deliveryKey(d.RequestID, d.Channel)] = d
	return nil
}

// Get 按 (request_id, channel) 读取。
func (r *MemoryDeliveryRepository) Get(_ context.Context, requestID string, channel domain.Channel) (domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.deliveries[deliveryKey(requestID, channel)]
	if !ok {
		return domain.Delivery{}, domain.ErrDeliveryNotFound
	}
	return d, nil
}

// ListByRequest 列出通知单全部明细（按渠道排序保证稳定输出）。
func (r *MemoryDeliveryRepository) ListByRequest(_ context.Context, requestID string) ([]domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Delivery, 0)
	for key, d := range r.deliveries {
		if len(key) > len(requestID) && key[:len(requestID)] == requestID && key[len(requestID)] == '|' {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out, nil
}

// Update 按 ID 覆盖（状态机合法性由领域层保证，仓储只存）。
func (r *MemoryDeliveryRepository) Update(_ context.Context, d domain.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := deliveryKey(d.RequestID, d.Channel)
	if _, ok := r.deliveries[key]; !ok {
		return domain.ErrDeliveryNotFound
	}
	r.deliveries[key] = d
	return nil
}

// FindByMessageID 按渠道消息 ID 定位明细（回执对账键）。
func (r *MemoryDeliveryRepository) FindByMessageID(_ context.Context, channel domain.Channel, messageID string) (domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(messageID) == "" {
		return domain.Delivery{}, domain.ErrDeliveryNotFound
	}
	for _, d := range r.deliveries {
		if d.Channel == channel && d.MessageID == messageID {
			return d, nil
		}
	}
	return domain.Delivery{}, domain.ErrDeliveryNotFound
}

// CountByStatus 按状态统计。
func (r *MemoryDeliveryRepository) CountByStatus(_ context.Context) (map[domain.DeliveryStatus]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := make(map[domain.DeliveryStatus]int)
	for _, d := range r.deliveries {
		counts[d.Status]++
	}
	return counts, nil
}

// MemoryTemplateRepository 是模板仓储的内存实现。
type MemoryTemplateRepository struct {
	mu        sync.Mutex
	templates map[string]domain.Template // key|channel|locale -> 最新版本
}

// NewMemoryTemplateRepository 构造内存仓储。
func NewMemoryTemplateRepository() *MemoryTemplateRepository {
	return &MemoryTemplateRepository{
		templates: make(map[string]domain.Template),
	}
}

func templateKey(key string, channel domain.Channel, locale string) string {
	return key + "|" + string(channel) + "|" + locale
}

// Upsert 写入模板：同定位键已存在则版本 +1。
func (r *MemoryTemplateRepository) Upsert(_ context.Context, t domain.Template) (domain.Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := templateKey(t.Key, t.Channel, t.Locale)
	if prev, ok := r.templates[k]; ok {
		t.Version = prev.Version + 1
	}
	r.templates[k] = t
	return t, nil
}

// Find 定位启用中的最新版本。
func (r *MemoryTemplateRepository) Find(_ context.Context, key string, channel domain.Channel, locale string) (domain.Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.templates[templateKey(key, channel, locale)]
	if !ok || !t.Enabled {
		return domain.Template{}, domain.ErrTemplateNotFound
	}
	return t, nil
}

// List 按模板键列出全部版本。
func (r *MemoryTemplateRepository) List(_ context.Context, key string) ([]domain.Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Template, 0)
	for _, t := range r.templates {
		if t.Key == key {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// MemoryPreferenceRepository 是偏好仓储的内存实现。
type MemoryPreferenceRepository struct {
	mu          sync.Mutex
	preferences map[string]domain.Preference
}

// NewMemoryPreferenceRepository 构造内存仓储。
func NewMemoryPreferenceRepository() *MemoryPreferenceRepository {
	return &MemoryPreferenceRepository{
		preferences: make(map[string]domain.Preference),
	}
}

func preferenceKey(p domain.Preference) string {
	return p.TenantID + "|" + string(p.SubjectType) + "|" + p.SubjectID +
		"|" + string(p.SceneClass) + "|" + string(p.Channel)
}

// Upsert 幂等写入。
func (r *MemoryPreferenceRepository) Upsert(_ context.Context, p domain.Preference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.preferences[preferenceKey(p)]; !ok {
		p.CreatedAt = p.UpdatedAt
	}
	r.preferences[preferenceKey(p)] = p
	return nil
}

// Get 读取单条偏好。
func (r *MemoryPreferenceRepository) Get(_ context.Context, tenantID string, subjectType domain.SubjectType,
	subjectID string, class domain.SceneClass, channel domain.Channel) (domain.Preference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.preferences[preferenceKey(domain.Preference{
		TenantID: tenantID, SubjectType: subjectType, SubjectID: subjectID,
		SceneClass: class, Channel: channel,
	})]
	if !ok {
		return domain.Preference{}, domain.ErrNotFound
	}
	return p, nil
}

// ListForSubject 列出主体全部偏好。
func (r *MemoryPreferenceRepository) ListForSubject(_ context.Context, tenantID string,
	subjectType domain.SubjectType, subjectID string) ([]domain.Preference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := tenantID + "|" + string(subjectType) + "|" + subjectID + "|"
	out := make([]domain.Preference, 0)
	for key, p := range r.preferences {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SceneClass != out[j].SceneClass {
			return out[i].SceneClass < out[j].SceneClass
		}
		return out[i].Channel < out[j].Channel
	})
	return out, nil
}

// MemoryBlacklistRepository 是黑名单仓储的内存实现。
type MemoryBlacklistRepository struct {
	mu    sync.Mutex
	items map[string]domain.Blacklist
}

// NewMemoryBlacklistRepository 构造内存仓储。
func NewMemoryBlacklistRepository() *MemoryBlacklistRepository {
	return &MemoryBlacklistRepository{
		items: make(map[string]domain.Blacklist),
	}
}

func blacklistKey(tenantID string, channel domain.Channel, targetHMAC string) string {
	return tenantID + "|" + string(channel) + "|" + targetHMAC
}

// Add 加入黑名单（幂等）。
func (r *MemoryBlacklistRepository) Add(_ context.Context, b domain.Blacklist) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[blacklistKey(b.TenantID, b.Channel, b.TargetHMAC)] = b
	return nil
}

// Exists 判断目标是否在黑名单：租户级或平台级命中即 true。
func (r *MemoryBlacklistRepository) Exists(_ context.Context, tenantID string, channel domain.Channel, targetHMAC string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[blacklistKey(tenantID, channel, targetHMAC)]; ok {
		return true, nil
	}
	if tenantID != domain.PlatformTenantID {
		if _, ok := r.items[blacklistKey(domain.PlatformTenantID, channel, targetHMAC)]; ok {
			return true, nil
		}
	}
	return false, nil
}

// Remove 移除黑名单条目。
func (r *MemoryBlacklistRepository) Remove(_ context.Context, tenantID string, channel domain.Channel, targetHMAC string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := blacklistKey(tenantID, channel, targetHMAC)
	if _, ok := r.items[key]; !ok {
		return domain.ErrNotFound
	}
	delete(r.items, key)
	return nil
}

// List 列出租户黑名单（含平台级）。
func (r *MemoryBlacklistRepository) List(_ context.Context, tenantID string) ([]domain.Blacklist, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Blacklist, 0)
	for _, b := range r.items {
		if b.TenantID == tenantID || b.TenantID == domain.PlatformTenantID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
