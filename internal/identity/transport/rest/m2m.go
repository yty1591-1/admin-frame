package rest

// 本文件实现 M2M 机器身份的两组 HTTP 端点（docs/identity-authn.md §21.2.2 / §21.2.4）：
//   - POST /{端}/v1/auth/token           协议端点，错误体按 RFC 6749 §5.2；
//   - POST /admin/v1/auth/m2m/clients... 管理端点，沿用统一信封 {code,message,trace_id}。
//
// 两组端点共用同一用例服务，差异只在错误体与鉴权方式。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// m2mTokenFormLimit 是令牌端点表单请求体上限；scope 列表远小于该值。
const m2mTokenFormLimit = 64 << 10

// m2mMaxPageSize 是客户端列表单页上限。
const m2mMaxPageSize = 100

// rfc6749Error 是 RFC 6749 §5.2 错误响应体。
type rfc6749Error struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// M2MEnabled 报告令牌端点是否已装配；false 时路由不注册（404）。
func (h *Handler) M2MEnabled() bool {
	return h != nil && h.m2m != nil
}

// M2MTokenHandler 返回 POST /{channel}/v1/auth/token 的处理链。
//
// 协议端点必须禁止缓存（响应含令牌），并复用认证路由的独立请求体上限。
// 返回 nil 表示未装配：调用方据此不注册路由，请求落到 mux 的 404 而非 403。
//
// 路由**必须由各端 router 用字面量 path 注册**（"POST /admin/v1/auth/token"），
// 不要在这里根据 h.channel 拼路径——tests/contract 的漂移检测依赖 `mux.Handle`
// 的字面量形态静态扫描，动态拼接会让端点对契约对账不可见。
func M2MTokenHandler(h *Handler) http.Handler {
	if h == nil || !h.M2MEnabled() {
		return nil
	}
	return pkghttpx.NoStoreMiddleware(AuthBodyLimit(http.HandlerFunc(h.M2MToken)))
}

