package app

import (
	"context"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// QueryService 是通知查询用例（ENT-10 NT-06）：通知单列表 / 详情 /
// 渠道投递明细。隔离约束：TenantID 必填（仓储层强制过滤），平台级运维
// 查询显式传 domain.PlatformTenantID。
type QueryService struct {
	requests   domain.RequestRepository
	deliveries domain.DeliveryRepository
}

// NewQueryService 构造查询用例。
func NewQueryService(requests domain.RequestRepository, deliveries domain.DeliveryRepository) (*QueryService, error) {
	switch {
	case requests == nil:
		return nil, apperr.Internal("notification query service requires a request repository", nil)
	case deliveries == nil:
		return nil, apperr.Internal("notification query service requires a delivery repository", nil)
	}
	return &QueryService{requests: requests, deliveries: deliveries}, nil
}

// ListRequests 分页查询通知单。
func (s *QueryService) ListRequests(ctx context.Context, filter domain.RequestFilter) ([]domain.Notification, int, error) {
	if strings.TrimSpace(filter.TenantID) == "" {
		return nil, 0, apperr.BadRequest("tenant id is required")
	}
	items, total, err := s.requests.List(ctx, filter)
	if err != nil {
		return nil, 0, apperr.Internal("list notification requests failed", err)
	}
	return items, total, nil
}

// GetRequest 读取通知单详情。
func (s *QueryService) GetRequest(ctx context.Context, tenantID, id string) (domain.Notification, error) {
	if strings.TrimSpace(id) == "" {
		return domain.Notification{}, apperr.BadRequest("request id is required")
	}
	n, err := s.requests.Get(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Notification{}, apperr.NotFound("notification request not found")
		}
		return domain.Notification{}, apperr.Internal("load notification request failed", err)
	}
	// 隔离校验在读取侧兜底（仓储过滤针对列表；单条必须显式比对）。
	if n.TenantID != tenantID {
		return domain.Notification{}, apperr.NotFound("notification request not found")
	}
	return n, nil
}

// ListDeliveries 读取通知单的逐渠道投递明细（先做隔离校验）。
func (s *QueryService) ListDeliveries(ctx context.Context, tenantID, requestID string) ([]domain.Delivery, error) {
	if _, err := s.GetRequest(ctx, tenantID, requestID); err != nil {
		return nil, err
	}
	items, err := s.deliveries.ListByRequest(ctx, requestID)
	if err != nil {
		return nil, apperr.Internal("list notification deliveries failed", err)
	}
	return items, nil
}
