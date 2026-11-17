package rest

import (
	"context"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

type secretResponse struct {
	ID                 int64   `json:"id"`
	Env                string  `json:"env"`
	Namespace          string  `json:"namespace"`
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	Owner              string  `json:"owner"`
	Status             string  `json:"status"`
	ActiveVersion      *int64  `json:"active_version"`
	Version            int64   `json:"version"`
	Fingerprint        string  `json:"fingerprint"`
	RotateIntervalDays int     `json:"rotate_interval_days"`
	DualVersion        bool    `json:"dual_version"`
	PendingAction      string  `json:"pending_action,omitempty"`
	SubmittedBy        string  `json:"submitted_by,omitempty"`
	ExpiresAt          *string `json:"expires_at"`
	CreatedBy          string  `json:"created_by"`
	UpdatedBy          string  `json:"updated_by"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

type secretListResponse struct {
	Items    []secretResponse `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

func toSecretView(s domain.Secret) secretResponse {
	return secretResponse{
		ID: s.ID, Env: s.Env, Namespace: s.Namespace, Name: s.Name,
		Description: s.Description, Owner: s.Owner, Status: string(s.Status),
		ActiveVersion: s.ActiveVersion, Version: s.Version, Fingerprint: s.Fingerprint,
		RotateIntervalDays: s.RotateIntervalDays, DualVersion: s.DualVersion,
		PendingAction: s.PendingAction, SubmittedBy: s.SubmittedBy,
		ExpiresAt: formatTimePtr(s.ExpiresAt),
		CreatedBy: s.CreatedBy, UpdatedBy: s.UpdatedBy,
		CreatedAt: formatTime(s.CreatedAt), UpdatedAt: formatTime(s.UpdatedAt),
	}
}

// ListSecrets 处理 GET /admin/v1/configs/secrets。
func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	items, total, err := h.secrets.List(r.Context(),
		strings.TrimSpace(query.Get("env")),
		strings.TrimSpace(query.Get("namespace")),
		domain.SecretStatus(strings.TrimSpace(query.Get("status"))),
		page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]secretResponse, 0, len(items))
	for _, item := range items {
		views = append(views, toSecretView(item))
	}
	apiresp.OK(w, secretListResponse{
		Items: views, Total: total, Page: page.Page, PageSize: page.PageSize,
	}, traceID)
}

// CreateSecret 处理 POST /admin/v1/configs/secrets。
func (h *Handler) CreateSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Env                string     `json:"env"`
		Namespace          string     `json:"namespace"`
		Name               string     `json:"name"`
		Description        string     `json:"description"`
		Owner              string     `json:"owner"`
		Plaintext          string     `json:"plaintext"`
		DualVersion        bool       `json:"dual_version"`
		RotateIntervalDays int        `json:"rotate_interval_days"`
		ExpiresAt          *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = h.secrets.CreateSecret(ctx, app.CreateSecretInput{
			Env: body.Env, Namespace: body.Namespace, Name: body.Name,
			Description: body.Description, Owner: body.Owner, Plaintext: body.Plaintext,
			DualVersion: body.DualVersion, RotateIntervalDays: body.RotateIntervalDays,
			ExpiresAt: body.ExpiresAt, Actor: a,
		})
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

