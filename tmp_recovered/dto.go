// Package rest 提供 IAM 的通道无关 HTTP 传输层（A 端 /admin/v1/iam/*、B 端 /biz/v1/iam/*）。
//
// 只依赖 app/domain/shared 契约：不 import infrastructure 与 platform，
// 参数解析与校验在 dto.go，各端路由与权限点绑定在 transport/admin、transport/biz。
package rest

import (
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// PermissionDTO 是权限点目录响应项。
type PermissionDTO struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Channel     string `json:"channel"`
	Kind        string `json:"kind"`
	DataBearing bool   `json:"data_bearing"`
	Status      string `json:"status"`
	Sort        int    `json:"sort"`
	ParentCode  string `json:"parent_code,omitempty"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
}

// PermissionTreeDTO 是权限树节点，children 为空时省略。
type PermissionTreeDTO struct {
	PermissionDTO
	Children []*PermissionTreeDTO `json:"children,omitempty"`
}

// PermissionListResponse 是权限目录分页响应。
type PermissionListResponse struct {
	Items []*PermissionDTO `json:"items"`
	Total int              `json:"total"`
}

// DataScopeDTO 是角色数据范围。
type DataScopeDTO struct {
	Resource    string   `json:"resource"`
	ScopeType   string   `json:"scope_type"`
	MerchantIDs []string `json:"merchant_ids,omitempty"`
}

// RoleDTO 是角色响应（含权限与数据范围）。
type RoleDTO struct {
	ID          string         `json:"id"`
	Channel     string         `json:"channel"`
	ScopeType   string         `json:"scope_type"`
	MerchantID  string         `json:"merchant_id"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	RoleType    string         `json:"role_type"`
	Status      string         `json:"status"`
	Version     int64          `json:"version"`
	Permissions []string       `json:"permissions"`
	DataScopes  []DataScopeDTO `json:"data_scopes"`
	BindingCount int           `json:"binding_count,omitempty"`
	CreatedBy   string         `json:"created_by,omitempty"`
	UpdatedBy   string         `json:"updated_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// RoleListResponse 是角色列表响应。
type RoleListResponse struct {
	Items []*RoleDTO `json:"items"`
	Total int        `json:"total"`
}

// CreateRoleRequest 是创建角色请求体。
type CreateRoleRequest struct {
	Channel     string         `json:"channel"`
	ScopeType   string         `json:"scope_type"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Permissions []string       `json:"permissions"`
	DataScopes  []DataScopeDTO `json:"data_scopes"`
	Reason      string         `json:"reason"`
}

// UpdateRoleRequest 是 PATCH 角色请求体；nil 字段表示不修改。
type UpdateRoleRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Version     int64   `json:"version"`
	Reason      string  `json:"reason"`
}

// ReplacePermissionsRequest 是全量替换权限请求体。
type ReplacePermissionsRequest struct {
	Permissions []string         `json:"permissions"`
	DataScopes  *[]DataScopeDTO  `json:"data_scopes"`
	Version     int64            `json:"version"`
	Reason      string           `json:"reason"`
}

