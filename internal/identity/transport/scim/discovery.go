package scim

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// ServiceProviderConfig 返回 GET /ServiceProviderConfig 响应。
//
// 明确声明不支持 bulk / sort / changePassword：客户端据此选择降级路径，
// 避免反复试探（RFC 7643 §5）。
func (h *Handler) ServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	base := scimBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"schemas":          []string{schemaServiceProviderConfig},
		"documentationUri": "https://www.rfc-editor.org/rfc/rfc7644",
		"patch":            map[string]any{"supported": true},
		"bulk": map[string]any{
			"supported": false, "maxOperations": 0, "maxPayloadSize": 0,
		},
		"filter":         map[string]any{"supported": true, "maxResults": 100},
		"changePassword": map[string]any{"supported": false},
		"sort":           map[string]any{"supported": false},
		"etag":           map[string]any{"supported": true},
		"authenticationSchemes": []map[string]any{
			{
				"type":        "oauthbearertoken",
				"name":        "OAuth Bearer Token",
				"description": "Authentication scheme using the OAuth Bearer Token Standard",
				"specUri":     "https://www.rfc-editor.org/info/rfc6750",
				"primary":     true,
			},
		},
		"meta": map[string]any{
			"resourceType": "ServiceProviderConfig",
			"location":     base + "/ServiceProviderConfig",
		},
	})
}

// ResourceTypes 返回 GET /ResourceTypes：仅声明 User 与 Group。
func (h *Handler) ResourceTypes(w http.ResponseWriter, r *http.Request) {
	resources := resourceTypeDefinitions(scimBaseURL(r))
	writeList(w, resources, len(resources))
}

// ResourceTypeByID 返回 GET /ResourceTypes/{id}。
//
// 必须提供：列表里每个 ResourceType 的 meta.location 指向该地址，
// 客户端（Okta / Entra ID）会跟随；缺失会得到 404 并判定能力探测失败。
func (h *Handler) ResourceTypeByID(w http.ResponseWriter, r *http.Request) {
	target := trimSpace(r.PathValue("id"))
	for _, resource := range resourceTypeDefinitions(scimBaseURL(r)) {
		if strings.EqualFold(asString(resource["id"]), target) {
			writeJSON(w, http.StatusOK, resource)
			return
		}
	}
	writeError(w, &app.ScimError{Status: http.StatusNotFound, Detail: "resource type not found"})
}

// Schemas 返回 GET /Schemas：User / Group 的最小属性声明。
func (h *Handler) Schemas(w http.ResponseWriter, r *http.Request) {
	schemas := schemaDefinitions(scimBaseURL(r))
	writeList(w, schemas, len(schemas))
}

// SchemaByID 返回 GET /Schemas/{id}；id 为 schema URI，例如
// urn:ietf:params:scim:schemas:core:2.0:User。
func (h *Handler) SchemaByID(w http.ResponseWriter, r *http.Request) {
	target := trimSpace(r.PathValue("id"))
	for _, schema := range schemaDefinitions(scimBaseURL(r)) {
		if strings.EqualFold(asString(schema["id"]), target) {
			writeJSON(w, http.StatusOK, schema)
			return
		}
	}
	writeError(w, &app.ScimError{Status: http.StatusNotFound, Detail: "schema not found"})
}

// resourceTypeDefinitions 构造 ResourceType 列表；base 用于 meta.location。
func resourceTypeDefinitions(base string) []map[string]any {
	return []map[string]any{
		{
			"schemas":     []string{schemaResourceType},
			"id":          "User",
			"name":        "User",
			"endpoint":    "/Users",
			"description": "A 端员工 / 运营账号",
			"schema":      schemaUser,
			"meta": map[string]any{
				"resourceType": "ResourceType",
				"location":     base + "/ResourceTypes/User",
			},
		},
		{
			"schemas":     []string{schemaResourceType},
			"id":          "Group",
			"name":        "Group",
			"endpoint":    "/Groups",
			"description": "IdP 组；按 group_role_map 投影为 A 端角色绑定",
			"schema":      schemaGroup,
			"meta": map[string]any{
				"resourceType": "ResourceType",
				"location":     base + "/ResourceTypes/Group",
			},
		},
	}
}

// schemaDefinitions 构造 Schema 列表；base 用于 meta.location。
func schemaDefinitions(base string) []map[string]any {
	return []map[string]any{
		{
			"schemas":     []string{schemaSchema},
			"id":          schemaUser,
			"name":        "User",
			"description": "A 端账号；口令只由 IdP 管理，SCIM 不接受 password",
			"attributes": []map[string]any{
				attr("userName", "string", true, "readWrite", "immutable-when-bound"),
				attr("externalId", "string", true, "readWrite", "idempotency-key"),
				attr("name", "complex", false, "readWrite", ""),
				attr("displayName", "string", false, "readWrite", ""),
				attr("active", "boolean", false, "readWrite", ""),
				attr("groups", "complex", false, "readOnly", "projected from Group.members"),
			},
			"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + schemaUser},
		},
		{
			"schemas":     []string{schemaSchema},
			"id":          schemaGroup,
			"name":        "Group",
			"description": "IdP 组；成员变更投影为 IAM 角色绑定",
			"attributes": []map[string]any{
				attr("displayName", "string", true, "readWrite", "role-mapping-key"),
				attr("externalId", "string", true, "readWrite", "idempotency-key"),
				attr("members", "complex", false, "readWrite", "value = account id or externalId"),
			},
			"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + schemaGroup},
		},
	}
}

// asString 把 map 中的字符串值取出；非字符串返回空串。
func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// attr 构造属性声明。
func attr(name, typ string, required bool, mutability, description string) map[string]any {
	out := map[string]any{
		"name":        name,
		"type":        typ,
		"multiValued": typ == "complex" && name == "members",
		"required":    required,
		"caseExact":   false,
		"mutability":  mutability,
		"returned":    "default",
		"uniqueness":  "none",
	}
	if description != "" {
		out["description"] = description
	}
	return out
}

// scimBaseURL 返回 SCIM 基础 URL，用于 meta.location。
func scimBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := trimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := trimSpace(r.Host)
	if host == "" {
		return "/admin/scim/v2"
	}
	return scheme + "://" + host + "/admin/scim/v2"
}

// writeList 写出发现端点的列表响应。
func writeList(w http.ResponseWriter, resources []map[string]any, total int) {
	items := make([]any, 0, len(resources))
	for _, resource := range resources {
		items = append(items, resource)
	}
	writeJSON(w, http.StatusOK, listResponse{
		Schemas:      []string{schemaListResponse},
		TotalResults: total,
		StartIndex:   1,
		ItemsPerPage: total,
		Resources:    items,
	})
}
