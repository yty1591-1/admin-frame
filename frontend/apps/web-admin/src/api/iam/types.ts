/** A 端 IAM 契约类型（与 api/openapi/schemas/admin/iam-*.yaml 对齐）。 */

export type Channel = 'admin' | 'biz';
export type DataScopeType = 'all' | 'merchant' | 'merchant_set' | 'self';
export type RoleScopeType = 'merchant' | 'platform';
export type RoleStatus = 'active' | 'disabled';
export type RoleType = 'breakglass' | 'custom' | 'system';
export type SubjectType = 'merchant_staff' | 'staff';

export interface DataScope {
  merchant_ids?: string[];
  resource: string;
  scope_type: DataScopeType;
}

export interface Permission {
  channel: 'admin' | 'biz' | 'both';
  code: string;
  data_bearing: boolean;
  description?: string;
  name: string;
  resource: string;
  sort: number;
  status: 'active' | 'deprecated';
}

export interface PermissionListData {
  items: Permission[];
  total: number;
}

export interface Role {
  binding_count?: number;
  channel: Channel;
  code: string;
  created_at: string;
  created_by?: string;
  data_scopes: DataScope[];
  description: string;
  id: string;
  merchant_id?: string;
  name: string;
  parent_id?: string;
  permissions: string[];
  role_type: RoleType;
  scope_type: RoleScopeType;
  status: RoleStatus;
  updated_at: string;
  updated_by?: string;
  version: number;
}

export interface RoleListData {
  items: Role[];
  total: number;
}

export interface RoleConstraint {
  max_bindings: null | number;
  required_role_ids: string[];
  role_id: string;
  version: number;
}

export interface CreateRolePayload {
  code: string;
  data_scopes?: DataScope[];
  description?: string;
  name: string;
  parent_id?: string;
  permissions: string[];
  reason?: string;
  scope_type: RoleScopeType;
}

export interface UpdateRolePayload {
  description?: string;
  name?: string;
  parent_id?: string;
  reason?: string;
  status?: RoleStatus;
  version: number;
}

export interface ReplacePermissionsPayload {
  data_scopes?: DataScope[];
  permissions: string[];
  reason?: string;
  version: number;
}

export interface Binding {
  channel: Channel;
  created_at: string;
  expires_at?: string;
  granted_by: string;
  id: string;
  merchant_id?: string;
  role_id: string;
  subject_id: string;
  subject_type: SubjectType;
}

export interface BindingListData {
  items: Binding[];
  total: number;
}

export interface CreateBindingPayload {
  expires_at?: string;
  reason?: string;
  role_id: string;
  subject_id: string;
  subject_type: SubjectType;
}

export interface IamSubjectPermissions {
  channel: Channel;
  data_scopes: DataScope[];
  generated_at: string;
  merchant_id?: string;
  permissions: string[];
  roles: string[];
  snapshot_version: number;
  subject_id: string;
  subject_type: SubjectType;
}

export interface AuthorizeResource {
  id?: string;
  merchant_id?: string;
  type?: string;
}

export interface AuthorizePayload {
  permission: string;
  resource?: AuthorizeResource;
  subject_id: string;
  subject_type: SubjectType;
}

export interface AuthorizeResult {
  allowed: boolean;
  data_scope?: DataScope;
  matched_roles: string[];
  reason: string;
  snapshot_version: number;
}

export type ChangeStatus =
  | 'applied'
  | 'approved'
  | 'cancelled'
  | 'pending'
  | 'rejected';

export type ChangeAction =
  | 'binding_create'
  | 'binding_delete'
  | 'breakglass_request'
  | 'role_clone'
  | 'role_constraint_update'
  | 'role_create'
  | 'role_delete'
  | 'role_permission_replace'
  | 'role_update';

export type ChangeTargetType =
  | 'iam_binding'
  | 'iam_breakglass'
  | 'iam_role'
  | 'iam_sod_group';

export interface ChangeRequest {
  action: ChangeAction;
  applied_at?: string;
  approved_at?: string;
  approved_by?: string;
  channel: Channel;
  id: string;
  merchant_id?: string;
  payload?: Record<string, unknown>;
  reason: string;
  requested_at: string;
  requested_by: string;
  status: ChangeStatus;
  target_id?: string;
  target_type: ChangeTargetType;
  version: number;
}

