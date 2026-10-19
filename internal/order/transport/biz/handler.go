package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// Handler 处理 B 端订单 HTTP 请求（商户隔离）。
type Handler struct {
	svc *app.OrderService
}

// NewHandler 构造 B 端订单 Handler。
func NewHandler(svc *app.OrderService) *Handler {
	return &Handler{svc: svc}
}

// List 列举当前商户订单。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	if p.MerchantID == "" {
		httpx.FromError(w, apperr.Forbidden("merchant context required"), traceID)
		return
	}
	q, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items, err := h.svc.ListOrders(r.Context(), domain.ListFilter{
		MerchantID: p.MerchantID,
		Limit:      q.Limit(),
		Offset:     q.Offset(),
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	dtos := make([]*OrderDTO, 0, len(items))
	for _, o := range items {
		dtos = append(dtos, toDTO(o))
	}
	httpx.OK(w, ListResponse{Items: dtos, Total: len(dtos)}, traceID)
}

// Get 获取本商户订单；越权表现为 NotFound。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	o, err := h.svc.GetOrderForMerchant(r.Context(), r.PathValue("id"), p.MerchantID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toDTO(o), traceID)
}

// Cancel 取消本商户订单。
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	o, err := h.svc.CancelOrder(r.Context(), app.CancelOrderCommand{
		OrderID:    r.PathValue("id"),
		MerchantID: p.MerchantID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toDTO(o), traceID)
}

func toDTO(o *domain.Order) *OrderDTO {
	return &OrderDTO{
		ID:         o.ID,
		MerchantID: o.MerchantID,
		UserID:     o.UserID,
		AmountCent: o.AmountCent,
		Currency:   o.Currency,
		Status:     string(o.Status),
		CreatedAt:  o.CreatedAt,
		UpdatedAt:  o.UpdatedAt,
	}
}
