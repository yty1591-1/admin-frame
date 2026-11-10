// Package scim 提供 SCIM 2.0 服务端传输层（docs/identity-authn.md §21.1）。
//
// 协议例外：/admin/scim/v2/* 的响应体按 RFC 7643 / 7644，不使用统一信封
// {code,message,trace_id}；链路 ID 经响应头 X-Trace-Id 返回。
package scim

import (
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// SCIM 2.0 协议常量。
const (
	// schemaUser User 资源 schema URN。
	schemaUser = "urn:ietf:params:scim:schemas:core:2.0:User"
	// schemaGroup Group 资源 schema URN。
	schemaGroup = "urn:ietf:params:scim:schemas:core:2.0:Group"
	// schemaListResponse 列表响应 schema URN。
	schemaListResponse = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	// schemaError 协议错误 schema URN。
	schemaError = "urn:ietf:params:scim:api:messages:2.0:Error"
	// schemaPatchOp PATCH 请求 schema URN。
	schemaPatchOp = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	// schemaServiceProviderConfig 能力声明 schema URN。
	schemaServiceProviderConfig = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	// schemaResourceType 资源类型 schema URN。
	schemaResourceType = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	// schemaSchema 模型声明 schema URN。
	schemaSchema = "urn:ietf:params:scim:schemas:core:2.0:Schema"
	// contentTypeSCIM SCIM 媒体类型；同时接受 application/json。
	contentTypeSCIM = "application/scim+json"
	// contentTypeJSON 通用 JSON 媒体类型。
	contentTypeJSON = "application/json"
)

// scimTimeFormat SCIM meta 时间格式（RFC 7643 §3.1）。
const scimTimeFormat = time.RFC3339

// userResource 是 User 的线上表示。
type userResource struct {
	Schemas     []string   `json:"schemas"`
	ID          string     `json:"id,omitempty"`
	ExternalID  string     `json:"externalId,omitempty"`
	UserName    string     `json:"userName"`
	Name        *nameValue `json:"name,omitempty"`
	DisplayName string     `json:"displayName,omitempty"`
	Active      *bool      `json:"active,omitempty"`
	// Password 只用于检测并拒绝：SSO-only 账号禁止经 SCIM 下发或重置口令。
	Password *string         `json:"password,omitempty"`
	Groups   []groupRefValue `json:"groups,omitempty"`
	Meta     metaValue       `json:"meta"`
}

// nameValue 是 User.name 复合属性；只落 display_name，不新增 PII 列。
type nameValue struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// displayNameOf 计算展示名：优先 formatted，其次 givenName + familyName。
func (n *nameValue) displayNameOf() string {
	if n == nil {
		return ""
	}
	if v := trimSpace(n.Formatted); v != "" {
		return v
	}
	given := trimSpace(n.GivenName)
	family := trimSpace(n.FamilyName)
	switch {
	case given == "" && family == "":
		return ""
	case given == "":
		return family
	case family == "":
		return given
	default:
		return given + " " + family
	}
}

// groupValue 是 Group 的线上表示。
type groupValue struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id,omitempty"`
	ExternalID  string        `json:"externalId,omitempty"`
	DisplayName string        `json:"displayName"`
	Members     []memberValue `json:"members,omitempty"`
	Meta        metaValue     `json:"meta"`
}

// memberValue 是 Group.members 元素；value 使用账号 ID 或 externalId。
type memberValue struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"`
}

// groupRefValue 是 User.groups 元素（只读投影）。
type groupRefValue struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"`
}

// metaValue 是资源元数据；version 与 ETag 同源。
type metaValue struct {
	ResourceType string `json:"resourceType,omitempty"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Version      string `json:"version,omitempty"`
	Location     string `json:"location,omitempty"`
}

// listResponse 是 SCIM 列表响应；Resources 字段名按 RFC 7644 首字母大写。
type listResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []any    `json:"Resources"`
}

// errorResponse 是 RFC 7644 §3.12 协议错误体。
type errorResponse struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	ScimType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// userToResource 把用例视图转换为线上表示。
func userToResource(user *app.ScimUser, location string) userResource {
	active := user.Active
	resource := userResource{
		Schemas:     []string{schemaUser},
		ID:          user.ID,
		ExternalID:  user.ExternalID,
		UserName:    user.UserName,
		DisplayName: user.DisplayName,
		Active:      &active,
		Meta: metaValue{
			ResourceType: "User",
			Created:      user.CreatedAt.UTC().Format(scimTimeFormat),
			LastModified: user.UpdatedAt.UTC().Format(scimTimeFormat),
			Version:      etagOf(user.Version),
			Location:     location,
		},
	}
	if len(user.Groups) > 0 {
		refs := make([]groupRefValue, 0, len(user.Groups))
		for _, group := range user.Groups {
			refs = append(refs, groupRefValue{Value: group.Value, Display: group.Display, Type: "direct"})
		}
		resource.Groups = refs
	}
	return resource
}

// groupToResource 把用例视图转换为线上表示。
func groupToResource(group *app.ScimGroup, location string) groupValue {
	members := make([]memberValue, 0, len(group.Members))
	for _, member := range group.Members {
		members = append(members, memberValue{Value: member.Value, Display: member.Display, Type: "User"})
	}
	return groupValue{
		Schemas:     []string{schemaGroup},
		ID:          group.ID,
		ExternalID:  group.ExternalID,
		DisplayName: group.DisplayName,
		Members:     members,
		Meta: metaValue{
			ResourceType: "Group",
			Created:      group.CreatedAt.UTC().Format(scimTimeFormat),
			LastModified: group.UpdatedAt.UTC().Format(scimTimeFormat),
			Version:      etagOf(group.Version),
			Location:     location,
		},
	}
}

// etagOf 生成 ETag / meta.version：弱校验器，与版本号同源。
func etagOf(version int64) string {
	return fmt.Sprintf(`W/"%d"`, version)
}

// trimSpace 包装 strings.TrimSpace，避免在多个小函数里重复导入。
func trimSpace(value string) string { return strings.TrimSpace(value) }
