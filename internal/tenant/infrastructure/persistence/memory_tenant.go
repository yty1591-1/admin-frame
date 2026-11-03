// Package persistence 提供租户仓储实现（内存 / MySQL）。
//
// 两套实现共享同一组领域语义（ErrNotFound / ErrVersionConflict），
// 使应用层与传输层不需要按驱动分支。
package persistence

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// MemoryRepository 是租户仓储的内存实现（local / dev / test）。
//
// 语义与 MySQL 实现保持一致：创建检测主键冲突、更新走版本 CAS。
type MemoryRepository struct {
	mu sync.RWMutex
	// txMu 串行化成员变更临界区（对应 MySQL 实现的租户行锁）。
	// 独立于 mu：临界区内仍要调用读写方法，共用一把锁会自锁。
	txMu        sync.Mutex
	items       map[string]domain.Tenant
	members     map[string]map[string]domain.Member
	quotas      map[string]map[string]domain.Quota
	plans       map[string]domain.Plan
	invitations map[string]domain.Invitation
}

// NewMemoryRepository 构造内存仓储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		items:       make(map[string]domain.Tenant),
		members:     make(map[string]map[string]domain.Member),
		quotas:      make(map[string]map[string]domain.Quota),
		plans:       make(map[string]domain.Plan),
		invitations: make(map[string]domain.Invitation),
	}
}

// WithinTx 通过快照实现回滚：fn 返回错误时恢复写入前的三张表。
//
// 内存模式也要有事务语义，否则「审计失败 → 业务回滚」的用例在本地永远是绿的，
// 到 MySQL 上才发现行为不同。写入串行由 txMu 保证（与 Mutate 互斥）。
func (r *MemoryRepository) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("tenant: transaction requires a function")
	}
	r.txMu.Lock()
	defer r.txMu.Unlock()
	snapshot := r.snapshot()
	if err := fn(ctx); err != nil {
		r.restore(snapshot)
		return err
	}
	return nil
}

// memorySnapshot 是内存表的最小快照。
//
// 只复制 map，不深拷贝实体：仓储的写入都是「整条替换」，不会原地改结构体。
type memorySnapshot struct {
	items       map[string]domain.Tenant
	members     map[string]map[string]domain.Member
	quotas      map[string]map[string]domain.Quota
	invitations map[string]domain.Invitation
}

func (r *MemoryRepository) snapshot() memorySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := memorySnapshot{
		items:       make(map[string]domain.Tenant, len(r.items)),
		members:     make(map[string]map[string]domain.Member, len(r.members)),
		quotas:      make(map[string]map[string]domain.Quota, len(r.quotas)),
		invitations: make(map[string]domain.Invitation, len(r.invitations)),
	}
	for k, v := range r.items {
		out.items[k] = v
	}
	for tenantID, bySubject := range r.members {
		copied := make(map[string]domain.Member, len(bySubject))
		for k, v := range bySubject {
			copied[k] = v
		}
		out.members[tenantID] = copied
	}
	for tenantID, byResource := range r.quotas {
		copied := make(map[string]domain.Quota, len(byResource))
		for k, v := range byResource {
			copied[k] = v
		}
		out.quotas[tenantID] = copied
	}
	for k, v := range r.invitations {
		out.invitations[k] = v
	}
	return out
}

func (r *MemoryRepository) restore(snapshot memorySnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = snapshot.items
	r.members = snapshot.members
	r.quotas = snapshot.quotas
	r.invitations = snapshot.invitations
}

// Mutate 串行化同一租户的成员变更。
//
// 内存实现没有事务，用独立互斥锁模拟「持锁执行」：临界区内调用本仓储的
// 读写方法不会死锁（它们用 mu，本方法用 txMu）。代价是全局串行，
// 但内存实现只服务本地开发与单测，可接受。
//
// 与 WithinTx 一样带快照回滚：Add 的「成员写入 + 审计」同事务语义
// 在内存模式下也必须成立，否则用例假绿、到 MySQL 上才发现审计失败
// 没有回滚成员。
func (r *MemoryRepository) Mutate(ctx context.Context, _ string, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("tenant: member mutation requires a function")
	}
	r.txMu.Lock()
	defer r.txMu.Unlock()
	snapshot := r.snapshot()
	if err := fn(ctx); err != nil {
		r.restore(snapshot)
		return err
	}
	return nil
}

// Create 新建租户；ID 或 code 重复返回对应领域错误。
func (r *MemoryRepository) Create(_ context.Context, tenant domain.Tenant) error {
	if err := tenant.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[tenant.ID]; ok {
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, tenant.ID)
	}
	for _, item := range r.items {
		if item.Code == tenant.Code {
			return fmt.Errorf("%w: %s", domain.ErrCodeExists, tenant.Code)
		}
	}
	r.items[tenant.ID] = tenant
	return nil
}

