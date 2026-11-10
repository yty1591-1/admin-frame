package scim

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// ListUsers GET /admin/scim/v2/Users
//
// 分页：startIndex 从 1 开始，count ∈ [0,100]，必须返回 totalResults。
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter, err := parseUserFilter(query.Get("filter"))
	if err != nil {
		writeError(w, err)
		return
	}
	startIndex, err := parseStartIndex(query.Get("startIndex"))
	if err != nil {
		writeError(w, err)
		return
	}
	count, err := parseCount(query.Get("count"), h.service.PageSize())
	if err != nil {
		writeError(w, err)
		return
	}
	page, listErr := h.service.ListUsers(r.Context(), app.ScimUserListQuery{
		UserName:    filter.UserName,
		ExternalID:  filter.ExternalID,
		DisplayName: filter.DisplayName,
		ID:          filter.ID,
		Active:      filter.Active,
		StartIndex:  startIndex,
		Count:       count,
	})
	if listErr != nil {
		writeError(w, listErr)
		return
	}
	resources := make([]any, 0, len(page.Users))
	for i := range page.Users {
		resources = append(resources, userToResource(&page.Users[i], locationOf(r, "Users", page.Users[i].ID)))
	}
	writeJSON(w, http.StatusOK, listResponse{
		Schemas:      []string{schemaListResponse},
		TotalResults: page.Total,
		StartIndex:   page.StartIndex,
		ItemsPerPage: page.ItemsPerPage,
		Resources:    resources,
	})
}

// GetUser GET /admin/scim/v2/Users/{id}
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	user, err := h.service.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", etagOf(user.Version))
	writeJSON(w, http.StatusOK, userToResource(user, locationOf(r, "Users", user.ID)))
}

// CreateUser POST /admin/scim/v2/Users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var payload userResource
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	if err := rejectPassword(payload.Password); err != nil {
		writeError(w, err)
		return
	}
	if trimSpace(payload.UserName) == "" {
		writeError(w, scimInvalidValue("userName is required"))
		return
	}
	if trimSpace(payload.ExternalID) == "" {
		writeError(w, scimInvalidValue("externalId is required"))
		return
	}
	user, err := h.service.CreateUser(r.Context(), app.ScimUserInput{
		ExternalID:  payload.ExternalID,
		UserName:    payload.UserName,
		DisplayName: displayNameOfPayload(payload),
		Active:      payload.Active,
	}, apirespTraceID(r))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", etagOf(user.Version))
	w.Header().Set("Location", locationOf(r, "Users", user.ID))
	writeJSON(w, http.StatusCreated, userToResource(user, locationOf(r, "Users", user.ID)))
}

// ReplaceUser PUT /admin/scim/v2/Users/{id}
func (h *Handler) ReplaceUser(w http.ResponseWriter, r *http.Request) {
	var payload userResource
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	if err := rejectPassword(payload.Password); err != nil {
		writeError(w, err)
		return
	}
	if trimSpace(payload.UserName) == "" {
		writeError(w, scimInvalidValue("userName is required"))
		return
	}
	if trimSpace(payload.ExternalID) == "" {
		writeError(w, scimInvalidValue("externalId is required"))
		return
	}
	ifMatch, err := ifMatchOf(r)
	if err != nil {
		writeError(w, err)
		return
	}
	user, updateErr := h.service.ReplaceUser(r.Context(), r.PathValue("id"), app.ScimUserInput{
		ExternalID:  payload.ExternalID,
		UserName:    payload.UserName,
		DisplayName: displayNameOfPayload(payload),
		Active:      payload.Active,
	}, ifMatch, apirespTraceID(r))
	if updateErr != nil {
		writeError(w, updateErr)
		return
	}
	w.Header().Set("ETag", etagOf(user.Version))
	writeJSON(w, http.StatusOK, userToResource(user, locationOf(r, "Users", user.ID)))
}

// PatchUser PATCH /admin/scim/v2/Users/{id}
func (h *Handler) PatchUser(w http.ResponseWriter, r *http.Request) {
	var payload patchRequest
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	patch, err := parseUserPatch(payload)
	if err != nil {
		writeError(w, err)
		return
	}
	ifMatch, err := ifMatchOf(r)
	if err != nil {
		writeError(w, err)
		return
	}
	user, patchErr := h.service.PatchUser(r.Context(), r.PathValue("id"), patch, ifMatch, apirespTraceID(r))
	if patchErr != nil {
		writeError(w, patchErr)
		return
	}
	w.Header().Set("ETag", etagOf(user.Version))
	writeJSON(w, http.StatusOK, userToResource(user, locationOf(r, "Users", user.ID)))
}

// DeleteUser DELETE /admin/scim/v2/Users/{id}
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteUser(r.Context(), r.PathValue("id"), apirespTraceID(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rejectPassword 拒绝 password 字段：SSO-only 账号禁止经 SCIM 下发或重置口令。
func rejectPassword(password *string) error {
	if password == nil {
		return nil
	}
	return &app.ScimError{
		Status: http.StatusBadRequest, ScimType: "mutability",
		Detail: "password is managed by the identity provider, not by SCIM",
	}
}

// displayNameOfPayload 计算展示名：displayName 优先，其次 name 复合属性。
func displayNameOfPayload(payload userResource) string {
	if name := strings.TrimSpace(payload.DisplayName); name != "" {
		return name
	}
	return payload.Name.displayNameOf()
}

// parseStartIndex 解析 startIndex；缺省 1，非法为 400 invalidValue。
func parseStartIndex(raw string) (int, error) {
	trimmed := trimSpace(raw)
	if trimmed == "" {
		return 1, nil
	}
	value := parsePositiveInt(trimmed, -1)
	if value < 1 {
		return 0, scimInvalidValue("startIndex must be >= 1")
	}
	return value, nil
}

// parseCount 解析 count；缺省使用配置页大小，允许 0（只返回 totalResults）。
func parseCount(raw string, fallback int) (int, error) {
	trimmed := trimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	value := parsePositiveInt(trimmed, -1)
	if value < 0 {
		return 0, scimInvalidValue("count must be >= 0")
	}
	if value > 100 {
		value = 100
	}
	return value, nil
}

// ifMatchOf 解析 If-Match；缺失返回 nil，非法返回 412。
func ifMatchOf(r *http.Request) (*int64, error) {
	header := trimSpace(r.Header.Get("If-Match"))
	if header == "" {
		return nil, nil
	}
	version := parseETag(header)
	if version == nil {
		return nil, &app.ScimError{
			Status: http.StatusPreconditionFailed,
			Detail: "If-Match must be a resource version ETag",
		}
	}
	return version, nil
}

// scimInvalidValue 构造 400 invalidValue（传输层局部别名，保持调用点简短）。
func scimInvalidValue(detail string) *app.ScimError {
	return &app.ScimError{Status: http.StatusBadRequest, ScimType: "invalidValue", Detail: detail}
}
