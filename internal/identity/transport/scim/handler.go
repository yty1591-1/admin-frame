package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// HandlerConfig 是 SCIM 传输层装配参数。
type HandlerConfig struct {
	// Service SCIM 用例；必填。
	Service *app.ScimService
	// Limiter 端点限流器（单令牌 10rps，叠加单 IP 20rps）；nil 时不限流。
	Limiter app.LoginLimiter
	// ClientIP 可信代理解析器；nil 表示只使用 RemoteAddr。
	ClientIP *pkghttpx.ClientIPResolver
	// Hasher IP 摘要器；nil 时回退 SHA-256。
	Hasher *crypto.Hasher
	// MaxPayload 单请求体上限（字节）；≤0 使用 1MiB。
	MaxPayload int64
	// Timeout 单请求处理超时；≤0 使用 10s。
	Timeout time.Duration
}

// Handler 承载 /admin/scim/v2/* 的协议处理。
type Handler struct {
	service    *app.ScimService
	limiter    app.LoginLimiter
	ipResolver *pkghttpx.ClientIPResolver
	hasher     *crypto.Hasher
	maxPayload int64
	timeout    time.Duration
}

// NewHandler 构造 SCIM Handler；缺少用例时返回 nil（路由不注册）。
func NewHandler(cfg HandlerConfig) *Handler {
	if cfg.Service == nil {
		return nil
	}
	handler := &Handler{
		service:    cfg.Service,
		limiter:    cfg.Limiter,
		ipResolver: cfg.ClientIP,
		hasher:     cfg.Hasher,
		maxPayload: cfg.MaxPayload,
		timeout:    cfg.Timeout,
	}
	if handler.maxPayload <= 0 {
		handler.maxPayload = 1 << 20
	}
	if handler.timeout <= 0 {
		handler.timeout = 10 * time.Second
	}
	return handler
}

// principalKey 是上下文中的 SCIM 令牌主体键。
type principalKey struct{}

// wrap 组合认证、限流、超时、链路头与观测，返回可直接挂到 mux 的处理器。
func (h *Handler) wrap(resource, method string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
		defer cancel()
		r = r.WithContext(ctx)
		// SCIM 响应体是协议例外，但链路 ID 仍经响应头回传（§21.1.4）。
		w.Header().Set("X-Trace-Id", apiresp.TraceIDFromContext(ctx))
		w.Header().Set("Cache-Control", "no-store")

		principal, err := h.authenticate(r)
		if err != nil {
			h.observe(ctx, resource, method, http.StatusUnauthorized, "auth", time.Now())
			writeError(w, err)
			return
		}
		if retryAfter, limited := h.checkLimit(r, principal); limited {
			w.Header().Set("Retry-After", retryAfterHeader(retryAfter))
			h.observe(ctx, resource, method, http.StatusTooManyRequests, "rate_limited", time.Now())
			writeError(w, &app.ScimError{
				Status: http.StatusTooManyRequests, Detail: "request rate limit exceeded",
			})
			return
		}
		r = r.WithContext(context.WithValue(ctx, principalKey{}, principal))
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next(recorder, r)
		h.observe(ctx, resource, method, recorder.status, "", started)
	})
}

// authenticate 校验 Bearer SCIM 令牌；失败统一 401。
func (h *Handler) authenticate(r *http.Request) (*app.ScimPrincipal, error) {
	raw := bearerToken(r.Header.Get("Authorization"))
	if raw == "" {
		return nil, &app.ScimError{Status: http.StatusUnauthorized, Detail: "bearer token is required"}
	}
	principal, err := h.service.AuthenticateToken(r.Context(), raw)
	if err != nil {
		var scimErr *app.ScimError
		if errors.As(err, &scimErr) {
			return nil, scimErr
		}
		return nil, &app.ScimError{Status: http.StatusServiceUnavailable, Detail: "scim authentication is unavailable"}
	}
	return principal, nil
}

// checkLimit 执行单令牌 + 单 IP 双维度限流；命中任一维度即拒绝。
func (h *Handler) checkLimit(r *http.Request, principal *app.ScimPrincipal) (time.Duration, bool) {
	if h.limiter == nil {
		return 0, false
	}
	keys := make([]string, 0, 2)
	if principal != nil && principal.TokenID != "" {
		keys = append(keys, "scim:token:"+principal.TokenID)
	}
	if ip := h.clientIPHash(r); ip != "" {
		keys = append(keys, "scim:ip:"+ip)
	}
	if len(keys) == 0 {
		keys = append(keys, "scim:global")
	}
	var (
		retryAfter time.Duration
		denied     bool
	)
	for _, key := range keys {
		allowed, after := h.limiter.Allow(key)
		if allowed {
			continue
		}
		denied = true
		if after > retryAfter {
			retryAfter = after
		}
	}
	return retryAfter, denied
}

// clientIPHash 返回客户端 IP 摘要；与业务端使用同一摘要域，避免明文 IP 落库。
func (h *Handler) clientIPHash(r *http.Request) string {
	ip := h.ipResolver.ClientIP(r)
	if ip == "" {
		ip = compactRemoteAddr(r.RemoteAddr)
	}
	return app.ClientRef(h.hasher, app.HashDomainClientIP, ip)
}

