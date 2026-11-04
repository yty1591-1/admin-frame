// Package rest 提供通知域 HTTP Handler（A 端管理 + B 端查询）。
//
// transport 只依赖 app / domain / shared 契约，不 import platform（LAYER-01）；
// 隔离约束：B 端强制使用 Principal.CurrentTenant 过滤（T-22），A 端跨租户
// 查询由权限点与审计约束。
package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	sharedhttpx "github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// HandlerConfig 是 Handler 装配参数。
type HandlerConfig struct {
	// Queries 通知查询用例，必填。
	Queries *app.QueryService
	// Templates 模板用例；A 端模板管理必填，B 端可为空。
	Templates *app.TemplateService
	// Replay 失败补偿用例；A 端必填，B 端可为空。
	Replay *app.ReplayService
	// Send 代发用例；A 端必填，B 端可为空。
	Send *app.SendService
	// Channel 端别（admin / biz），用于校验 Principal 一致（SEC-14）。
	Channel string
}

// Handler 处理通知查询与管理请求。
type Handler struct {
	queries   *app.QueryService
	templates *app.TemplateService
	replay    *app.ReplayService
	send      *app.SendService
	channel   string
}

// NewHandler 构造通知 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Queries == nil {
		return nil, apperr.Internal("notification handler requires a query service", nil)
	}
	channel := strings.TrimSpace(cfg.Channel)
	if channel == "" {
		return nil, apperr.Internal("notification handler requires a channel", nil)
	}
	if channel == "admin" && cfg.Templates == nil {
		// 模板管理是 A 端核心能力；Send / Replay 在方法级判空（最小部署
		// 可能只开查询，方法返回明确的服务不可用而非构造失败）。
		return nil, apperr.Internal("admin notification handler requires a template service", nil)
	}
	return &Handler{
		queries:   cfg.Queries,
		templates: cfg.Templates,
		replay:    cfg.Replay,
		send:      cfg.Send,
		channel:   channel,
	}, nil
}

// tenantScope 返回本次请求允许查询的租户：B 端强制 CurrentTenant（T-22），
// A 端按查询参数（未传取平台哨兵——平台级通知运维视图）。
func (h *Handler) tenantScope(r *http.Request) (string, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return "", apperr.Unauthorized("authentication required")
	}
	if string(principal.Channel) != h.channel {
		return "", apperr.Unauthorized("channel mismatch")
	}
	if h.channel == "biz" {
		tenant := principal.CurrentTenant()
		if strings.TrimSpace(tenant) == "" {
			return "", apperr.Forbidden("no active tenant in token")
		}
		return tenant, nil
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenant == "" {
		tenant = domain.PlatformTenantID
	}
	return tenant, nil
}

func (h *Handler) actor(r *http.Request) (app.Actor, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return app.Actor{}, apperr.Unauthorized("authentication required")
	}
	if string(principal.Channel) != h.channel {
		return app.Actor{}, apperr.Unauthorized("channel mismatch")
	}
	return app.Actor{
		SubjectID:  principal.SubjectID,
		ActorType:  "user",
		Channel:    string(principal.Channel),
		MerchantID: principal.MerchantID,
		TraceID:    sharedhttpx.TraceIDFromContext(r.Context()),
	}, nil
}

// List 处理 GET /{admin,biz}/v1/notifications。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	tenant, err := h.tenantScope(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.RequestFilter{
		TenantID: tenant,
		Scene:    query.Get("scene"),
		Status:   domain.RequestStatus(strings.TrimSpace(query.Get("status"))),
		Limit:    parseIntDefault(query.Get("limit"), 20),
		Offset:   parseIntDefault(query.Get("offset"), 0),
	}
	items, total, err := h.queries.ListRequests(r.Context(), filter)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	out := make([]notificationResponse, 0, len(items))
	for _, n := range items {
		out = append(out, toNotificationResponse(n))
	}
	sharedhttpx.OK(w, listResponse{Items: out, Total: total, Limit: filter.Limit, Offset: filter.Offset}, traceID)
}

