package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// EventPublisher 领域事件发布端口（worker 可订阅）。
type EventPublisher interface {
	// Publish 发布零个或多个领域事件。
	Publish(ctx context.Context, events ...domain.Event) error
}

type noopPublisher struct{}

// Publish 空实现，丢弃全部事件。
func (noopPublisher) Publish(context.Context, ...domain.Event) error { return nil }

// OrderService 应用层用例。
type OrderService struct {
	repo      domain.Repository
	publisher EventPublisher
	ids       id.Generator

	mu       sync.Mutex
	idemKeys map[string]string // idempotency key -> order id
}

// NewOrderService 构造订单应用服务；publisher 为 nil 时使用空实现。
// 默认使用 Snowflake（NodeID=1）；多实例部署须注入互不冲突的 Generator。
func NewOrderService(repo domain.Repository, publisher EventPublisher) *OrderService {
	return NewOrderServiceWithID(repo, publisher, nil)
}

// NewOrderServiceWithID 同 NewOrderService，并允许注入分布式 ID 生成器。
// ids 为 nil 时使用 NodeID=1 的 Snowflake（仅适合单实例/本地）。
func NewOrderServiceWithID(repo domain.Repository, publisher EventPublisher, ids id.Generator) *OrderService {
	if publisher == nil {
		publisher = noopPublisher{}
	}
	if ids == nil {
		ids = id.MustSnowflake(id.Options{NodeID: 1})
	}
	return &OrderService{
		repo:      repo,
		publisher: publisher,
		ids:       ids,
		idemKeys:  make(map[string]string),
	}
}

// CreateOrder 创建订单；IdemKey 非空时按键幂等返回已有订单。
func (s *OrderService) CreateOrder(ctx context.Context, cmd CreateOrderCommand) (*domain.Order, error) {
	if cmd.IdemKey != "" {
		s.mu.Lock()
		if existingID, ok := s.idemKeys[cmd.IdemKey]; ok {
			s.mu.Unlock()
			return s.repo.FindByID(ctx, existingID)
		}
		s.mu.Unlock()
	}

	raw, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate id", err)
	}
	orderID := fmt.Sprintf("ord_%s", raw)
	order, err := domain.NewOrder(orderID, cmd.MerchantID, cmd.UserID, cmd.AmountCent, cmd.Currency)
	if err != nil {
		return nil, err
	}
	// 演示：内部字段仅 A 可见
	order.CostCent = order.AmountCent * 70 / 100
	order.Remark = "internal"

	if err := s.repo.Save(ctx, order); err != nil {
		return nil, err
	}
	if cmd.IdemKey != "" {
		s.mu.Lock()
		s.idemKeys[cmd.IdemKey] = order.ID
		s.mu.Unlock()
	}
	// 演示内存总线：发布失败不回滚已落库订单，生产应走 Outbox。
	_ = s.publisher.Publish(ctx, domain.OrderCreated{
		OrderID:    order.ID,
		MerchantID: order.MerchantID,
		UserID:     order.UserID,
		At:         time.Now().UTC(),
	})
	return order, nil
}

// GetOrder 按 ID 获取订单（无端别隔离，供 A 端）。
func (s *OrderService) GetOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return order, nil
}

// GetOrderForMerchant B 端：强制商户隔离。
func (s *OrderService) GetOrderForMerchant(ctx context.Context, orderID, merchantID string) (*domain.Order, error) {
	if merchantID == "" {
		return nil, apperr.Forbidden("merchant_id required")
	}
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.MerchantID != merchantID {
		return nil, apperr.NotFound("order not found")
	}
	return order, nil
}

// GetOrderForUser C 端：强制用户归属。
func (s *OrderService) GetOrderForUser(ctx context.Context, orderID, userID string) (*domain.Order, error) {
	if userID == "" {
		return nil, apperr.Forbidden("user_id required")
	}
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, apperr.NotFound("order not found")
	}
	return order, nil
}

// ListOrders 分页列举订单；Limit 非法时回落到 20，超过 100 返回参数错误。
func (s *OrderService) ListOrders(ctx context.Context, filter domain.ListFilter) ([]*domain.Order, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		return nil, apperr.BadRequest("page_size exceeds maximum of 100")
	}
	return s.repo.List(ctx, filter)
}

// CancelOrder 按端别隔离规则加载并取消订单。
func (s *OrderService) CancelOrder(ctx context.Context, cmd CancelOrderCommand) (*domain.Order, error) {
	var order *domain.Order
	var err error
	switch {
	case cmd.MerchantID != "":
		order, err = s.GetOrderForMerchant(ctx, cmd.OrderID, cmd.MerchantID)
	case cmd.UserID != "":
		order, err = s.GetOrderForUser(ctx, cmd.OrderID, cmd.UserID)
	default:
		order, err = s.GetOrder(ctx, cmd.OrderID)
	}
	if err != nil {
		return nil, err
	}
	if err := order.Cancel(); err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, order); err != nil {
		return nil, err
	}
	// 同上：演示场景忽略发布错误。
	_ = s.publisher.Publish(ctx, domain.OrderCancelled{
		OrderID:    order.ID,
		MerchantID: order.MerchantID,
		UserID:     order.UserID,
		At:         time.Now().UTC(),
	})
	return order, nil
}