export interface ChangeRequestListData {
  items: ChangeRequest[];
  total: number;
}

export interface CreateChangeRequestPayload {
  action: ChangeAction;
  payload: Record<string, unknown>;
  reason: string;
  target_id?: string;
}

export interface DecideChangeRequestPayload {
  reason?: string;
  version: number;
}

export interface BreakglassGrant {
  applied_at?: string;
  approved_at?: string;
  approved_by?: string;
  binding_id?: string;
  duration_seconds: number;
  expires_at?: string;
  id: string;
  reason: string;
  requested_at: string;
  requested_by: string;
  status: ChangeStatus;
  subject_id: string;
  subject_type: SubjectType;
  version: number;
}

export interface SodGroup {
  channel: Channel;
  code: string;
  created_at: string;
  created_by?: string;
  description: string;
  id: string;
  max_active_roles: number;
  merchant_id?: string;
  name: string;
  role_ids: string[];
  status: RoleStatus;
  updated_at: string;
  updated_by?: string;
  version: number;
}

export interface SodGroupListData {
  items: SodGroup[];
  total: number;
}

export interface SodViolation {
  merchant_id?: string;
  role_ids: string[];
  subject_id: string;
  subject_type: SubjectType;
}

export interface SodViolationListData {
  items: SodViolation[];
  total: number;
}

export interface SodGroupPayload {
  channel?: Channel;
  code: string;
  description?: string;
  max_active_roles?: number;
  name: string;
  reason?: string;
  role_ids: string[];
}

export type MenuNodeType = 'catalog' | 'iframe' | 'link' | 'menu';

export interface MenuAdmin {
  channel: Channel;
  code: string;
  component: string;
  created_at: string;
  external_url: string;
  hide_in_menu: boolean;
  icon: string;
  id: string;
  keep_alive: boolean;
  node_type: MenuNodeType;
  parent_code?: string;
  permission_code?: string;
  redirect: string;
  route_name: string;
  route_path: string;
  sort: number;
  status: RoleStatus;
  title: string;
  title_key: string;
  updated_at: string;
  updated_by: string;
  version: number;
}

export interface MenuAdminListData {
  items: MenuAdmin[];
  total: number;
}

export interface MenuAdminTreeNode extends MenuAdmin {
  children?: MenuAdminTreeNode[];
}

export interface MenuPayload {
  channel?: Channel;
  code?: string;
  component?: string;
  external_url?: string;
  hide_in_menu?: boolean;
  icon?: string;
  keep_alive?: boolean;
  node_type: MenuNodeType;
  parent_code?: string;
  permission_code?: string;
  reason?: string;
  redirect?: string;
  route_name: string;
  route_path: string;
  sort?: number;
  title: string;
  title_key?: string;
  version?: number;
}

export interface RoleTemplate {
  channel: Channel;
  code: string;
  created_at: string;
  created_by?: string;
  data_scopes: DataScope[];
  description: string;
  id: string;
  name: string;
  permissions: string[];
  status: RoleStatus;
  updated_at: string;
  updated_by?: string;
  version: number;
}

export interface RoleTemplateListData {
  items: RoleTemplate[];
  total: number;
}

export interface TemplateDrift {
  role_code: string;
  role_id: string;
  role_version: number;
  template_version: number;
}

export interface TemplateDriftListData {
  items: TemplateDrift[];
  total: number;
}

export interface CreateTemplatePayload {
  channel?: Channel;
  code: string;
  data_scopes?: DataScope[];
  description?: string;
  name: string;
  permissions: string[];
}

export interface UpdateTemplatePayload {
  description?: string;
  name?: string;
  status?: RoleStatus;
  version: number;
}

export interface InstantiateTemplatePayload {
  channel?: Channel;
  code: string;
  name: string;
  reason?: string;
  scope_type?: RoleScopeType;
}

export interface PageQuery {
  page?: number;
  pageSize?: number;
}

/** 前端 page/pageSize → 后端 page/page_size，并夹紧上限 100。 */
export function toPageQuery<T extends PageQuery>(
  query: T,
): Record<string, unknown> {
  const { page = 1, pageSize = 20 } = query;
  const rest: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(query)) {
    if (key !== 'page' && key !== 'pageSize') {
      rest[key] = value;
    }
  }
  return {
    ...rest,
    page,
    page_size: Math.min(Math.max(pageSize, 1), 100),
  };
}
