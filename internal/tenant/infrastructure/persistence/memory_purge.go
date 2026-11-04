package persistence

// ENT-15 数据处置的内存实现（单测与内存模式）：manifest 仓储与租户表边界。

import (
	"context"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// MemoryExportManifestRepository 是导出证据的进程内实现。
type MemoryExportManifestRepository struct {
	mu        sync.RWMutex
	manifests map[string][]domain.ExportManifest
}

// NewMemoryExportManifestRepository 构造内存 manifest 仓储。
func NewMemoryExportManifestRepository() *MemoryExportManifestRepository {
	return &MemoryExportManifestRepository{manifests: make(map[string][]domain.ExportManifest)}
}

// Record 登记一条 manifest。
func (r *MemoryExportManifestRepository) Record(_ context.Context, m domain.ExportManifest) error {
	m = m.Normalized()
	if err := m.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifests[m.TenantID] = append(r.manifests[m.TenantID], m)
	return nil
}

// FindLatest 返回该租户最新一条 manifest。
func (r *MemoryExportManifestRepository) FindLatest(_ context.Context, tenantID string) (domain.ExportManifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows := r.manifests[strings.TrimSpace(tenantID)]
	if len(rows) == 0 {
		return domain.ExportManifest{}, domain.ErrManifestNotFound
	}
	latest := rows[0]
	for _, m := range rows[1:] {
		if m.CreatedAt.After(latest.CreatedAt) {
			latest = m
		}
	}
	return latest, nil
}

// MemoryTenantPurger 是租户表边界的内存实现：删除租户 + 级联成员 / 配额 / 邀请。
type MemoryTenantPurger struct {
	repo *MemoryRepository
}

// NewMemoryTenantPurger 构造（复用内存仓储的内部映射，删除即真删）。
func NewMemoryTenantPurger(repo *MemoryRepository) *MemoryTenantPurger {
	return &MemoryTenantPurger{repo: repo}
}

// Name 边界名。
func (p *MemoryTenantPurger) Name() string { return "tenant" }

// Purge 删除该租户全部租户域数据，返回删除行数。
func (p *MemoryTenantPurger) Purge(_ context.Context, tenantID string, _ int) (int64, error) {
	id := strings.TrimSpace(tenantID)
	p.repo.mu.Lock()
	defer p.repo.mu.Unlock()
	tenant, ok := p.repo.items[id]
	if !ok {
		return 0, nil
	}
	if tenant.Status != domain.StatusTerminated {
		return 0, domain.ErrTerminated
	}
	var total int64
	total += int64(len(p.repo.members[id]))
	delete(p.repo.members, id)
	total += int64(len(p.repo.quotas[id]))
	delete(p.repo.quotas, id)
	for invID, inv := range p.repo.invitations {
		if inv.TenantID == id {
			delete(p.repo.invitations, invID)
			total++
		}
	}
	delete(p.repo.items, id)
	total++
	return total, nil
}