// observe 上报一次请求的观测数据；成功时刷新最后成功时间。
func (h *Handler) observe(ctx context.Context, resource, method string, status int, reason string, started time.Time) {
	observer := h.observer()
	if observer == nil {
		return
	}
	result := "success"
	if status >= http.StatusBadRequest {
		result = "error"
		if reason == "" {
			reason = strconv.Itoa(status)
		}
	}
	observer.ObserveScimRequest(ctx, resource, method, result, reason, time.Since(started))
}

// observer 返回观测端口；用例未注入时返回 nil。
func (h *Handler) observer() app.ScimObserver {
	if h == nil {
		return nil
	}
	return h.service.Observer()
}

// statusRecorder 捕获响应状态码，供观测使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 记录状态码后交给底层 ResponseWriter。
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// bearerToken 从 Authorization 头解析 Bearer 令牌。
func bearerToken(header string) string {
	value := strings.TrimSpace(header)
	if len(value) < 7 || !strings.EqualFold(value[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

// compactRemoteAddr 去掉端口，保留 IP。
func compactRemoteAddr(remote string) string {
	trimmed := strings.TrimSpace(remote)
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	return trimmed
}

// retryAfterHeader 把等待时长转换为 Retry-After 秒数（向上取整，至少 1s）。
func retryAfterHeader(d time.Duration) string {
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

// writeJSON 写出 SCIM 响应；Content-Type 固定为 application/scim+json。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeSCIM)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// 响应已开始写出，无法再改状态码；记录到服务端日志由上层观测兜底。
		return
	}
}

// writeError 写出 RFC 7644 §3.12 协议错误体。
func writeError(w http.ResponseWriter, err error) {
	scimErr := normalizeError(err)
	if scimErr.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="scim"`)
	}
	writeJSON(w, scimErr.Status, errorResponse{
		Schemas:  []string{schemaError},
		Status:   strconv.Itoa(scimErr.Status),
		ScimType: scimErr.ScimType,
		Detail:   scimErr.Detail,
	})
}

// normalizeError 把用例错误映射为 SCIM 协议错误。
//
// 业务码映射见 §21.1.4：40000→400 / 40100→401 / 40300→403 / 40400→404 /
// 40900→409 uniqueness / 42900→429 / 5xxxx→503 temporarilyUnavailable。
func normalizeError(err error) *app.ScimError {
	if err == nil {
		return &app.ScimError{Status: http.StatusInternalServerError, Detail: "internal error"}
	}
	var scimErr *app.ScimError
	if errors.As(err, &scimErr) {
		return scimErr
	}
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case apperr.CodeBadRequest:
			return &app.ScimError{Status: http.StatusBadRequest, ScimType: "invalidValue", Detail: appErr.Message}
		case apperr.CodeUnauthorized:
			return &app.ScimError{Status: http.StatusUnauthorized, Detail: appErr.Message}
		case apperr.CodeForbidden:
			return &app.ScimError{Status: http.StatusForbidden, Detail: appErr.Message}
		case apperr.CodeNotFound:
			return &app.ScimError{Status: http.StatusNotFound, Detail: appErr.Message}
		case apperr.CodeConflict:
			return &app.ScimError{Status: http.StatusConflict, ScimType: "uniqueness", Detail: appErr.Message}
		case apperr.CodeTooManyRequests:
			return &app.ScimError{Status: http.StatusTooManyRequests, Detail: appErr.Message}
		default:
			return &app.ScimError{Status: http.StatusServiceUnavailable, Detail: "service temporarily unavailable"}
		}
	}
	return &app.ScimError{Status: http.StatusServiceUnavailable, Detail: "service temporarily unavailable"}
}

// decodeBody 解码请求体并施加大小上限；超限返回 400 invalidSyntax。
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	if contentType := mediaType(r.Header.Get("Content-Type")); contentType != "" &&
		contentType != contentTypeSCIM && contentType != contentTypeJSON {
		return &app.ScimError{
			Status: http.StatusUnsupportedMediaType,
			Detail: "Content-Type must be application/scim+json or application/json",
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxPayload)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &app.ScimError{Status: http.StatusBadRequest, ScimType: "invalidSyntax", Detail: "request body is too large"}
		}
		return &app.ScimError{Status: http.StatusBadRequest, ScimType: "invalidSyntax", Detail: "request body is not valid JSON"}
	}
	return nil
}

// mediaType 去掉 Content-Type 的参数部分并转小写。
func mediaType(value string) string {
	trimmed := strings.TrimSpace(value)
	if idx := strings.IndexByte(trimmed, ';'); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return strings.ToLower(strings.TrimSpace(trimmed))
}

// parseETag 解析 If-Match 中的弱 ETag（W/"<version>"）；缺失或非法返回 nil。
func parseETag(header string) *int64 {
	value := strings.TrimSpace(header)
	if value == "" {
		return nil
	}
	value = strings.TrimPrefix(value, "W/")
	value = strings.Trim(value, `"`)
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return nil
	}
	return &version
}

// locationOf 拼接资源绝对路径；请求无 Host 时返回相对路径。
func locationOf(r *http.Request, resource, id string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" &&
		(forwarded == "http" || forwarded == "https") {
		scheme = forwarded
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "/admin/scim/v2/" + resource + "/" + id
	}
	return scheme + "://" + host + "/admin/scim/v2/" + resource + "/" + id
}

// parsePositiveInt 解析正整数查询参数；非法或缺失返回 fallback。
func parsePositiveInt(value string, fallback int) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback
	}
	return parsed
}