// Get 处理 GET /{admin,biz}/v1/notifications/{id}。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	tenant, err := h.tenantScope(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	n, err := h.queries.GetRequest(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, toNotificationResponse(n), traceID)
}

// Deliveries 处理 GET /{admin,biz}/v1/notifications/{id}/deliveries。
func (h *Handler) Deliveries(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	tenant, err := h.tenantScope(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	items, err := h.queries.ListDeliveries(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	out := make([]deliveryResponse, 0, len(items))
	for _, d := range items {
		out = append(out, toDeliveryResponse(d))
	}
	sharedhttpx.OK(w, listResponse{Items: out, Total: len(out)}, traceID)
}

// Send 处理 POST /admin/v1/notifications（A 端代发）。
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body sendRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	if h.send == nil {
		sharedhttpx.FromError(w, apperr.Internal("send service is not available on this channel", nil), traceID)
		return
	}
	tenant := strings.TrimSpace(body.TenantID)
	if tenant == "" {
		tenant = domain.PlatformTenantID
	}
	requestID, err := h.send.Send(r.Context(), app.SendInput{
		TenantID:    tenant,
		Scene:       body.Scene,
		SceneClass:  body.SceneClass,
		Recipient:   app.Recipient{UserID: body.UserID, Email: body.Email, Phone: body.Phone, PushToken: body.PushToken},
		Channels:    body.Channels,
		TemplateKey: body.TemplateKey,
		Locale:      body.Locale,
		Params:      body.Params,
		Priority:    body.Priority,
		Reference:   body.Reference,
		Actor:       actor,
	})
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.JSON(w, http.StatusAccepted, 0, "accepted", sendResponse{RequestID: requestID}, traceID)
}

// Replay 处理 POST /admin/v1/notifications/{id}/replay（A 端 DLQ 补偿）。
func (h *Handler) Replay(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	if h.replay == nil {
		sharedhttpx.FromError(w, apperr.Internal("replay service is not available on this channel", nil), traceID)
		return
	}
	newID, err := h.replay.Replay(r.Context(), app.ReplayInput{
		RequestID: r.PathValue("id"),
		Actor:     actor,
	})
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.JSON(w, http.StatusAccepted, 0, "accepted", sendResponse{RequestID: newID}, traceID)
}

// ListTemplates 处理 GET /admin/v1/notification-templates?key=...
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		sharedhttpx.FromError(w, apperr.BadRequest("key is required"), traceID)
		return
	}
	items, err := h.templates.List(r.Context(), key)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	out := make([]templateResponse, 0, len(items))
	for _, t := range items {
		out = append(out, toTemplateResponse(t))
	}
	sharedhttpx.OK(w, listResponse{Items: out, Total: len(out)}, traceID)
}

// UpsertTemplate 处理 POST /admin/v1/notification-templates。
func (h *Handler) UpsertTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body templateUpsertRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	saved, err := h.templates.Upsert(r.Context(), app.UpsertTemplateInput{
		Key:     body.Key,
		Channel: domain.Channel(body.Channel),
		Locale:  body.Locale,
		Subject: body.Subject,
		Body:    body.Body,
		Actor:   actor,
	})
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.JSON(w, http.StatusCreated, 0, "ok", toTemplateResponse(saved), traceID)
}

// SetTemplateEnabled 处理 PUT /admin/v1/notification-templates/status。
func (h *Handler) SetTemplateEnabled(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body templateStatusRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	if err := h.templates.SetEnabled(r.Context(), body.Key, domain.Channel(body.Channel),
		body.Locale, body.Enabled, actor); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, map[string]string{"status": "ok"}, traceID)
}

// decodeJSON 解析必需请求体：空 body 视为错误。
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return apperr.BadRequest("request body is required")
	}
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return apperr.BadRequest("invalid request body")
	}
	return nil
}

func parseIntDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return def
	}
	return n
}

// ---- 响应结构 ----

