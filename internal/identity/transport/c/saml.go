// Package c 提供 SAML 2.0 SP 的 HTTP 端点：授权发起、ACS、一次性票据兑换与 SP 元数据。
package c

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// samlACSMaxBodyBytes 是 ACS 表单上限；SAMLResponse 远大于普通认证请求体。
const samlACSMaxBodyBytes = 512 << 10

// SAMLAuthorize POST /c/v1/auth/saml/{provider}/authorize
//
// 生成 AuthnRequest 与一次性 state，返回 IdP 跳转地址；provider 未配置返回 40400。
func (h *Handler) SAMLAuthorize(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	provider := strings.TrimSpace(r.PathValue("provider"))
	if h.saml == nil {
		httpx.FromError(w, apperr.Unavailable("saml login is not enabled"), traceID)
		return
	}
	if !h.saml.HasProvider(provider) {
		httpx.FromError(w, apperr.NotFound("saml provider is not configured"), traceID)
		return
	}
	result, err := h.saml.AuthorizeWithClient(r.Context(), provider, h.clientIPHash(r), traceID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, oauthAuthorizeResponse{AuthorizeURL: result.AuthorizeURL, State: result.State}, traceID)
}

// SAMLACS POST /c/v1/auth/saml/acs
//
// IdP 以浏览器表单 POST 回传 SAMLResponse。校验通过后签发一次性 ticket，
// 并以 303 跳回前端；令牌只在 exchange 端点返回，避免出现在浏览器地址栏。
func (h *Handler) SAMLACS(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.saml == nil {
		httpx.FromError(w, apperr.Unavailable("saml login is not enabled"), traceID)
		return
	}
	if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil ||
		contentType != "application/x-www-form-urlencoded" {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, samlACSMaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid form body", http.StatusBadRequest)
		return
	}
	ticket, err := h.saml.ACS(r.Context(), app.SAMLACSInput{
		SAMLResponse: r.PostFormValue("SAMLResponse"),
		RelayState:   r.PostFormValue("RelayState"),
		IPHash:       h.clientIPHash(r),
		UAHash:       h.clientUAHash(r),
		Device:       h.clientDevice(r),
		TraceID:      traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	target, err := url.Parse(strings.TrimSpace(h.samlRedirectURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		httpx.FromError(w, apperr.Unavailable("saml login redirect is not configured"), traceID)
		return
	}
	query := target.Query()
	query.Set("ticket", ticket)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

// SAMLExchange POST /c/v1/auth/saml/exchange
//
// 用 ACS 下发的一次性 ticket 换取令牌；ticket 过期、已消费或不存在统一 40100。
func (h *Handler) SAMLExchange(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.saml == nil {
		httpx.FromError(w, apperr.Unavailable("saml login is not enabled"), traceID)
		return
	}
	var req samlExchangeRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	result, err := h.saml.Exchange(r.Context(), req.Ticket, h.clientDevice(r))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// SAMLMetadata GET /c/v1/auth/saml/metadata
//
// 返回本系统 SP 元数据，供 IdP 侧登记 ACS 与签名/加密证书。
func (h *Handler) SAMLMetadata(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.saml == nil {
		httpx.FromError(w, apperr.Unavailable("saml login is not enabled"), traceID)
		return
	}
	raw, err := h.saml.SPMetadata()
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// samlExchangeRequest 是 ticket 兑换请求。
type samlExchangeRequest struct {
	Ticket string `json:"ticket"`
}
