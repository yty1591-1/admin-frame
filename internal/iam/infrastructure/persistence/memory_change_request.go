package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryChangeRequestRepository 是基于内存 map 的审批单仓储。
//
// 仅用于单测与 local / demo：审批单要求与业务写入同事务、并要求跨实例可见，
// 生产必须使用 SQLChangeRequestRepository（后续落地）。
type MemoryChangeRequestRepository struct {
	mu       sync.RWMutex
	requests map[string]*domain.ChangeRequest
}

// NewMemoryChangeRequestRepository 创建空的审批单仓储。
func NewMemoryChangeRequestRepository() *MemoryChangeRequestRepository {
	return &MemoryChangeRequestRepository{
		requests: make(map[string]*domain.ChangeRequest),
	}
}

// FindByID 按主键加载审批单；不存在返回 NotFound。
func (r *MemoryChangeRequestRepository) FindByID(_ context.Context, id string) (*domain.ChangeRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	request, ok := r.requests[id]
	if !ok {
		return nil, apperr.NotFound("change request not found")
	}
	return cloneChangeRequest(request), nil
}

// Save 创建或更新审批单。
//
// expectedVersion 为 0 表示创建：主键冲突返回 Conflict；
// 非 0 表示更新：版本必须一致，否则返回 Conflict（乐观锁）。
// 审批单的端别与动作一旦创建即不可变，防止用同一张单子偷换目标。
func (r *MemoryChangeRequestRepository) Save(_ context.Context, request *domain.ChangeRequest, expectedVersion int64) error {
	if request == nil {
		return apperr.BadRequest("change request is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.requests[request.ID]
	if expectedVersion == 0 {
		if exists {
			return apperr.Conflict("change request id already exists")
		}
		r.requests[request.ID] = cloneChangeRequest(request)
		return nil
	}
	if !exists {
		return apperr.NotFound("change request not found")
	}
	if existing.Version != expectedVersion {
		return apperr.Conflict("change request version conflict")
	}
	if existing.Channel != request.Channel || existing.Action != request.Action {
		return apperr.BadRequest("change request channel and action are immutable")
	}
	r.requests[request.ID] = cloneChangeRequest(request)
	return nil
}

// List 按条件分页返回审批单，按申请时间倒序（同一时刻按 ID 倒序）。
func (r *MemoryChangeRequestRepository) List(_ context.Context, filter domain.ChangeRequestFilter) ([]*domain.ChangeRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*domain.ChangeRequest, 0, len(r.requests))
	for _, request := range r.requests {
		if filter.Channel != "" && request.Channel != filter.Channel {
			continue
		}
		if filter.Status != "" && request.Status != filter.Status {
			continue
		}
		if filter.RequestedBy != "" && request.RequestedBy != filter.RequestedBy {
			continue
		}
		if filter.Action != "" && request.Action != filter.Action {
			continue
		}
		out = append(out, cloneChangeRequest(request))
	}

	slices.SortFunc(out, func(a, b *domain.ChangeRequest) int {
		if byTime := b.RequestedAt.Compare(a.RequestedAt); byTime != 0 {
			return byTime
		}
		return strings.Compare(b.ID, a.ID)
	})
	return paginate(out, filter.Offset, filter.Limit), nil
}

// cloneChangeRequest 返回审批单深拷贝，隔离 Payload / ApprovedAt / AppliedAt。
func cloneChangeRequest(request *domain.ChangeRequest) *domain.ChangeRequest {
	if request == nil {
		return nil
	}
	cp := *request
	cp.Payload = slices.Clone(request.Payload)
	if request.ApprovedAt != nil {
		approvedAt := *request.ApprovedAt
		cp.ApprovedAt = &approvedAt
	}
	if request.AppliedAt != nil {
		appliedAt := *request.AppliedAt
		cp.AppliedAt = &appliedAt
	}
	return &cp
}
