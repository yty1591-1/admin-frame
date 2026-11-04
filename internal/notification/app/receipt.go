package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// ReceiptService 是渠道回执处理用例（ENT-10 NT-07）：供应商回执回调
// 把「渠道侧受理」推进为「送达终态」。
//
// 安全三件套（规划 §2.H）：验签（transport 层用 pkg/webhook 完成后才进本
// 用例）、幂等（重复回执对已终态明细直接吸收）、状态机（delivery/请求
// 均单调推进，回执不能把 delivered 打回 sent）。
type ReceiptService struct {
	deliveries domain.DeliveryRepository
	requests   domain.RequestRepository
	observer   Observer
	now        func() time.Time
}

// NewReceiptService 构造回执用例。
func NewReceiptService(requests domain.RequestRepository, deliveries domain.DeliveryRepository,
	opts Options) (*ReceiptService, error) {
	switch {
	case requests == nil:
		return nil, apperr.Internal("notification receipt service requires a request repository", nil)
	case deliveries == nil:
		return nil, apperr.Internal("notification receipt service requires a delivery repository", nil)
	}
	return &ReceiptService{
		deliveries: deliveries,
		requests:   requests,
		observer:   observerOrNoop(opts.Observer),
		now:        time.Now,
	}, nil
}

// ReceiptInput 是一条回执的归一化输入（transport 层已验签并解析供应商格式）。
type ReceiptInput struct {
	// Channel 回执渠道。
	Channel domain.Channel
	// MessageID 渠道消息 ID（投递时的回执对账键）。
	MessageID string
	// Delivered true=送达确认；false=送达失败 / 超时。
	Delivered bool
	// ProviderCode 供应商原始状态码（仅排障）。
	ProviderCode string
	// Summary 脱敏失败摘要（Delivered=false 时建议填写）。
	Summary string
}

// Apply 应用回执：更新明细终态并收敛通知单状态。
//
// 幂等语义：
//   - 明细不存在（消息 ID 未知）→ ErrDeliveryNotFound（transport 记日志即可，
//     对供应商仍回成功，避免重放风暴）；
//   - 明细已终态 → 吸收（重复回执是供应商语义的常态）。
//
// 通知单收敛：任一渠道 delivered → 通知单 delivered（单调不回退）；
// undelivered 只在通知单尚未 delivered 时落定。
func (s *ReceiptService) Apply(ctx context.Context, in ReceiptInput) error {
	if !in.Channel.Implemented() {
		return apperr.BadRequest("unsupported receipt channel: " + string(in.Channel))
	}
	if strings.TrimSpace(in.MessageID) == "" {
		return apperr.BadRequest("message id is required")
	}
	d, err := s.deliveries.FindByMessageID(ctx, in.Channel, in.MessageID)
	if err != nil {
		if errors.Is(err, domain.ErrDeliveryNotFound) {
			return apperr.NotFound("delivery not found for message " + in.MessageID)
		}
		return apperr.Internal("load delivery by message id failed", err)
	}

	target := domain.DeliveryDelivered
	if !in.Delivered {
		target = domain.DeliveryUndelivered
	}
	next, err := d.Transition(target, s.now())
	if err != nil {
		if errors.Is(err, domain.ErrDeliveryTerminal) {
			return nil // 重复回执：幂等吸收。
		}
		return apperr.Internal("apply receipt to delivery failed", err)
	}
	next.ProviderCode = strings.TrimSpace(in.ProviderCode)
	if in.Summary != "" {
		if annotated, ferr := next.WithFailure(d.SemanticCode, in.ProviderCode, in.Summary); ferr == nil {
			next.LastError = annotated.LastError
		}
	}
	if err := s.deliveries.Update(ctx, next); err != nil {
		return apperr.Internal("persist receipt delivery failed", err)
	}
	s.observer.DeliveryObserved(d.Channel, d.Provider, string(target), "")

	return s.convergeRequest(ctx, d.RequestID)
}

// convergeRequest 回执驱动的通知单收敛（sent → delivered / undelivered）。
func (s *ReceiptService) convergeRequest(ctx context.Context, requestID string) error {
	n, err := s.requests.Get(ctx, requestID)
	if err != nil {
		return apperr.Internal("reload notification for receipt failed", err)
	}
	deliveries, err := s.deliveries.ListByRequest(ctx, requestID)
	if err != nil {
		return apperr.Internal("list deliveries for receipt failed", err)
	}
	anyDelivered, anyUndelivered := false, false
	for _, d := range deliveries {
		switch d.Status {
		case domain.DeliveryDelivered:
			anyDelivered = true
		case domain.DeliveryUndelivered:
			anyUndelivered = true
		}
	}
	target := domain.RequestStatus("")
	switch {
	case anyDelivered:
		target = domain.RequestDelivered
	case anyUndelivered && n.Status == domain.RequestSent:
		// 全渠道已受理但存在送达失败：undelivered 只从 sent 落定，
		// 避免后续其他渠道的送达确认把终态打回。
		target = domain.RequestUndelivered
	default:
		return nil
	}
	next, err := n.Transition(target, "", s.now())
	if err != nil {
		// 状态机拒绝（已终态 / 非法回退）是正常的：回执乱序到达。
		return nil
	}
	if err := s.requests.UpdateStatus(ctx, next); err != nil && !errors.Is(err, domain.ErrVersionConflict) {
		return apperr.Internal("persist receipt convergence failed", err)
	}
	s.observer.RequestObserved(n.Scene, next.Status)
	return nil
}