// M2MToken POST /{channel}/v1/auth/token
//
// client_credentials 签发机器令牌。约束：
//   - 只接受 application/x-www-form-urlencoded；
//   - 凭据只来自 HTTP Basic 或表单，禁止 query 传参；
//   - grant_type 必填且只能是 client_credentials；scope 必填，无隐式默认值；
//   - 失败体按 RFC 6749 §5.2，trace_id 经 X-Trace-Id 返回。
func (h *Handler) M2MToken(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	w.Header().Set("X-Trace-Id", traceID)
	w.Header().Set("Cache-Control", "no-store")
	if h.m2m == nil {
		http.NotFound(w, r)
		return
	}
	if !isFormContentType(r.Header.Get("Content-Type")) {
		writeRFC6749Error(w, http.StatusUnsupportedMediaType, "invalid_request",
			"content type must be application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, m2mTokenFormLimit)
	if err := r.ParseForm(); err != nil {
		writeRFC6749Error(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	// 禁止经 query 传参：凭据出现在 URL 会进入访问日志与代理缓存。
	for _, key := range []string{"client_id", "client_secret", "grant_type", "scope"} {
		if _, ok := r.URL.Query()[key]; ok {
			writeRFC6749Error(w, http.StatusBadRequest, "invalid_request", "query parameters are not allowed")
			return
		}
	}
	if grant := strings.TrimSpace(r.PostFormValue("grant_type")); grant != "client_credentials" {
		writeRFC6749Error(w, http.StatusBadRequest, "unsupported_grant_type",
			"only client_credentials is supported")
		return
	}
	clientID, clientSecret, ok := m2mCredentials(r)
	if !ok {
		// Basic 与表单同时出现且不一致：按认证失败处理，避免两套凭据互相覆盖。
		writeRFC6749Error(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	// 限流键优先用 client_id；缺失时回落 IP 摘要，避免匿名请求绕过配额。
	limitKey := clientID
	if limitKey == "" {
		limitKey = h.clientIPHash(r)
	}
	if retryAfter, err := h.checkM2MLimit(r, limitKey); err != nil {
		writeRateLimited(w, retryAfter, traceID)
		return
	}
	result, err := h.m2m.IssueToken(r.Context(), app.IssueTokenInput{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scope:        strings.TrimSpace(r.PostFormValue("scope")),
		TraceID:      traceID,
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrM2MInvalidClient):
			writeRFC6749Error(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		case errors.Is(err, app.ErrM2MInvalidScope):
			writeRFC6749Error(w, http.StatusBadRequest, "invalid_scope", "requested scope is not allowed")
		case errors.Is(err, apperr.ErrUnavailable):
			writeRFC6749Error(w, http.StatusServiceUnavailable, "temporarily_unavailable",
				"authentication dependency unavailable")
		default:
			writeRFC6749Error(w, http.StatusServiceUnavailable, "temporarily_unavailable", "request failed")
		}
		return
	}
	writeJSONBody(w, http.StatusOK, map[string]any{
		"access_token": result.AccessToken,
		"token_type":   "Bearer",
		"expires_in":   result.ExpiresIn,
		"scope":        strings.Join(result.Scopes, " "),
	})
}

// checkM2MLimit 执行 M2M 签发端点限流：按 client_id + 单 IP 双维度。
//
// 未配置专用限流器时回退端点限流器，保证「配置了 m2m 却没有配额」不会被误放行。
func (h *Handler) checkM2MLimit(r *http.Request, clientID string) (time.Duration, error) {
	limiter := h.m2mLimiter
	if limiter == nil {
		limiter = h.limiter
	}
	if limiter == nil {
		return 0, nil
	}
	channel := string(h.channel)
	keys := make([]string, 0, 2)
	if clientID != "" {
		keys = append(keys, channel+":m2m_token:client:"+clientID)
	}
	if ip := h.clientIPHash(r); ip != "" {
		keys = append(keys, channel+":m2m_token:ip:"+ip)
	}
	if len(keys) == 0 {
		keys = append(keys, channel+":m2m_token:global")
	}
	var retryAfter time.Duration
	for _, key := range keys {
		allowed, after := limiter.Allow(key)
		if allowed {
			continue
		}
		if after > retryAfter {
			retryAfter = after
		}
		return retryAfter, apperr.TooManyRequests("m2m token rate limit exceeded")
	}
	return 0, nil
}

// m2mCredentials 解析客户端凭据：Basic 优先，其次表单；两者不一致时 ok=false。
func m2mCredentials(r *http.Request) (clientID, clientSecret string, ok bool) {
	basicID, basicSecret, hasBasic := r.BasicAuth()
	formID := strings.TrimSpace(r.PostFormValue("client_id"))
	formSecret := strings.TrimSpace(r.PostFormValue("client_secret"))
	if hasBasic {
		if (formID != "" && formID != basicID) || (formSecret != "" && formSecret != basicSecret) {
			return "", "", false
		}
		return strings.TrimSpace(basicID), basicSecret, true
	}
	if formID == "" || formSecret == "" {
		return "", "", false
	}
	return formID, formSecret, true
}

// isFormContentType 判断是否为 RFC 6749 要求的表单类型。
func isFormContentType(value string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return mediaType == "application/x-www-form-urlencoded"
}

// writeRFC6749Error 写协议错误体；invalid_client 附带 WWW-Authenticate。
func writeRFC6749Error(w http.ResponseWriter, status int, code, description string) {
	if code == "invalid_client" {
		w.Header().Set("WWW-Authenticate", `Basic realm="m2m"`)
	}
	writeJSONBody(w, status, rfc6749Error{Error: code, ErrorDescription: description})
}

// writeRateLimited 写限流响应；Retry-After 取整秒且至少 1。
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration, traceID string) {
	seconds := int(retryAfter / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	if traceID != "" {
		w.Header().Set("X-Trace-Id", traceID)
	}
	writeJSONBody(w, http.StatusTooManyRequests, rfc6749Error{
		Error:            "temporarily_unavailable",
		ErrorDescription: "rate limit exceeded",
	})
}

// writeJSONBody 写 JSON 响应体。
func writeJSONBody(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// m2mClientDTO 是管理端点返回的客户端视图；不含任何密钥材料。
type m2mClientDTO struct {
	ID              string   `json:"id"`
	ClientID        string   `json:"client_id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	AllowedChannels []string `json:"allowed_channels"`
	AllowedScopes   []string `json:"allowed_scopes"`
	Owner           string   `json:"owner,omitempty"`
	TokenVersion    int64    `json:"token_version"`
	RotationDueAt   string   `json:"rotation_due_at,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	DeletedAt       string   `json:"deleted_at,omitempty"`
}

// toM2MClientDTO 把领域对象转换为响应视图。
func toM2MClientDTO(client *domain.M2MClient) m2mClientDTO {
	channels := make([]string, 0, len(client.AllowedChannels))
	for _, item := range client.AllowedChannels {
		channels = append(channels, string(item))
	}
	dto := m2mClientDTO{
		ID:              client.ID,
		ClientID:        client.ClientID,
		Name:            client.Name,
		Status:          string(client.Status),
		AllowedChannels: channels,
		AllowedScopes:   append([]string{}, client.AllowedScopes...),
		Owner:           client.Owner,
		TokenVersion:    client.TokenVersion,
		CreatedAt:       client.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       client.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if client.RotationDueAt != nil {
		dto.RotationDueAt = client.RotationDueAt.UTC().Format(time.RFC3339)
	}
	if client.DeletedAt != nil {
		dto.DeletedAt = client.DeletedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

// ListM2MClients GET /admin/v1/auth/m2m/clients
func (h *Handler) ListM2MClients(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.m2m == nil {
		apiresp.FromError(w, apperr.NotFound("m2m is not enabled"), traceID)
		return
	}
	query := r.URL.Query()
	page, err := pagination.ParseWith(r, pagination.Config{PageSizeParam: "page_size", MaxPageSize: m2mMaxPageSize})
	if err != nil {
		apiresp.FromError(w, apperr.BadRequest("invalid pagination parameters"), traceID)
		return
	}
	filter := domain.M2MClientListFilter{
		Status: domain.M2MClientStatus(strings.TrimSpace(query.Get("status"))),
		Limit:  page.Limit(),
		Offset: page.Offset(),
	}
	if channel := strings.TrimSpace(query.Get("channel")); channel != "" {
		filter.Channel = domain.Channel(channel)
	}
	clients, total, listErr := h.m2m.ListClients(r.Context(), filter)
	if listErr != nil {
		apiresp.FromError(w, listErr, traceID)
		return
	}
	items := make([]m2mClientDTO, 0, len(clients))
	for _, client := range clients {
		items = append(items, toM2MClientDTO(client))
	}
	apiresp.OK(w, map[string]any{
		"items": items,
		"total": total,
		"page":  page.Page,
		"size":  page.PageSize,
	}, traceID)
}

// m2mCreateClientRequest 是创建客户端的请求体。
type m2mCreateClientRequest struct {
	Name     string   `json:"name"`
	Owner    string   `json:"owner"`
	Channels []string `json:"allowed_channels"`
	Scopes   []string `json:"allowed_scopes"`
}

// CreateM2MClient POST /admin/v1/auth/m2m/clients
//
// client_secret 只在本次响应出现一次，之后不可再取回。
func (h *Handler) CreateM2MClient(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.m2m == nil {
		apiresp.FromError(w, apperr.NotFound("m2m is not enabled"), traceID)
		return
	}
	var req m2mCreateClientRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	channels := make([]domain.Channel, 0, len(req.Channels))
	for _, item := range req.Channels {
		channels = append(channels, domain.Channel(item))
	}
	actorID, _ := auth.FromContext(r.Context())
	created, err := h.m2m.CreateClient(r.Context(), app.CreateM2MClientInput{
		Name:     req.Name,
		Owner:    req.Owner,
		Channels: channels,
		Scopes:   req.Scopes,
		ActorID:  actorID.SubjectID,
		TraceID:  traceID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusCreated, 0, "ok", map[string]any{
		"client":        toM2MClientDTO(created.Client),
		"client_secret": created.ClientSecret,
	}, traceID)
}

// RotateM2MSecret POST /admin/v1/auth/m2m/clients/{id}/secrets
//
// 新 secret 立即生效，旧 secret 进入 rotate_grace 宽限；同时递增 token_version。
func (h *Handler) RotateM2MSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.m2m == nil {
		apiresp.FromError(w, apperr.NotFound("m2m is not enabled"), traceID)
		return
	}
	clientID := r.PathValue("id")
	actorID, _ := auth.FromContext(r.Context())
	rotated, err := h.m2m.RotateSecret(r.Context(), clientID, actorID.SubjectID, traceID)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	payload := map[string]any{
		"client_id":     rotated.ClientID,
		"client_secret": rotated.ClientSecret,
		"fingerprint":   rotated.Fingerprint,
	}
	if !rotated.ExpiresAt.IsZero() {
		payload["retiring_expires_at"] = rotated.ExpiresAt.UTC().Format(time.RFC3339)
	}
	apiresp.OK(w, payload, traceID)
}

// DisableM2MClient POST /admin/v1/auth/m2m/clients/{id}/disable
func (h *Handler) DisableM2MClient(w http.ResponseWriter, r *http.Request) {
	h.m2mLifecycle(w, r, func(clientID, actorID, traceID string) error {
		return h.m2m.DisableClient(r.Context(), clientID, actorID, traceID)
	})
}

// EnableM2MClient POST /admin/v1/auth/m2m/clients/{id}/enable
func (h *Handler) EnableM2MClient(w http.ResponseWriter, r *http.Request) {
	h.m2mLifecycle(w, r, func(clientID, actorID, traceID string) error {
		return h.m2m.EnableClient(r.Context(), clientID, actorID, traceID)
	})
}

// DeleteM2MClient DELETE /admin/v1/auth/m2m/clients/{id}
func (h *Handler) DeleteM2MClient(w http.ResponseWriter, r *http.Request) {
	h.m2mLifecycle(w, r, func(clientID, actorID, traceID string) error {
		return h.m2m.DeleteClient(r.Context(), clientID, actorID, traceID)
	})
}

// m2mLifecycle 统一处理停用 / 启用 / 软删三个无请求体的处置端点。
func (h *Handler) m2mLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	action func(clientID, actorID, traceID string) error,
) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.m2m == nil {
		apiresp.FromError(w, apperr.NotFound("m2m is not enabled"), traceID)
		return
	}
	// 显式丢弃请求体，避免调用方误以为该端点接受参数。
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1))
	actorID, _ := auth.FromContext(r.Context())
	if err := action(r.PathValue("id"), actorID.SubjectID, traceID); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
