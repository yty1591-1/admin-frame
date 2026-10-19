package c

import (
	"encoding/json"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/domain"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// Handler 处理 C 端订单 HTTP 请求（用户归属 + 字段裁剪）。
type Handler struct {
	svc *app.OrderService
}

// NewHandler 构造 C 端订单 Handler。
func NewHandler(svc *app.OrderService) *Handler {
	return &Handler{svc: svc}
}

// Create 创建订单；必须携带 Idempotency-Key（API-11）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())

	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		httpx.FromError(w, apperr.BadRequest("Idempotency-Key required"), traceID)
		return
	}

	var req CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.FromError(w, apperr.BadRequest("invalid json"), traceID)
		return
	}
	o, err := h.svc.CreateOrder(r.Context(), app.CreateOrderCommand{
		MerchantID: req.MerchantID,
		UserID:     p.SubjectID,
		AmountCent: req.AmountCent,
		Currency:   req.Currency,
		IdemKey:    idemKey,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toDTO(o), traceID)
}

// List 列举当前用户订单（响应已裁剪内部字段）。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	q, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items, err := h.svc.ListOrders(r.Context(), domain.ListFilter{
		UserID: p.SubjectID,
		Limit:  q.Limit(),
		Offset: q.Offset(),
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

// Get 获取当前用户订单；越权表现为 NotFound。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	o, err := h.svc.GetOrderForUser(r.Context(), r.PathValue("id"), p.SubjectID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toDTO(o), traceID)
}

// Cancel 取消当前用户订单。
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	p := auth.MustFromContext(r.Context())
	o, err := h.svc.CancelOrder(r.Context(), app.CancelOrderCommand{
		OrderID: r.PathValue("id"),
		UserID:  p.SubjectID,
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
		AmountCent: o.AmountCent,
		Currency:   o.Currency,
		Status:     string(o.Status),
		CreatedAt:  o.CreatedAt,
	}
}
