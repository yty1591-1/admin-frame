package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// CertificationCampaignDTO 是访问复核活动响应。
type CertificationCampaignDTO struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Channel        string     `json:"channel"`
	MerchantID     string     `json:"merchant_id"`
	RoleIDs        []string   `json:"role_ids"`
	PrivilegedOnly bool       `json:"privileged_only"`
	Status         string     `json:"status"`
	DueAt          time.Time  `json:"due_at"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	Version        int64      `json:"version"`
	TaskCount      int        `json:"task_count"`
}

// CertificationTaskDTO 是复核任务响应。
type CertificationTaskDTO struct {
	ID                 string     `json:"id"`
	CampaignID         string     `json:"campaign_id"`
	SubjectType        string     `json:"subject_type"`
	SubjectID          string     `json:"subject_id"`
	MerchantID         string     `json:"merchant_id"`
	RoleID             string     `json:"role_id"`
	BindingID          string     `json:"binding_id"`
	BindingFingerprint string     `json:"binding_fingerprint"`
	ReviewerID         string     `json:"reviewer_id"`
	Status             string     `json:"status"`
	Decision           string     `json:"decision,omitempty"`
	Reason             string     `json:"reason,omitempty"`
	DecidedBy          string     `json:"decided_by,omitempty"`
	DecidedAt          *time.Time `json:"decided_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	Version            int64      `json:"version"`
}

// CertificationCampaignListResponse 是活动分页响应。
type CertificationCampaignListResponse struct {
	Items []*CertificationCampaignDTO `json:"items"`
	Total int                         `json:"total"`
}

// CertificationTaskListResponse 是任务分页响应。
type CertificationTaskListResponse struct {
	Items []*CertificationTaskDTO `json:"items"`
	Total int                     `json:"total"`
}

// CreateCertificationCampaignRequest 是创建活动请求。
type CreateCertificationCampaignRequest struct {
	Channel        string    `json:"channel"`
	Name           string    `json:"name"`
	RoleIDs        []string  `json:"role_ids"`
	PrivilegedOnly bool      `json:"privileged_only"`
	DueAt          time.Time `json:"due_at"`
	Reason         string    `json:"reason"`
}

// DecideCertificationTaskRequest 是裁决任务请求；reason 由领域层强制。
type DecideCertificationTaskRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// CloseCertificationCampaignRequest 是关闭活动请求。
type CloseCertificationCampaignRequest struct {
	Reason string `json:"reason"`
}

// CreateCertificationCampaign POST /iam/certifications
func (h *Handler) CreateCertificationCampaign(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.certification == nil {
		httpx.FromError(w, apperr.Internal("iam certification service is not configured", nil), traceID)
		return
	}
	var req CreateCertificationCampaignRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	channel := h.effectiveChannel(req.Channel)
	if channel == "" {
		httpx.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	campaign, err := h.certification.CreateCampaign(r.Context(), app.CreateCertificationCampaignCommand{
		Channel:        channel,
		MerchantID:     h.merchantScope(principal),
		Name:           req.Name,
		RoleIDs:        req.RoleIDs,
		PrivilegedOnly: req.PrivilegedOnly,
		DueAt:          req.DueAt,
		Actor:          principal.SubjectID,
		Reason:         req.Reason,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toCertificationCampaignDTO(campaign), traceID)
}

// ActivateCertificationCampaign POST /iam/certifications/{id}/activate
func (h *Handler) ActivateCertificationCampaign(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	campaign, err := h.certification.ActivateCampaign(r.Context(), r.PathValue("id"), scope, principal.SubjectID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toCertificationCampaignDTO(campaign), traceID)
}

// ListCertificationCampaigns GET /iam/certifications
func (h *Handler) ListCertificationCampaigns(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	_, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.CertificationCampaignFilter{
		Status: domain.CertificationStatus(strings.TrimSpace(query.Get("status"))),
		Limit:  page.Limit(),
		Offset: page.Offset(),
	}
	campaigns, err := h.certification.ListCampaigns(r.Context(), scope, filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	total, err := h.certification.CountCampaigns(r.Context(), scope, filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*CertificationCampaignDTO, 0, len(campaigns))
	for _, campaign := range campaigns {
		items = append(items, toCertificationCampaignDTO(campaign))
	}
	httpx.OK(w, CertificationCampaignListResponse{Items: items, Total: total}, traceID)
}

// GetCertificationCampaign GET /iam/certifications/{id}
func (h *Handler) GetCertificationCampaign(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	_, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	campaign, err := h.certification.GetCampaign(r.Context(), r.PathValue("id"), scope)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toCertificationCampaignDTO(campaign), traceID)
}

// ListCertificationTasks GET /iam/certifications/{id}/tasks
func (h *Handler) ListCertificationTasks(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	_, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	filter := domain.CertificationTaskFilter{
		CampaignID: r.PathValue("id"),
		Status:     domain.CertificationTaskStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		Limit:      page.Limit(),
		Offset:     page.Offset(),
	}
	tasks, total, err := h.certification.ListTasks(r.Context(), r.PathValue("id"), scope, filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*CertificationTaskDTO, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, toCertificationTaskDTO(task))
	}
	httpx.OK(w, CertificationTaskListResponse{Items: items, Total: total}, traceID)
}

// DecideCertificationTask POST /iam/certifications/{id}/tasks/{taskId}/decision
func (h *Handler) DecideCertificationTask(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req DecideCertificationTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	task, err := h.certification.DecideTask(
		r.Context(), r.PathValue("id"), r.PathValue("taskId"),
		domain.CertificationDecision(strings.TrimSpace(req.Decision)),
		req.Reason, principal.SubjectID, scope,
	)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toCertificationTaskDTO(task), traceID)
}

