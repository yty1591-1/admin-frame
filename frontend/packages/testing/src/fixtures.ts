import type { RoleListData, SubjectPermissions, TokenPair } from '@admin-frame/api-client';

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
  ],
  total: 1,
};
