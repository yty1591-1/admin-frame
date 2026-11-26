package scim

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// ListGroups GET /admin/scim/v2/Groups
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter, err := parseGroupFilter(query.Get("filter"))
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
	page, listErr := h.service.ListGroups(r.Context(), app.ScimGroupQuery{
		DisplayName: filter.DisplayName,
		ExternalID:  filter.ExternalID,
		ID:          filter.ID,
		StartIndex:  startIndex,
		Count:       count,
	})
	if listErr != nil {
		writeError(w, listErr)
		return
	}
	resources := make([]any, 0, len(page.Groups))
	for i := range page.Groups {
		resources = append(resources, groupToResource(&page.Groups[i], locationOf(r, "Groups", page.Groups[i].ID)))
	}
	writeJSON(w, http.StatusOK, listResponse{
		Schemas:      []string{schemaListResponse},
		TotalResults: page.Total,
		StartIndex:   page.StartIndex,
		ItemsPerPage: page.ItemsPerPage,
		Resources:    resources,
	})
}

// GetGroup GET /admin/scim/v2/Groups/{id}
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	group, err := h.service.GetGroup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", etagOf(group.Version))
	writeJSON(w, http.StatusOK, groupToResource(group, locationOf(r, "Groups", group.ID)))
}

// CreateGroup POST /admin/scim/v2/Groups
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var payload groupValue
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	if trimSpace(payload.DisplayName) == "" {
		writeError(w, scimInvalidValue("displayName is required"))
		return
	}
	if trimSpace(payload.ExternalID) == "" {
		writeError(w, scimInvalidValue("externalId is required"))
		return
	}
	group, err := h.service.CreateGroup(r.Context(), app.ScimGroupInput{
		ExternalID:  payload.ExternalID,
		DisplayName: payload.DisplayName,
		Members:     memberValues(payload.Members),
	}, apirespTraceID(r))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", etagOf(group.Version))
	w.Header().Set("Location", locationOf(r, "Groups", group.ID))
	writeJSON(w, http.StatusCreated, groupToResource(group, locationOf(r, "Groups", group.ID)))
}

// ReplaceGroup PUT /admin/scim/v2/Groups/{id}
func (h *Handler) ReplaceGroup(w http.ResponseWriter, r *http.Request) {
	var payload groupValue
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	if trimSpace(payload.DisplayName) == "" {
		writeError(w, scimInvalidValue("displayName is required"))
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
	group, updateErr := h.service.ReplaceGroup(r.Context(), r.PathValue("id"), app.ScimGroupInput{
		ExternalID:  payload.ExternalID,
		DisplayName: payload.DisplayName,
		Members:     memberValues(payload.Members),
	}, ifMatch, apirespTraceID(r))
	if updateErr != nil {
		writeError(w, updateErr)
		return
	}
	w.Header().Set("ETag", etagOf(group.Version))
	writeJSON(w, http.StatusOK, groupToResource(group, locationOf(r, "Groups", group.ID)))
}

// PatchGroup PATCH /admin/scim/v2/Groups/{id}
func (h *Handler) PatchGroup(w http.ResponseWriter, r *http.Request) {
	var payload patchRequest
	if err := h.decodeBody(w, r, &payload); err != nil {
		writeError(w, err)
		return
	}
	patch, err := parseGroupPatch(payload)
	if err != nil {
		writeError(w, err)
		return
	}
	ifMatch, err := ifMatchOf(r)
	if err != nil {
		writeError(w, err)
		return
	}
	group, patchErr := h.service.PatchGroup(r.Context(), r.PathValue("id"), patch, ifMatch, apirespTraceID(r))
	if patchErr != nil {
		writeError(w, patchErr)
		return
	}
	w.Header().Set("ETag", etagOf(group.Version))
	writeJSON(w, http.StatusOK, groupToResource(group, locationOf(r, "Groups", group.ID)))
}

// DeleteGroup DELETE /admin/scim/v2/Groups/{id}
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteGroup(r.Context(), r.PathValue("id"), apirespTraceID(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memberValues 提取成员标识，保持原始顺序。
func memberValues(members []memberValue) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		if value := trimSpace(member.Value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