// GetSecret 处理 GET /admin/v1/configs/secrets/{id}。
func (h *Handler) GetSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	secret, err := h.secrets.Get(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

// SubmitSecret 处理 POST .../submit。
func (h *Handler) SubmitSecret(w http.ResponseWriter, r *http.Request) {
	h.mutateSecret(w, r, func(ctx context.Context, id int64, a app.Actor) (domain.Secret, error) {
		return h.secrets.SubmitSecret(ctx, id, a)
	})
}

// ApproveSecret 处理 POST .../approve。
func (h *Handler) ApproveSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = h.secrets.ApproveSecret(ctx, app.ApproveSecretInput{
			ID: id, Decision: domain.ReviewDecision(body.Decision), Comment: body.Comment, Actor: a,
		})
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

// RotateSecret 处理 POST .../rotate。
func (h *Handler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Plaintext string `json:"plaintext"`
		Comment   string `json:"comment"`
	}
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = h.secrets.RotateSecret(ctx, app.RotateSecretInput{
			ID: id, Plaintext: body.Plaintext, Comment: body.Comment, Actor: a,
		})
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

// RevokeSecret 处理 POST .../revoke。
func (h *Handler) RevokeSecret(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Comment string `json:"comment"`
	}
	_ = decodeOptionalJSON(r, &body)
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = h.secrets.RevokeSecret(ctx, id, body.Comment, a)
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

type secretVersionResponse struct {
	Version     int64   `json:"version"`
	Fingerprint string  `json:"fingerprint"`
	Status      string  `json:"status"`
	Algorithm   string  `json:"algorithm"`
	CreatedBy   string  `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	RevokedAt   *string `json:"revoked_at"`
}

// ListSecretVersions 处理 GET .../versions。
func (h *Handler) ListSecretVersions(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items, total, err := h.secrets.ListVersions(r.Context(), id, page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]secretVersionResponse, 0, len(items))
	for _, item := range items {
		views = append(views, secretVersionResponse{
			Version: item.Version, Fingerprint: item.ValueHash, Status: string(item.Status),
			Algorithm: item.Algorithm, CreatedBy: item.CreatedBy, CreatedAt: formatTime(item.CreatedAt),
			RevokedAt: formatTimePtr(item.RevokedAt),
		})
	}
	apiresp.OK(w, map[string]any{"items": views, "total": total, "page": page.Page, "page_size": page.PageSize}, traceID)
}

type activateSecretVersionRequest struct {
	BreakGlass       bool   `json:"break_glass"`
	BreakGlassReason string `json:"break_glass_reason"`
}

// ActivateSecretVersion 处理 POST .../versions/{version}/activate。
func (h *Handler) ActivateSecretVersion(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := parsePositive(r.PathValue("version"), "version")
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body activateSecretVersionRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = h.secrets.ActivateVersion(ctx, app.ActivateVersionInput{
			SecretID: id, Version: version,
			BreakGlass: body.BreakGlass, BreakGlassReason: body.BreakGlassReason, Actor: a,
		})
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}

type secretGrantResponse struct {
	ID              int64   `json:"id"`
	Env             string  `json:"env"`
	Namespace       string  `json:"namespace"`
	ServiceIdentity string  `json:"service_identity"`
	Permission      string  `json:"permission"`
	ExpiresAt       *string `json:"expires_at"`
	CreatedBy       string  `json:"created_by"`
	CreatedAt       string  `json:"created_at"`
	RevokedAt       *string `json:"revoked_at"`
}

func toSecretGrantView(g domain.SecretGrant) secretGrantResponse {
	return secretGrantResponse{
		ID: g.ID, Env: g.Env, Namespace: g.Namespace, ServiceIdentity: g.ServiceIdentity,
		Permission: g.Permission, ExpiresAt: formatTimePtr(g.ExpiresAt),
		CreatedBy: g.CreatedBy, CreatedAt: formatTime(g.CreatedAt), RevokedAt: formatTimePtr(g.RevokedAt),
	}
}

// ListSecretGrants 处理 GET .../grants。
func (h *Handler) ListSecretGrants(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items, err := h.secrets.ListGrants(r.Context(), id)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]secretGrantResponse, 0, len(items))
	for _, item := range items {
		views = append(views, toSecretGrantView(item))
	}
	apiresp.OK(w, map[string]any{"items": views, "total": len(views)}, traceID)
}

// CreateSecretGrant 处理 POST .../grants。
func (h *Handler) CreateSecretGrant(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		ServiceIdentity string     `json:"service_identity"`
		ExpiresAt       *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	var grant domain.SecretGrant
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		grant, cerr = h.secrets.CreateGrant(ctx, app.CreateGrantInput{
			SecretID: id, ServiceIdentity: body.ServiceIdentity, ExpiresAt: body.ExpiresAt, Actor: a,
		})
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretGrantView(grant), traceID)
}

// RevokeSecretGrant 处理 POST .../grants/{grant_id}/revoke。
func (h *Handler) RevokeSecretGrant(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	grantID, err := parsePositive(r.PathValue("grant_id"), "grant_id")
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var grant domain.SecretGrant
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		grant, cerr = h.secrets.RevokeGrant(ctx, id, grantID, a)
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretGrantView(grant), traceID)
}

// ListSecretAccesses 处理 GET .../accesses。
func (h *Handler) ListSecretAccesses(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items, total, err := h.secrets.ListAccesses(r.Context(), id, page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]map[string]any, 0, len(items))
	for _, item := range items {
		views = append(views, map[string]any{
			"occurred_at": formatTime(item.OccurredAt), "service_identity": item.ServiceIdentity,
			"secret_name": item.SecretName, "secret_version": item.SecretVersion,
			"result": item.Result, "trace_id": item.TraceID,
		})
	}
	apiresp.OK(w, map[string]any{"items": views, "total": total, "page": page.Page, "page_size": page.PageSize}, traceID)
}

// ResolveSecrets 处理 POST /internal/v1/configs/secrets/resolve。
func (h *Handler) ResolveSecrets(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	identity, err := resolveServiceIdentity(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Env       string   `json:"env"`
		Namespace string   `json:"namespace"`
		Names     []string `json:"names"`
	}
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	// 与配置 pull 同一套 namespace allowlist（按部署 env 判严）。
	if err := authorizeNamespace(h.deployEnv, identity, body.Namespace); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items, err := h.secrets.Resolve(r.Context(), app.ResolveInput{
		Env: body.Env, Namespace: body.Namespace, Names: body.Names,
		ServiceIdentity: identity, TraceID: traceID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	views := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"name": item.Name, "version": item.Version, "value": item.Value,
			"lease_expires_at": formatTime(item.LeaseExpiresAt),
		}
		if item.GraceVersion != nil {
			row["grace_version"] = *item.GraceVersion
			row["grace_value"] = item.GraceValue
		}
		views = append(views, row)
	}
	apiresp.OK(w, map[string]any{"items": views}, traceID)
}

// resolveServiceIdentity 按 Q11：优先证书 SAN；mTLS=require 时禁止裸 header。
func resolveServiceIdentity(r *http.Request) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CONFIG_API_MTLS")))
	if mode == "" {
		mode = "off"
	}
	headerID := strings.TrimSpace(r.Header.Get("X-Service-Identity"))
	certID := ""
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		certID = identityFromCertificate(r.TLS.PeerCertificates[0])
	}
	switch mode {
	case "require":
		if certID == "" {
			return "", apperr.Forbidden("client certificate required")
		}
		return certID, nil
	case "optional":
		if certID != "" {
			return certID, nil
		}
		if headerID == "" {
			return "", apperr.Forbidden("service identity required")
		}
		return headerID, nil
	default: // off：本机 / 演练允许 header
		if headerID != "" {
			return headerID, nil
		}
		if certID != "" {
			return certID, nil
		}
		return "", apperr.Forbidden("service identity required")
	}
}

func identityFromCertificate(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	for _, uri := range cert.URIs {
		if uri != nil && uri.String() != "" {
			return uri.String()
		}
	}
	for _, dns := range cert.DNSNames {
		if strings.TrimSpace(dns) != "" {
			return dns
		}
	}
	return strings.TrimSpace(cert.Subject.CommonName)
}

func (h *Handler) mutateSecret(w http.ResponseWriter, r *http.Request,
	fn func(ctx context.Context, id int64, a app.Actor) (domain.Secret, error)) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	a, err := actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var secret domain.Secret
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		var cerr error
		secret, cerr = fn(ctx, id, a)
		return cerr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSecretView(secret), traceID)
}