// CloseCertificationCampaign POST /iam/certifications/{id}/close
func (h *Handler) CloseCertificationCampaign(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req CloseCertificationCampaignRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	campaign, err := h.certification.CloseCampaign(
		r.Context(), r.PathValue("id"), scope, principal.SubjectID, req.Reason,
	)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toCertificationCampaignDTO(campaign), traceID)
}

// AutoExpireCertificationCampaign POST /iam/certifications/{id}/auto-expire
func (h *Handler) AutoExpireCertificationCampaign(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	_, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	affected, err := h.certification.AutoExpireOverdue(r.Context(), r.PathValue("id"), scope)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, map[string]int{"affected": affected}, traceID)
}

// ExportCertificationEvidence GET /iam/certifications/{id}/export?format=csv|json
//
// 导出是批量数据外发：除 iam:certification:read 外，路由层仍由鉴权中间件
// 校验权限；此处只负责稳定列序与 Content-Type，不做业务裁剪。
func (h *Handler) ExportCertificationEvidence(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.certificationScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	payload, contentType, err := h.certification.ExportEvidence(
		r.Context(), r.PathValue("id"), scope, format, principal.SubjectID,
	)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if format == "" {
		format = "csv"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		`attachment; filename="certification-`+r.PathValue("id")+`.`+strings.ToLower(format)+`"`)
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: payload 是服务端生成的 CSV/JSON 导出内容，Content-Type 固定且以附件下载，不经 HTML 渲染。
	_, _ = w.Write(payload)
}

// certificationScope 从认证主体派生复核作用域；商户永远来自 Principal。
func (h *Handler) certificationScope(r *http.Request) (auth.Principal, app.CertificationScope, error) {
	principal, err := h.principal(r)
	if err != nil {
		return auth.Principal{}, app.CertificationScope{}, err
	}
	scope := app.CertificationScope{Channel: h.channel, MerchantID: h.merchantScope(principal)}
	if err := scope.Validate(); err != nil {
		return auth.Principal{}, app.CertificationScope{}, err
	}
	return principal, scope, nil
}

// toCertificationCampaignDTO 转换活动响应。
func toCertificationCampaignDTO(campaign *domain.CertificationCampaign) *CertificationCampaignDTO {
	roleIDs := make([]string, len(campaign.RoleIDs))
	copy(roleIDs, campaign.RoleIDs)
	return &CertificationCampaignDTO{
		ID:             campaign.ID,
		Name:           campaign.Name,
		Channel:        string(campaign.Channel),
		MerchantID:     campaign.MerchantID,
		RoleIDs:        roleIDs,
		PrivilegedOnly: campaign.PrivilegedOnly,
		Status:         string(campaign.Status),
		DueAt:          campaign.DueAt.UTC(),
		CreatedBy:      campaign.CreatedBy,
		CreatedAt:      campaign.CreatedAt.UTC(),
		ClosedAt:       campaign.ClosedAt,
		Version:        campaign.Version,
		TaskCount:      campaign.TaskCount,
	}
}

// toCertificationTaskDTO 转换任务响应。
func toCertificationTaskDTO(task *domain.CertificationTask) *CertificationTaskDTO {
	return &CertificationTaskDTO{
		ID:                 task.ID,
		CampaignID:         task.CampaignID,
		SubjectType:        string(task.SubjectType),
		SubjectID:          task.SubjectID,
		MerchantID:         task.MerchantID,
		RoleID:             task.RoleID,
		BindingID:          task.BindingID,
		BindingFingerprint: task.BindingFingerprint,
		ReviewerID:         task.ReviewerID,
		Status:             string(task.Status),
		Decision:           string(task.Decision),
		Reason:             task.Reason,
		DecidedBy:          task.DecidedBy,
		DecidedAt:          task.DecidedAt,
		CreatedAt:          task.CreatedAt.UTC(),
		Version:            task.Version,
	}
}