// Get 按 ID 读取租户副本。
func (r *MemoryRepository) Get(_ context.Context, id string) (domain.Tenant, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenant, ok := r.items[strings.TrimSpace(id)]
	if !ok {
		return domain.Tenant{}, domain.ErrNotFound
	}
	return tenant, nil
}

// List 按状态 / 名称过滤并分页。
func (r *MemoryRepository) List(_ context.Context, filter domain.Filter) ([]domain.Tenant, int, error) {
	filter = filter.Normalize()
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]domain.Tenant, 0, len(r.items))
	for _, tenant := range r.items {
		if filter.Status != "" && tenant.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" && !strings.Contains(tenant.Name, filter.Keyword) {
			continue
		}
		matched = append(matched, tenant)
	}
	// 与 SQL 的 ORDER BY created_at DESC, id DESC 对齐，保证分页稳定。
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	if filter.Offset >= total {
		return nil, total, nil
	}
	end := filter.Offset + filter.Limit
	if end > total {
		end = total
	}
	return matched[filter.Offset:end], total, nil
}

// UpdateStatus 以版本 CAS 写入迁移结果。
func (r *MemoryRepository) UpdateStatus(_ context.Context, tenant domain.Tenant) error {
	return r.casUpdate(tenant)
}

// UpdateProfile 以版本 CAS 写入资料变更（T-11）。
func (r *MemoryRepository) UpdateProfile(_ context.Context, tenant domain.Tenant) error {
	return r.casUpdate(tenant)
}

// casUpdate 是两条 CAS 通道（状态 / 资料）的共同实现。
func (r *MemoryRepository) casUpdate(tenant domain.Tenant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.items[tenant.ID]
	if !ok {
		return domain.ErrNotFound
	}
	expected := tenant.Version - 1
	if current.Version != expected {
		return fmt.Errorf("%w: tenant %s version %d, expected %d",
			domain.ErrVersionConflict, tenant.ID, current.Version, expected)
	}
	if err := tenant.Validate(); err != nil {
		return err
	}
	r.items[tenant.ID] = tenant
	return nil
}

// UpsertMember 按 (tenant_id, subject_id) 幂等写入成员。
//
// 已存在时保留首次加入时间：created_at 表达「何时进这个租户」，
// 改角色或停用后再启用都不应重置它。
func (r *MemoryRepository) UpsertMember(_ context.Context, member domain.Member) error {
	if err := member.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bySubject, ok := r.members[member.TenantID]
	if !ok {
		bySubject = make(map[string]domain.Member)
		r.members[member.TenantID] = bySubject
	}
	if existing, exists := bySubject[member.SubjectID]; exists {
		member.CreatedAt = existing.CreatedAt
	}
	bySubject[member.SubjectID] = member
	return nil
}

// GetMember 读取单个成员。
func (r *MemoryRepository) GetMember(_ context.Context, tenantID, subjectID string) (domain.Member, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	member, ok := r.members[strings.TrimSpace(tenantID)][strings.TrimSpace(subjectID)]
	if !ok {
		return domain.Member{}, domain.ErrMemberNotFound
	}
	return member, nil
}

// ListMembers 分页列出租户成员；默认只返回在册成员，按角色与主体排序保证分页稳定。
// limit<=0 表示全量；返回满足条件的总数。
func (r *MemoryRepository) ListMembers(_ context.Context, tenantID string, includeDisabled bool, limit, offset int) ([]domain.Member, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bySubject := r.members[strings.TrimSpace(tenantID)]
	matched := make([]domain.Member, 0, len(bySubject))
	for _, member := range bySubject {
		if !includeDisabled && !member.Active() {
			continue
		}
		matched = append(matched, member)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Role != matched[j].Role {
			return roleOrder(matched[i].Role) < roleOrder(matched[j].Role)
		}
		return matched[i].SubjectID < matched[j].SubjectID
	})

	total := len(matched)
	if offset >= total {
		return nil, total, nil
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	return matched[offset:end], total, nil
}

// CountActiveByRole 统计某角色的在册成员数。
func (r *MemoryRepository) CountActiveByRole(_ context.Context, tenantID string, role domain.MemberRole) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, member := range r.members[strings.TrimSpace(tenantID)] {
		if member.Role == role && member.Active() {
			count++
		}
	}
	return count, nil
}

// CountActive 统计在册成员总数（T-13 席位上限的当前值）。
func (r *MemoryRepository) CountActive(_ context.Context, tenantID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, member := range r.members[strings.TrimSpace(tenantID)] {
		if member.Active() {
			count++
		}
	}
	return count, nil
}

// UpsertQuota 按 (tenant_id, resource) 幂等写入配额。
func (r *MemoryRepository) UpsertQuota(_ context.Context, quota domain.Quota) error {
	if err := quota.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byResource, ok := r.quotas[quota.TenantID]
	if !ok {
		byResource = make(map[string]domain.Quota)
		r.quotas[quota.TenantID] = byResource
	}
	byResource[quota.Resource] = quota
	return nil
}

