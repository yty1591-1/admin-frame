package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryCertificationCampaignRepository 是内存版访问复核活动仓储。
//
// 仅用于单测与 local / demo；生产必须使用 SQL 实现，才能与审计 Outbox 同事务。
type MemoryCertificationCampaignRepository struct {
	mu        sync.RWMutex
	campaigns map[string]*domain.CertificationCampaign
}

// NewMemoryCertificationCampaignRepository 创建空的活动仓储。
func NewMemoryCertificationCampaignRepository() *MemoryCertificationCampaignRepository {
	return &MemoryCertificationCampaignRepository{
		campaigns: make(map[string]*domain.CertificationCampaign),
	}
}

// Save 创建（expectedVersion=0）或更新活动；端别与商户创建后不可变。
func (r *MemoryCertificationCampaignRepository) Save(
	_ context.Context, campaign *domain.CertificationCampaign, expectedVersion int64,
) error {
	if campaign == nil {
		return apperr.BadRequest("certification campaign is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.campaigns[campaign.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("certification campaign id already exists")
		}
		r.campaigns[campaign.ID] = cloneCertificationCampaign(campaign)
		return nil
	}
	if !exists {
		return apperr.NotFound("certification campaign not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("certification campaign version conflict")
	}
	if existing.Channel != campaign.Channel || existing.MerchantID != campaign.MerchantID {
		return apperr.BadRequest("certification campaign channel and merchant are immutable")
	}
	r.campaigns[campaign.ID] = cloneCertificationCampaign(campaign)
	return nil
}

// FindByID 按主键加载活动。
func (r *MemoryCertificationCampaignRepository) FindByID(
	_ context.Context, id string,
) (*domain.CertificationCampaign, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	campaign, ok := r.campaigns[id]
	if !ok {
		return nil, apperr.NotFound("certification campaign not found")
	}
	return cloneCertificationCampaign(campaign), nil
}

// List 按条件分页返回活动，按创建时间倒序（同一时刻按 ID 倒序）。
func (r *MemoryCertificationCampaignRepository) List(
	_ context.Context, filter domain.CertificationCampaignFilter,
) ([]*domain.CertificationCampaign, error) {
	if filter.Channel == domain.ChannelBiz && strings.TrimSpace(filter.MerchantID) == "" {
		return nil, apperr.BadRequest("merchant id is required on channel biz")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.CertificationCampaign, 0, len(r.campaigns))
	for _, campaign := range r.campaigns {
		if !matchCampaignFilter(campaign, filter) {
			continue
		}
		out = append(out, cloneCertificationCampaign(campaign))
	}
	slices.SortFunc(out, func(a, b *domain.CertificationCampaign) int {
		if byTime := b.CreatedAt.Compare(a.CreatedAt); byTime != 0 {
			return byTime
		}
		return strings.Compare(b.ID, a.ID)
	})
	return paginate(out, filter.Offset, filter.Limit), nil
}

// Count 返回满足条件的活动总数。
func (r *MemoryCertificationCampaignRepository) Count(
	_ context.Context, filter domain.CertificationCampaignFilter,
) (int, error) {
	if filter.Channel == domain.ChannelBiz && strings.TrimSpace(filter.MerchantID) == "" {
		return 0, apperr.BadRequest("merchant id is required on channel biz")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, campaign := range r.campaigns {
		if matchCampaignFilter(campaign, filter) {
			total++
		}
	}
	return total, nil
}

// matchCampaignFilter 判断活动是否命中过滤条件。
func matchCampaignFilter(campaign *domain.CertificationCampaign, filter domain.CertificationCampaignFilter) bool {
	if filter.Channel != "" && campaign.Channel != filter.Channel {
		return false
	}
	if filter.MerchantID != "" && campaign.MerchantID != filter.MerchantID {
		return false
	}
	if filter.Status != "" && campaign.Status != filter.Status {
		return false
	}
	return true
}

// MemoryCertificationTaskRepository 是内存版访问复核任务仓储。
type MemoryCertificationTaskRepository struct {
	mu    sync.RWMutex
	tasks map[string]*domain.CertificationTask
	// keys 以 (campaign_id,subject_id,role_id) 映射主键，用于幂等批量写入。
	keys map[string]string
}

// NewMemoryCertificationTaskRepository 创建空的任务仓储。
func NewMemoryCertificationTaskRepository() *MemoryCertificationTaskRepository {
	return &MemoryCertificationTaskRepository{
		tasks: make(map[string]*domain.CertificationTask),
		keys:  make(map[string]string),
	}
}

// SaveAll 幂等批量写入：重复键保留原任务主键与原有状态，绝不覆盖已裁决任务。
func (r *MemoryCertificationTaskRepository) SaveAll(_ context.Context, tasks []*domain.CertificationTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range tasks {
		if task == nil {
			return apperr.BadRequest("certification task is required")
		}
		key := certificationTaskKey(task)
		if existingID, ok := r.keys[key]; ok {
			if existingID != task.ID {
				// 重复生成：保留既有主键，继续处理下一条。
				continue
			}
			// 同一主键重放：同样保留既有状态。
			continue
		}
		if _, exists := r.tasks[task.ID]; exists {
			return apperr.Conflict("certification task id already exists")
		}
		r.tasks[task.ID] = cloneCertificationTask(task)
		r.keys[key] = task.ID
	}
	return nil
}

// FindByID 按主键加载任务。
func (r *MemoryCertificationTaskRepository) FindByID(
	_ context.Context, id string,
) (*domain.CertificationTask, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	task, ok := r.tasks[id]
	if !ok {
		return nil, apperr.NotFound("certification task not found")
	}
	return cloneCertificationTask(task), nil
}

// ListByCampaign 返回活动下全部任务，按创建时间、主键升序。
func (r *MemoryCertificationTaskRepository) ListByCampaign(
	_ context.Context, campaignID string,
) ([]*domain.CertificationTask, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.CertificationTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task.CampaignID != campaignID {
			continue
		}
		out = append(out, cloneCertificationTask(task))
	}
	sortCertificationTasks(out)
	return out, nil
}

// ListPage 按活动与状态分页列举任务。
func (r *MemoryCertificationTaskRepository) ListPage(
	_ context.Context, filter domain.CertificationTaskFilter,
) ([]*domain.CertificationTask, error) {
	if strings.TrimSpace(filter.CampaignID) == "" {
		return nil, apperr.BadRequest("campaign id is required")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.CertificationTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task.CampaignID != filter.CampaignID {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		out = append(out, cloneCertificationTask(task))
	}
	sortCertificationTasks(out)
	return paginate(out, filter.Offset, filter.Limit), nil
}

// CountByCampaign 返回活动下的任务总数。
func (r *MemoryCertificationTaskRepository) CountByCampaign(_ context.Context, campaignID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, task := range r.tasks {
		if task.CampaignID == campaignID {
			total++
		}
	}
	return total, nil
}

// Save 更新任务并执行乐观锁；不允许把任务移动到其他活动或改换主体 / 角色。
func (r *MemoryCertificationTaskRepository) Save(
	_ context.Context, task *domain.CertificationTask, expectedVersion int64,
) error {
	if task == nil {
		return apperr.BadRequest("certification task is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.tasks[task.ID]
	if !ok {
		return apperr.NotFound("certification task not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("certification task version conflict")
	}
	key := certificationTaskKey(task)
	if owner, ok := r.keys[key]; ok && owner != task.ID {
		return apperr.Conflict("certification task subject and role already exist in campaign")
	}
	delete(r.keys, certificationTaskKey(existing))
	r.tasks[task.ID] = cloneCertificationTask(task)
	r.keys[key] = task.ID
	return nil
}

// certificationTaskKey 返回任务唯一键。
func certificationTaskKey(task *domain.CertificationTask) string {
	return task.CampaignID + "|" + task.SubjectID + "|" + task.RoleID
}

// sortCertificationTasks 按创建时间、主键升序排序，保证分页与导出稳定。
func sortCertificationTasks(tasks []*domain.CertificationTask) {
	slices.SortFunc(tasks, func(a, b *domain.CertificationTask) int {
		if byTime := a.CreatedAt.Compare(b.CreatedAt); byTime != 0 {
			return byTime
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// cloneCertificationCampaign 深拷贝活动，隔离 RoleIDs / ClosedAt。
func cloneCertificationCampaign(campaign *domain.CertificationCampaign) *domain.CertificationCampaign {
	if campaign == nil {
		return nil
	}
	cp := *campaign
	cp.RoleIDs = slices.Clone(campaign.RoleIDs)
	if campaign.ClosedAt != nil {
		closedAt := *campaign.ClosedAt
		cp.ClosedAt = &closedAt
	}
	return &cp
}

// cloneCertificationTask 深拷贝任务，隔离 DecidedAt。
func cloneCertificationTask(task *domain.CertificationTask) *domain.CertificationTask {
	if task == nil {
		return nil
	}
	cp := *task
	if task.DecidedAt != nil {
		decidedAt := *task.DecidedAt
		cp.DecidedAt = &decidedAt
	}
	return &cp
}
