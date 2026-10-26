import type {
  AuthSessionListData,
  BizSubjectPermissions,
  MFASetupData,
  PermissionListData,
  RoleListData,
  SubjectPermissions,
  TokenPair,
} from '@admin-frame/api-client';

export const tokenPairFixture: TokenPair = {
  access_token: 'test-access',
  refresh_token: 'test-refresh',
  token_type: 'Bearer',
  expires_in: 600,
};

export const subjectPermissionsFixture: SubjectPermissions = {
  channel: 'admin',
  subject_type: 'staff',
  subject_id: 'staff-1',
  roles: ['platform-admin'],
  permissions: ['iam:role:read', 'iam:permission:read'],
  data_scopes: [{ resource: '*', scope_type: 'all' }],
  snapshot_version: 1,
  generated_at: '2026-01-01T00:00:00Z',
};

/** B 端快照：租户上下文由服务端推导，fixture 不携带 merchant_id 作为请求参数。 */
export const bizSubjectPermissionsFixture: BizSubjectPermissions = {
  channel: 'biz',
  subject_type: 'merchant_staff',
  subject_id: 'merchant-staff-1',
  merchant_id: 'merchant-1',
  roles: ['merchant-admin'],
  permissions: ['biz:iam:role:read'],
  data_scopes: [{ resource: '*', scope_type: 'merchant' }],
  snapshot_version: 1,
  generated_at: '2026-01-01T00:00:00Z',
};

export const roleListFixture: RoleListData = {
  items: [
    {
      id: 'role-ops',
      channel: 'admin',
      scope_type: 'admin',
      code: 'ops',
      name: '运营',
      role_type: 'custom',
      status: 'active',
      version: 1,
      permissions: ['iam:role:read'],
      data_scopes: [{ resource: 'iam', scope_type: 'all' }],
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
    {
      id: 'role-system',
      channel: 'admin',
      scope_type: 'admin',
      code: 'platform-admin',
      name: '平台管理员',
      role_type: 'system',
      status: 'active',
      version: 1,
      permissions: ['iam:role:read', 'iam:role:write'],
      data_scopes: [{ resource: '*', scope_type: 'all' }],
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  ],
  total: 2,
};

export const permissionListFixture: PermissionListData = {
  items: [
    {
      code: 'iam:role:read',
      name: '查看角色',
      channel: 'admin',
      kind: 'api',
      data_bearing: false,
      status: 'active',
      sort: 10,
    },
    {
      code: 'iam:role:write',
      name: '维护角色',
      channel: 'admin',
      kind: 'button',
      data_bearing: false,
      status: 'active',
      sort: 20,
    },
  ],
  total: 2,
};

export const mfaSetupFixture: MFASetupData = {
  secret: 'JBSWY3DPEHPK3PXP',
  otpauth_uri: 'otpauth://totp/admin-frame:staff-1?secret=JBSWY3DPEHPK3PXP&issuer=admin-frame',
  recovery_codes: ['recovery-1', 'recovery-2'],
};

export const authSessionListFixture: AuthSessionListData = {
  items: [
    {
      id: 'session-1',
      current: true,
      status: 'active',
      created_at: '2026-01-01T00:00:00Z',
      last_used_at: '2026-01-01T00:05:00Z',
      expires_at: '2026-01-01T00:15:00Z',
    },
  ],
};