type notificationResponse struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	Scene          string   `json:"scene"`
	RecipientRef   string   `json:"recipient_ref"`
	IdempotencyKey string   `json:"idempotency_key"`
	Channels       []string `json:"channels"`
	TemplateKey    string   `json:"template_key"`
	Locale         string   `json:"locale"`
	Priority       string   `json:"priority"`
	Status         string   `json:"status"`
	RejectReason   string   `json:"reject_reason,omitempty"`
	Version        int64    `json:"version"`
	Actor          string   `json:"actor"`
	Reference      string   `json:"reference,omitempty"`
	ExpiresAt      string   `json:"expires_at"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func toNotificationResponse(n domain.Notification) notificationResponse {
	channels := make([]string, 0, len(n.Channels))
	for _, c := range n.Channels {
		channels = append(channels, string(c))
	}
	return notificationResponse{
		ID:             n.ID,
		TenantID:       n.TenantID,
		Scene:          n.Scene,
		RecipientRef:   n.RecipientRef,
		IdempotencyKey: n.IdempotencyKey,
		Channels:       channels,
		TemplateKey:    n.TemplateKey,
		Locale:         n.Locale,
		Priority:       string(n.Priority),
		Status:         string(n.Status),
		RejectReason:   n.RejectReason,
		Version:        n.Version,
		Actor:          n.Actor,
		Reference:      n.Reference,
		ExpiresAt:      n.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt:      n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      n.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type deliveryResponse struct {
	ID           string `json:"id"`
	RequestID    string `json:"request_id"`
	Channel      string `json:"channel"`
	Provider     string `json:"provider,omitempty"`
	Status       string `json:"status"`
	Attempt      int    `json:"attempt"`
	SemanticCode string `json:"semantic_code,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func toDeliveryResponse(d domain.Delivery) deliveryResponse {
	resp := deliveryResponse{
		ID:           d.ID,
		RequestID:    d.RequestID,
		Channel:      string(d.Channel),
		Provider:     d.Provider,
		Status:       string(d.Status),
		Attempt:      d.Attempt,
		SemanticCode: d.SemanticCode,
		ProviderCode: d.ProviderCode,
		MessageID:    d.MessageID,
		CreatedAt:    d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if d.FinishedAt != nil {
		resp.FinishedAt = d.FinishedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

type templateResponse struct {
	Key       string `json:"key"`
	Channel   string `json:"channel"`
	Locale    string `json:"locale"`
	Version   int64  `json:"version"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	Enabled   bool   `json:"enabled"`
	CreatedBy string `json:"created_by"`
	UpdatedAt string `json:"updated_at"`
}

func toTemplateResponse(t domain.Template) templateResponse {
	return templateResponse{
		Key:       t.Key,
		Channel:   string(t.Channel),
		Locale:    t.Locale,
		Version:   t.Version,
		Subject:   t.Subject,
		Body:      t.Body,
		Enabled:   t.Enabled,
		CreatedBy: t.CreatedBy,
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type listResponse struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type sendRequest struct {
	TenantID    string            `json:"tenant_id,omitempty"`
	Scene       string            `json:"scene"`
	SceneClass  string            `json:"scene_class,omitempty"`
	UserID      string            `json:"user_id,omitempty"`
	Email       string            `json:"email,omitempty"`
	Phone       string            `json:"phone,omitempty"`
	PushToken   string            `json:"push_token,omitempty"`
	Channels    []string          `json:"channels"`
	TemplateKey string            `json:"template_key"`
	Locale      string            `json:"locale,omitempty"`
	Params      map[string]string `json:"params"`
	Priority    string            `json:"priority,omitempty"`
	Reference   string            `json:"reference,omitempty"`
}

type sendResponse struct {
	RequestID string `json:"request_id"`
}

type templateUpsertRequest struct {
	Key     string `json:"key"`
	Channel string `json:"channel"`
	Locale  string `json:"locale,omitempty"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

type templateStatusRequest struct {
	Key     string `json:"key"`
	Channel string `json:"channel"`
	Locale  string `json:"locale,omitempty"`
	Enabled bool   `json:"enabled"`
}
