// Package rest 提供租户管理的 HTTP 传输实现（/admin/v1/tenants）。
//
// 只做参数解析、用例调用与响应封装；状态机与审计在 domain / app，
// 本包不得出现「某状态能否迁移」的判断（否则规则会分裂成两份）。
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// maxBodyBytes 管理类请求体上限：租户只有名称与原因，4KB 足够。
const maxBodyBytes = 4 << 10

// HandlerConfig 是租户 Handler 的装配参数。
type HandlerConfig struct {
	// Service 租户用例，必填。
	Service *app.Service
	// Members 成员用例，必填。
	Members *app.MemberService
	// Quotas 配额用例，必填。
	Quotas *app.QuotaService
	// PageLimits 与平台分页配置一致。
	PageLimits pagination.Config
	// Channel 端别，用于校验 Principal 与处理器一致（SEC-14）。
	Channel string
}

// Handler 处理租户管理请求。
type Handler struct {
	service *app.Service
	members *app.MemberService
	quotas  *app.QuotaService
	pageCfg pagination.Config
	channel string
}

// NewHandler 构造租户 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Service == nil {
		return nil, apperr.Internal("tenant handler requires a service", nil)
	}
	if cfg.Members == nil {
		return nil, apperr.Internal("tenant handler requires a member service", nil)
	}
	if cfg.Quotas == nil {
		return nil, apperr.Internal("tenant handler requires a quota service", nil)
	}
	if strings.TrimSpace(cfg.Channel) == "" {
		return nil, apperr.Internal("tenant handler requires a channel", nil)
	}
	return &Handler{
		service: cfg.Service,
		members: cfg.Members,
		quotas:  cfg.Quotas,
		pageCfg: cfg.PageLimits,
		channel: cfg.Channel,
	}, nil
}

// Create 处理 POST /admin/v1/tenants。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var body createRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	tenant, err := h.service.Create(r.Context(), app.CreateInput{
		Name:           body.Name,
		PlanCode:       body.PlanCode,
		OwnerSubjectID: body.OwnerSubjectID,
		Actor:          actor,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.JSON(w, http.StatusCreated, 0, "ok", toResponse(tenant), traceID)
}

// List 处理 GET /admin/v1/tenants。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	result, err := h.service.List(r.Context(), domain.Filter{
		Status:  domain.Status(strings.TrimSpace(query.Get("status"))),
		Keyword: query.Get("keyword"),
		Limit:   page.Limit(),
		Offset:  page.Offset(),
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]tenantResponse, 0, len(result.Items))
	for _, tenant := range result.Items {
		items = append(items, toResponse(tenant))
	}
	httpx.OK(w, listResponse{
		Items:    items,
		Total:    result.Total,
		Page:     page.Page,
		PageSize: page.PageSize,
	}, traceID)
}

// Get 处理 GET /admin/v1/tenants/{id}。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	tenant, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toResponse(tenant), traceID)
}

// Activate 处理 POST /admin/v1/tenants/{id}/activate。
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, domain.StatusActive)
}

// Suspend 处理 POST /admin/v1/tenants/{id}/suspend（必须给原因）。
func (h *Handler) Suspend(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, domain.StatusSuspended)
}

// Resume 处理 POST /admin/v1/tenants/{id}/resume。
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, domain.StatusActive)
}

// Terminate 处理 POST /admin/v1/tenants/{id}/terminate（终态，必须给原因）。
func (h *Handler) Terminate(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, domain.StatusTerminated)
}

// transition 是四个迁移动作的共同实现：解析可选原因并调用用例。
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, target domain.Status) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	// 原因可选请求体：无 body 视为空原因，由状态机决定是否接受。
	var body transitionRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	tenant, err := h.service.Transition(r.Context(), app.TransitionInput{
		ID:     r.PathValue("id"),
		Target: target,
		Reason: body.Reason,
		Actor:  actor,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toResponse(tenant), traceID)
}

// actor 从 Principal 构造操作者，并校验端别一致。
func (h *Handler) actor(r *http.Request) (app.Actor, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return app.Actor{}, apperr.Unauthorized("authentication required")
	}
	if string(principal.Channel) != h.channel {
		return app.Actor{}, apperr.Unauthorized("channel mismatch")
	}
	return app.Actor{
		SubjectID: principal.SubjectID,
		// A 端管理接口的主体恒为人类用户；服务账号若接入需在 Principal 扩展主体类型，
		// 此处不猜测，避免把机器身份记成用户。
		ActorType:  "user",
		Channel:    string(principal.Channel),
		MerchantID: principal.MerchantID,
		TraceID:    httpx.TraceIDFromContext(r.Context()),
	}, nil
}

// decodeJSON 解析必需请求体：空 body 视为错误。
func decodeJSON(r *http.Request, dst any) error {
	return decodeBody(r, dst, true)
}

// decodeOptionalJSON 解析可选请求体：空 body 视为零值。
func decodeOptionalJSON(r *http.Request, dst any) error {
	return decodeBody(r, dst, false)
}

// decodeBody 统一处理大小限制、未知字段与尾随垃圾。
func decodeBody(r *http.Request, dst any, required bool) error {
	if r.Body == nil {
		if required {
			return errors.New("request body is required")
		}
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dst)
	if errors.Is(err, io.EOF) {
		if required {
			return errors.New("request body is required")
		}
		return nil
	}
	if err != nil {
		return errors.New("invalid JSON body")
	}
	if decoder.More() {
		return errors.New("unexpected trailing data in body")
	}
	return nil
}

// createRequest 是创建租户请求体。
type createRequest struct {
	// Name 租户名称。
	Name string `json:"name"`
	// PlanCode 套餐编码；空使用默认套餐（T-24）。
	PlanCode string `json:"plan_code"`
	// OwnerSubjectID 首名 owner 主体 ID；可选，开通前必须补齐（T-12）。
	OwnerSubjectID string `json:"owner_subject_id"`
}

// transitionRequest 是状态迁移请求体。
type transitionRequest struct {
	// Reason 迁移原因；停用与终止必填。
	Reason string `json:"reason"`
}

// tenantResponse 是租户响应体。
type tenantResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	PlanID       string  `json:"plan_id,omitempty"`
	StatusReason string  `json:"status_reason,omitempty"`
	Version      int64   `json:"version"`
	SuspendedAt  *string `json:"suspended_at"`
	TerminatedAt *string `json:"terminated_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// listResponse 是租户列表响应体。
type listResponse struct {
	Items    []tenantResponse `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// toResponse 把领域实体转为响应体；时间统一 RFC3339（UTC）。
func toResponse(tenant domain.Tenant) tenantResponse {
	return tenantResponse{
		ID:           tenant.ID,
		Name:         tenant.Name,
		Status:       string(tenant.Status),
		PlanID:       tenant.PlanID,
		StatusReason: tenant.StatusReason,
		Version:      tenant.Version,
		SuspendedAt:  formatTimePtr(tenant.SuspendedAt),
		TerminatedAt: formatTimePtr(tenant.TerminatedAt),
		CreatedAt:    formatTime(tenant.CreatedAt),
		UpdatedAt:    formatTime(tenant.UpdatedAt),
	}
}

// formatTime 输出 UTC RFC3339；零值输出空串而不是 0001-01-01。
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// formatTimePtr 输出可空时间。
func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatTime(*value)
	return &formatted
}
