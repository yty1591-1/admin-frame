import type { HttpClientOptions } from './http/client';
import { HttpClient } from './http/client';
import { createBizAuthApi, type BizAuthApi } from './endpoints/biz/auth.api';
import { createBizIamApi, type BizIamApi } from './endpoints/biz/iam.api';

export type BizClientOptions = Omit<HttpClientOptions, 'channel'>;

export type BizApiClient = {
  channel: 'biz';
  http: HttpClient;
  auth: BizAuthApi;
  iam: BizIamApi;
};

/** B 端唯一入口；禁止与 A 端复用实例或类型（§6）。 */
export function createBizClient(options: BizClientOptions): BizApiClient {
  const http = new HttpClient({ ...options, channel: 'biz' });
  return {
    channel: 'biz',
    http,
    auth: createBizAuthApi(http),
    iam: createBizIamApi(http),
  };
}
