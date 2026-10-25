import type { HttpClientOptions } from './http/client';
import { HttpClient } from './http/client';
import { createAdminAuthApi, type AdminAuthApi } from './endpoints/admin/auth.api';
import { createAdminIamApi, type AdminIamApi } from './endpoints/admin/iam.api';

export type AdminClientOptions = Omit<HttpClientOptions, 'channel'>;

export type AdminApiClient = {
  channel: 'admin';
  http: HttpClient;
  auth: AdminAuthApi;
  iam: AdminIamApi;
};

/** A 端唯一入口；端别在编译期与运行期双重固定（§6）。 */
export function createAdminClient(options: AdminClientOptions): AdminApiClient {
  const http = new HttpClient({ ...options, channel: 'admin' });
  return {
    channel: 'admin',
    http,
    auth: createAdminAuthApi(http),
    iam: createAdminIamApi(http),
  };
}
