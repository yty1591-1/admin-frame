import { HttpResponse, http, type HttpHandler } from 'msw';
import { subjectPermissionsFixture, tokenPairFixture } from './fixtures';

export type HandlerOptions = {
  baseUrl?: string;
  traceId?: string;
};

function envelope(data: unknown, traceId: string) {
  return { code: 0, message: 'ok', trace_id: traceId, data };
}

/** A 端契约 Mock（MSW），响应结构必须与生成类型对齐（§14.1）。 */
export function createAdminHandlers(options: HandlerOptions = {}): HttpHandler[] {
  const baseUrl = options.baseUrl ?? 'http://api.test';
  const traceId = options.traceId ?? 'test-trace';
  return [
    http.post(`${baseUrl}/admin/v1/auth/login`, () =>
      HttpResponse.json(envelope(tokenPairFixture, traceId)),
    ),
    http.post(`${baseUrl}/admin/v1/auth/refresh`, () =>
      HttpResponse.json(envelope(tokenPairFixture, traceId)),
    ),
    http.post(`${baseUrl}/admin/v1/auth/logout`, () => new HttpResponse(null, { status: 204 })),
    http.get(`${baseUrl}/admin/v1/iam/me/permissions`, () =>
      HttpResponse.json(envelope(subjectPermissionsFixture, traceId)),
    ),
  ];
}