// ListQuotas 列出租户配额，按资源键排序。
func (r *MemoryRepository) ListQuotas(_ context.Context, tenantID string) ([]domain.Quota, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byResource := r.quotas[strings.TrimSpace(tenantID)]
	out := make([]domain.Quota, 0, len(byResource))
	for _, quota := range byResource {
		out = append(out, quota)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out, nil
}

// roleOrder 定义角色排序权重：owner 在前，便于列表直接读「谁负责」。
func roleOrder(role domain.MemberRole) int {
	switch role {
	case domain.MemberRoleOwner:
		return 0
	case domain.MemberRoleAdmin:
		return 1
	default:
		return 2
	}
}

// UpsertPlan 写入或覆盖套餐目录（内存实现供测试构造目录）。
func (r *MemoryRepository) UpsertPlan(plan domain.Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plans == nil {
		r.plans = make(map[string]domain.Plan)
	}
	r.plans[plan.Code] = plan
	return nil
}

// DefaultPlan 返回默认套餐。
func (r *MemoryRepository) DefaultPlan(_ context.Context) (domain.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, plan := range r.plans {
		if plan.IsDefault {
			return plan, nil
		}
	}
	return domain.Plan{}, domain.ErrNoDefaultPlan
}

// FindByCode 按编码加载套餐。
func (r *MemoryRepository) FindByCode(_ context.Context, code string) (domain.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plan, ok := r.plans[strings.TrimSpace(code)]
	if !ok {
		return domain.Plan{}, domain.ErrPlanNotFound
	}
	return plan, nil
}

// CreateInvitation 插入邀请；同租户同邮箱存在 pending 邀请返回
// ErrInvitationPendingExists（与 MySQL 生成列唯一索引同一语义）。
func (r *MemoryRepository) CreateInvitation(_ context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.invitations[invitation.ID]; ok {
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, invitation.ID)
	}
	for _, item := range r.invitations {
		if item.Status == domain.InvitationPending &&
			item.TenantID == invitation.TenantID &&
			strings.EqualFold(item.Email, invitation.Email) {
			return fmt.Errorf("%w: %s", domain.ErrInvitationPendingExists, invitation.Email)
		}
	}
	r.invitations[invitation.ID] = invitation
	return nil
}

// GetInvitation 按 (tenant, invitation) 读取。
func (r *MemoryRepository) GetInvitation(_ context.Context, tenantID, invitationID string) (domain.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	invitation, ok := r.invitations[strings.TrimSpace(invitationID)]
	if !ok || invitation.TenantID != strings.TrimSpace(tenantID) {
		return domain.Invitation{}, domain.ErrInvitationNotFound
	}
	return invitation, nil
}

// FindByTokenHash 按摘要查找邀请。
func (r *MemoryRepository) FindByTokenHash(_ context.Context, tokenHash string) (domain.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, invitation := range r.invitations {
		if invitation.TokenHash == strings.TrimSpace(tokenHash) {
			return invitation, nil
		}
	}
	return domain.Invitation{}, domain.ErrInvitationNotFound
}

// ListInvitations 分页列出邀请；与成员列表同一排序（时间倒序）保证分页稳定。
func (r *MemoryRepository) ListInvitations(_ context.Context, tenantID, status string, limit, offset int) ([]domain.Invitation, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	matched := make([]domain.Invitation, 0, len(r.invitations))
	for _, invitation := range r.invitations {
		if invitation.TenantID != strings.TrimSpace(tenantID) {
			continue
		}
		if status != "" && invitation.Status != domain.InvitationStatus(status) {
			continue
		}
		matched = append(matched, invitation)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	if offset >= total {
		return nil, total, nil
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	return matched[offset:end], total, nil
}

// CompleteInvitation 以 pending CAS 写入终态；0 命中返回 ErrInvitationNotPending。
func (r *MemoryRepository) CompleteInvitation(_ context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.invitations[invitation.ID]
	if !ok || current.TenantID != invitation.TenantID {
		return domain.ErrInvitationNotFound
	}
	if current.Status != domain.InvitationPending {
		return fmt.Errorf("%w: %s", domain.ErrInvitationNotPending, invitation.ID)
	}
	r.invitations[invitation.ID] = invitation
	return nil
}

// RefreshInvitation 覆盖摘要并顺延有效期；仅 pending 可刷新。
func (r *MemoryRepository) RefreshInvitation(_ context.Context, invitation domain.Invitation) error {
	if err := invitation.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.invitations[invitation.ID]
	if !ok || current.TenantID != invitation.TenantID {
		return domain.ErrInvitationNotFound
	}
	if current.Status != domain.InvitationPending {
		return fmt.Errorf("%w: %s", domain.ErrInvitationNotPending, invitation.ID)
	}
	// pending 唯一约束对自身不生效（同一行的 status 未变），直接覆盖。
	r.invitations[invitation.ID] = invitation
	return nil
}
