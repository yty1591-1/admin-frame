import type { HttpClient } from '../../http/client';
import { subjectPermissionsSchema } from '../../schemas';
import type { SubjectPermissions } from '../../types';

export type AdminIamApi = {
  /** 服务端权限快照是唯一权威来源（§10.4）。 */
  mePermissions(): Promise<SubjectPermissions>;
};

export function createAdminIamApi(http: HttpClient): AdminIamApi {
  return {
    mePermissions: () =>
      http.request<SubjectPermissions>('/admin/v1/iam/me/permissions', {
        schema: subjectPermissionsSchema,
        retry: 'safe',
      }),
  };
}
