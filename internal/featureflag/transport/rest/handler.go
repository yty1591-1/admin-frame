// Package rest 是 featureflag 的 HTTP Handler（仅 A 端管理面）。
//
// B 端 / C 端不提供任何 flag 端点（不泄露功能面，规划 §2.F）；
// 评估走进程内 Evaluator 注入，不经 HTTP。
package rest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/app"
	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	sharedhttpx "github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// Handler 是 flag 管理 Handler。
type Handler struct {
	flags *app.FlagService
}

// HandlerConfig 构造配置。
type HandlerConfig struct {
	// Flags 管理用例；必填。
	Flags *app.FlagService
}

// NewHandler 构造 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Flags == nil {
		return nil, apperr.Internal("featureflag handler requires a flag service", nil)
	}
	return &Handler{flags: cfg.Flags}, nil
}

// flagResponse 是 flag 视图；rules 原样输出（无 PII）。
type flagResponse struct {
	Key            string     `json:"key"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	DefaultEnabled bool       `json:"default_enabled"`
	Rules          []ruleView `json:"rules"`
	Permanent      bool       `json:"permanent"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Version        int64      `json:"version"`
	CreatedBy      string     `json:"created_by"`
	// EffectiveWithin 提示变更生效上限（秒），管理端可见（§2.C）。
	EffectiveWithin int `json:"effective_within_seconds"`
}

type ruleView struct {
	Channel    string   `json:"channel,omitempty"`
	TenantIDs  []string `json:"tenant_ids,omitempty"`
	Percentage int      `json:"percentage,omitempty"`
}

type listResponse struct {
	Items  []flagResponse `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// toView 聚合 → 视图。
func toView(f domain.Flag) flagResponse {
	out := flagResponse{
		Key:             f.Key,
		Description:     f.Description,
		Status:          string(f.Status),
		DefaultEnabled:  f.DefaultEnabled,
		Permanent:       f.Permanent,
		Version:         f.Version,
		CreatedBy:       f.CreatedBy,
		EffectiveWithin: 30,
	}
	if !f.Permanent && !f.ExpiresAt.IsZero() {
		expiry := f.ExpiresAt
		out.ExpiresAt = &expiry
	}
	out.Rules = make([]ruleView, 0, len(f.Rules))
	for _, r := range f.Rules {
		out.Rules = append(out.Rules, ruleView{
			Channel: r.Channel, TenantIDs: r.TenantIDs, Percentage: r.Percentage,
		})
	}
	return out
}

// actor 从请求构造审计主体。
func actor(r *http.Request) (app.Actor, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return app.Actor{}, apperr.Unauthorized("authentication required")
	}
	return app.Actor{
		SubjectID:  principal.SubjectID,
		ActorType:  "user",
		Channel:    string(principal.Channel),
		MerchantID: principal.MerchantID,
		TraceID:    sharedhttpx.TraceIDFromContext(r.Context()),
	}, nil
}

// List 处理 GET /admin/v1/feature-flags?state=&limit=&offset=。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	query := r.URL.Query()
	limit := 20
	if v, err := strconv.Atoi(query.Get("limit")); err == nil && v > 0 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(query.Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	flags, total, err := h.flags.List(r.Context(), strings.TrimSpace(query.Get("state")), limit, offset)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	items := make([]flagResponse, 0, len(flags))
	for _, f := range flags {
		items = append(items, toView(f))
	}
	sharedhttpx.OK(w, listResponse{Items: items, Total: total, Limit: limit, Offset: offset}, traceID)
}

// Get 处理 GET /admin/v1/feature-flags/{key}。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	flag, err := h.flags.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, toView(flag), traceID)
}

type createRequest struct {
	Key            string     `json:"key"`
	Description    string     `json:"description"`
	Rules          []ruleView `json:"rules"`
	DefaultEnabled bool       `json:"default_enabled"`
	Permanent      bool       `json:"permanent"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

// Create 处理 POST /admin/v1/feature-flags。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body createRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	rules, err := rulesFromView(body.Rules)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var expiresAt time.Time
	if body.ExpiresAt != nil {
		expiresAt = *body.ExpiresAt
	}
	flag, err := h.flags.Create(r.Context(), app.CreateInput{
		Key: body.Key, Description: body.Description, Rules: rules,
		DefaultEnabled: body.DefaultEnabled, Permanent: body.Permanent,
		ExpiresAt: expiresAt, Actor: actor,
	})
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.JSON(w, http.StatusCreated, 0, "created", toView(flag), traceID)
}

type updateRequest struct {
	Description     *string     `json:"description,omitempty"`
	Rules           *[]ruleView `json:"rules,omitempty"`
	DefaultEnabled  *bool       `json:"default_enabled,omitempty"`
	ExpiresAt       *time.Time  `json:"expires_at,omitempty"`
	ExpectedVersion int64       `json:"expected_version"`
}

// Update 处理 PATCH /admin/v1/feature-flags/{key}。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body updateRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	in := app.UpdateInput{Key: r.PathValue("key"), ExpectedVersion: body.ExpectedVersion, Actor: actor}
	if body.Description != nil {
		in.Description = body.Description
	}
	if body.Rules != nil {
		rules, rerr := rulesFromView(*body.Rules)
		if rerr != nil {
			sharedhttpx.FromError(w, rerr, traceID)
			return
		}
		in.Rules = &rules
	}
	if body.DefaultEnabled != nil {
		in.DefaultEnabled = body.DefaultEnabled
	}
	if body.ExpiresAt != nil {
		in.ExpiresAt = body.ExpiresAt
	}
	flag, err := h.flags.Update(r.Context(), in)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, toView(flag), traceID)
}

type statusRequest struct {
	Target          string `json:"target"`
	ExpectedVersion int64  `json:"expected_version"`
}

// SetStatus 处理 POST /admin/v1/feature-flags/{key}/status（kill switch 入口）。
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	var body statusRequest
	if err := decodeJSON(r, &body); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	target := domain.FlagStatus(strings.TrimSpace(body.Target))
	if target != domain.StatusEnabled && target != domain.StatusDisabled {
		sharedhttpx.FromError(w, apperr.BadRequest("target must be enabled or disabled"), traceID)
		return
	}
	flag, err := h.flags.SetStatus(r.Context(), r.PathValue("key"), target, body.ExpectedVersion, actor)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, toView(flag), traceID)
}

// Delete 处理 DELETE /admin/v1/feature-flags/{key}。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	traceID := sharedhttpx.TraceIDFromContext(r.Context())
	actor, err := actor(r)
	if err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	if err := h.flags.Delete(r.Context(), r.PathValue("key"), actor); err != nil {
		sharedhttpx.FromError(w, err, traceID)
		return
	}
	sharedhttpx.OK(w, map[string]string{"status": "deleted"}, traceID)
}

// rulesFromView 视图 → 领域规则。
func rulesFromView(views []ruleView) ([]domain.Rule, error) {
	rules := make([]domain.Rule, 0, len(views))
	for _, v := range views {
		rule := domain.Rule{Channel: strings.TrimSpace(v.Channel), Percentage: v.Percentage}
		if len(v.TenantIDs) > 0 {
			ids := make([]string, 0, len(v.TenantIDs))
			for _, id := range v.TenantIDs {
				if strings.TrimSpace(id) == "" {
					return nil, apperr.BadRequest("tenant_ids must not contain empty entries")
				}
				ids = append(ids, strings.TrimSpace(id))
			}
			rule.TenantIDs = ids
		}
		if err := rule.Validate(); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
		rules = append(rules, rule)
	}
	return rules, nil
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