// ReplacePermissionsResponse 是全量替换权限响应：返回最新角色与差量。
type ReplacePermissionsResponse struct {
	Role    *RoleDTO `json:"role"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// CloneRoleRequest 是复制角色请求体。
type CloneRoleRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// DeleteRoleRequest 是软删角色请求体（version 也可由 If-Match 提供）。
type DeleteRoleRequest struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

// BindingDTO 是绑定响应。
type BindingDTO struct {
	ID          string     `json:"id"`
	Channel     string     `json:"channel"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	MerchantID  string     `json:"merchant_id"`
	RoleID      string     `json:"role_id"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	GrantedBy   string     `json:"granted_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

// BindingListResponse 是绑定列表响应。
type BindingListResponse struct {
	Items []*BindingDTO `json:"items"`
	Total int           `json:"total"`
}

// CreateBindingRequest 是绑定角色请求体。
type CreateBindingRequest struct {
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	RoleID      string     `json:"role_id"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Reason      string     `json:"reason"`
}

// UnbindRequest 是解绑请求体。
type UnbindRequest struct {
	Reason string `json:"reason"`
}

// SubjectPermissionsResponse 是主体有效权限响应。
type SubjectPermissionsResponse struct {
	Channel         string         `json:"channel"`
	SubjectType     string         `json:"subject_type"`
	SubjectID       string         `json:"subject_id"`
	MerchantID      string         `json:"merchant_id"`
	Roles           []string       `json:"roles"`
	Permissions     []string       `json:"permissions"`
	DataScopes      []DataScopeDTO `json:"data_scopes"`
	SnapshotVersion int64          `json:"snapshot_version"`
	GeneratedAt     time.Time      `json:"generated_at"`
}

// AuthorizeRequest 是模拟鉴权请求体。
type AuthorizeRequest struct {
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	Permission  string         `json:"permission"`
	Resource    AuthorizeResource `json:"resource"`
}

// AuthorizeResource 是模拟鉴权的资源上下文。
type AuthorizeResource struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	MerchantID string `json:"merchant_id"`
}

// AuthorizeResponse 是模拟鉴权响应；不返回完整权限表。
type AuthorizeResponse struct {
	Allowed         bool          `json:"allowed"`
	Reason          string        `json:"reason"`
	MatchedRoles    []string      `json:"matched_roles"`
	DataScope       *DataScopeDTO `json:"data_scope,omitempty"`
	SnapshotVersion int64         `json:"snapshot_version"`
}

// toPermissionDTO 转换目录条目。
func toPermissionDTO(entry catalog.Entry) *PermissionDTO {
	return &PermissionDTO{
		Code:        string(entry.Code),
		Name:        entry.Name,
		Channel:     string(entry.Channel),
		Kind:        string(entry.Kind),
		DataBearing: entry.DataBearing,
		Status:      string(entry.Status),
		Sort:        entry.Sort,
		ParentCode:  string(entry.ParentCode),
		Path:        entry.Path,
		Description: entry.Description,
	}
}

// toPermissionTreeDTO 递归转换权限树。
func toPermissionTreeDTO(nodes []*app.PermissionNode) []*PermissionTreeDTO {
	out := make([]*PermissionTreeDTO, 0, len(nodes))
	for _, node := range nodes {
		dto := &PermissionTreeDTO{PermissionDTO: *toPermissionDTO(node.Entry)}
		dto.Children = toPermissionTreeDTO(node.Children)
		out = append(out, dto)
	}
	return out
}

// toDataScopeDTO 转换单个数据范围。
func toDataScopeDTO(scope domain.DataScope) DataScopeDTO {
	return DataScopeDTO{
		Resource:    scope.Resource,
		ScopeType:   string(scope.Type),
		MerchantIDs: scope.MerchantIDs,
	}
}

// toDataScopeDTOs 转换数据范围列表（保持角色内按资源排序）。
func toDataScopeDTOs(scopes []domain.DataScope) []DataScopeDTO {
	out := make([]DataScopeDTO, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, toDataScopeDTO(scope))
	}
	return out
}

// toRoleDTO 转换角色聚合。
func toRoleDTO(role *domain.Role) *RoleDTO {
	codes := role.PermissionCodes()
	permissions := make([]string, 0, len(codes))
	for _, code := range codes {
		permissions = append(permissions, string(code))
	}
	return &RoleDTO{
		ID:          role.ID,
		Channel:     string(role.Channel),
		ScopeType:   string(role.Scope),
		MerchantID:  role.MerchantID,
		Code:        role.Code,
		Name:        role.Name,
		Description: role.Description,
		RoleType:    string(role.Type),
		Status:      string(role.Status),
		Version:     role.Version,
		Permissions: permissions,
		DataScopes:  toDataScopeDTOs(role.SortedDataScopes()),
		CreatedBy:   role.CreatedBy,
		UpdatedBy:   role.UpdatedBy,
		CreatedAt:   role.CreatedAt,
		UpdatedAt:   role.UpdatedAt,
	}
}

// toBindingDTO 转换绑定聚合。
func toBindingDTO(binding *domain.Binding) *BindingDTO {
	return &BindingDTO{
		ID:          binding.ID,
		Channel:     string(binding.Channel),
		SubjectType: string(binding.SubjectType),
		SubjectID:   binding.SubjectID,
		MerchantID:  binding.MerchantID,
		RoleID:      binding.RoleID,
		ExpiresAt:   binding.ExpiresAt,
		GrantedBy:   binding.GrantedBy,
		CreatedAt:   binding.CreatedAt,
	}
}

// toPermissionCodes 把请求体权限码转换为领域类型（格式由领域层校验）。
func toPermissionCodes(codes []string) []iam.PermissionCode {
	out := make([]iam.PermissionCode, 0, len(codes))
	for _, code := range codes {
		out = append(out, iam.PermissionCode(code))
	}
	return out
}

// permissionCodeStrings 把领域权限码转回字符串，供响应与差量展示。
func permissionCodeStrings(codes []iam.PermissionCode) []string {
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		out = append(out, string(code))
	}
	return out
}

// toDataScopes 把请求体数据范围转换为领域值对象。
func toDataScopes(dtos []DataScopeDTO) ([]domain.DataScope, error) {
	out := make([]domain.DataScope, 0, len(dtos))
	for _, dto := range dtos {
		scope, err := domain.NewDataScope(dto.Resource, domain.DataScopeType(dto.ScopeType), dto.MerchantIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, scope)
	}
	return out, nil
}

// toSubjectPermissionsResponse 转换快照响应。
func toSubjectPermissionsResponse(snapshot *domain.SubjectSnapshot) *SubjectPermissionsResponse {
	permissions := make([]string, 0, len(snapshot.Permissions))
	for _, code := range snapshot.Permissions {
		permissions = append(permissions, string(code))
	}
	return &SubjectPermissionsResponse{
		Channel:         string(snapshot.Channel),
		SubjectType:     string(snapshot.SubjectType),
		SubjectID:       snapshot.SubjectID,
		MerchantID:      snapshot.MerchantID,
		Roles:           snapshot.Roles,
		Permissions:     permissions,
		DataScopes:      toDataScopeDTOs(snapshot.Scopes),
		SnapshotVersion: snapshot.Version,
		GeneratedAt:     snapshot.GeneratedAt,
	}
}
