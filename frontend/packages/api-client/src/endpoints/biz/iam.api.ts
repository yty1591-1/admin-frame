import type { HttpClient } from '../../http/client';
import { subjectPermissionsSchema } from '../../schemas';
import type { BizSubjectPermissions } from '../../types';

export type BizIamApi = {
  /** B 端租户上下文由 Token/服务端推导，前端不发送 merchant_id（§6）。 */
  mePermissions(): Promise<BizSubjectPermissions>;
};

export function createBizIamApi(http: HttpClient): BizIamApi {
  return {
    mePermissions: () =>
      http.request<BizSubjectPermissions>('/biz/v1/iam/me/permissions', {
        schema: subjectPermissionsSchema,
        retry: 'safe',
      }),
  };
}
