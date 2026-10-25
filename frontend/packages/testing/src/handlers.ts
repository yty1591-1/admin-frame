import { HttpResponse, http, type HttpHandler } from 'msw';
import { roleListFixture, subjectPermissionsFixture, tokenPairFixture } from './fixtures';

export type HandlerOptions = {
  baseUrl?: string;
  traceId?: string;
  /** 删除角色的结果：approval 用于验证 40900 审批引导分支，禁止自动重试。 */
  roleDeleteOutcome?: 'ok' | 'approval';
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
    http.get(`${baseUrl}/admin/v1/iam/roles`, () =>
      HttpResponse.json(envelope(roleListFixture, traceId)),
    ),
    http.delete(`${baseUrl}/admin/v1/iam/roles/:id`, ({ request }) => {
      if (!request.headers.get('idempotency-key')) {
        return HttpResponse.json(
          { code: 40000, message: '写操作必须提供 Idempotency-Key', trace_id: traceId },
          { status: 400 },
        );
      }
      if (options.roleDeleteOutcome === 'approval') {
        return HttpResponse.json(
          { code: 40900, message: '高风险变更需审批', trace_id: traceId },
          { status: 409 },
        );
      }
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
