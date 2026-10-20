package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// Handler 处理 A 端订单 HTTP 请求。
type Handler struct {
	svc *app.OrderService
}

// NewHandler 构造 A 端订单 Handler。
func NewHandler(svc *app.OrderService) *Handler {
	return &Handler{svc: svc}
}

// List 列举订单，支持 merchant_id / 分页查询参数。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	q, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	filter := domain.ListFilter{
		MerchantID: r.URL.Query().Get("merchant_id"),
		Limit:      q.Limit(),
		Offset:     q.Offset(),
	}
	items, err := h.svc.ListOrders(r.Context(), filter)
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

// Get 按 ID 获取订单（含内部字段）。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	id := r.PathValue("id")
	o, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toDTO(o), traceID)
}

// Cancel 取消订单（管理端，不强制商户/用户归属）。
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	id := r.PathValue("id")
	o, err := h.svc.CancelOrder(r.Context(), app.CancelOrderCommand{OrderID: id})
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
		Remark:     o.Remark,
		CostCent:   o.CostCent,
		CreatedAt:  o.CreatedAt,
		UpdatedAt:  o.UpdatedAt,
	}
}
