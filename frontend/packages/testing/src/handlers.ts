import { HttpResponse, delay, http, type HttpHandler } from 'msw';
import {
  authSessionListFixture,
  bizSubjectPermissionsFixture,
  mfaSetupFixture,
  permissionListFixture,
  roleListFixture,
  subjectPermissionsFixture,
  tokenPairFixture,
} from './fixtures';

export type HandlerOptions = {
  baseUrl?: string;
  traceId?: string;
  /** 登录结果：覆盖密码失败、两步 MFA 与登录前强制绑定分支（§10.1、§7.2.1）。 */
  loginOutcome?: 'tokens' | 'mfa' | 'mfaSetup' | 'unauthorized';
  /** 动态码校验结果；invalid 时票据同时作废（一次性消费）。 */
  mfaVerifyOutcome?: 'ok' | 'invalid';
  /** 权限快照结果：unavailable 验证 fail-closed，empty 验证无权限渲染（§7.2）。 */
  permissionSnapshotOutcome?: 'ok' | 'unavailable' | 'empty';
  /** 覆盖快照权限点，用于按钮级权限用例（§7.3）。 */
  permissions?: readonly string[];
  roleListOutcome?: 'ok' | 'serverError';
  permissionListOutcome?: 'ok' | 'serverError';
  /** 删除角色的结果：approval 验证 40900 审批引导，且不得自动重试。 */
  roleDeleteOutcome?: 'ok' | 'approval' | 'serverError';
  /** 列表响应延迟，用于验证首屏骨架态。 */
  delayMs?: number;
};

type EnvelopeOptions = {
  traceId: string;
  status?: number;
};

function envelope(data: unknown, { traceId, status = 200 }: EnvelopeOptions): Response {
  return HttpResponse.json({ code: 0, message: 'ok', trace_id: traceId, data }, { status });
}

function failure(
  code: number,
  message: string,
  { traceId, status = 400 }: EnvelopeOptions,
): Response {
  return HttpResponse.json({ code, message, trace_id: traceId }, { status });
}

async function waitFor(options: HandlerOptions): Promise<void> {
  if (options.delayMs !== undefined && options.delayMs > 0) {
    await delay(options.delayMs);
  }
}

async function readBody(request: Request): Promise<Record<string, unknown>> {
  try {
    const parsed: unknown = await request.json();
    return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

/**
 * A 端契约 Mock（MSW），响应结构必须与生成类型对齐（§14.1）。
 * 覆盖成功、业务错误与依赖不可用，供 apps 组件/流程测试直接复用。
 */
export function createAdminHandlers(options: HandlerOptions = {}): HttpHandler[] {
  const baseUrl = options.baseUrl ?? 'http://api.test';
  const traceId = options.traceId ?? 'test-trace';
  const meta: EnvelopeOptions = { traceId };
  const loginOutcome = options.loginOutcome ?? 'tokens';
  const snapshotOutcome = options.permissionSnapshotOutcome ?? 'ok';

  return [
    http.post(`${baseUrl}/admin/v1/auth/login`, () => {
      if (loginOutcome === 'unauthorized') {
        return failure(40100, '账号或密码错误', { traceId, status: 401 });
      }
      if (loginOutcome === 'mfa') {
        return envelope({ mfa_required: true, mfa_ticket: 'test-mfa-ticket' }, meta);
      }
      if (loginOutcome === 'mfaSetup') {
        return envelope(
          { mfa_binding_required: true, mfa_setup_ticket: 'test-setup-ticket' },
          meta,
        );
      }
      return envelope(tokenPairFixture, meta);
    }),
    http.post(`${baseUrl}/admin/v1/auth/refresh`, () => envelope(tokenPairFixture, meta)),
    http.post(`${baseUrl}/admin/v1/auth/mfa/verify`, async ({ request }) => {
      const body = await readBody(request);
      const hasTicket =
        typeof body.mfa_ticket === 'string' || typeof body.mfa_setup_ticket === 'string';
      if (!hasTicket) {
        // 已登录状态下的绑定确认（confirmMfaBinding）返回 204。
        return new HttpResponse(null, { status: 204 });
      }
      if (options.mfaVerifyOutcome === 'invalid' || body.code === '000000') {
        return failure(40100, '动态码错误', { traceId, status: 401 });
      }
      return envelope(tokenPairFixture, meta);
    }),
    http.post(`${baseUrl}/admin/v1/auth/mfa/setup`, () => envelope(mfaSetupFixture, meta)),
    http.post(
      `${baseUrl}/admin/v1/auth/mfa/disable`,
      () => new HttpResponse(null, { status: 204 }),
    ),
    http.get(`${baseUrl}/admin/v1/auth/sessions`, () => envelope(authSessionListFixture, meta)),
    http.delete(
      `${baseUrl}/admin/v1/auth/sessions/:id`,
      () => new HttpResponse(null, { status: 204 }),
    ),
    http.post(
      `${baseUrl}/admin/v1/auth/sessions/revoke-all`,
      () => new HttpResponse(null, { status: 204 }),
    ),
    http.post(`${baseUrl}/admin/v1/auth/logout`, () => new HttpResponse(null, { status: 204 })),
    http.post(`${baseUrl}/admin/v1/auth/logout-all`, () => new HttpResponse(null, { status: 204 })),
    http.get(`${baseUrl}/admin/v1/iam/me/permissions`, () => {
      if (snapshotOutcome === 'unavailable') {
        return failure(50300, '依赖不可用', { traceId, status: 503 });
      }
      const permissions =
        snapshotOutcome === 'empty'
          ? []
          : (options.permissions ?? subjectPermissionsFixture.permissions);
      return envelope({ ...subjectPermissionsFixture, permissions }, meta);
    }),
    http.get(`${baseUrl}/admin/v1/iam/permissions`, async () => {
      await waitFor(options);
      if (options.permissionListOutcome === 'serverError') {
        return failure(50000, '服务端错误', { traceId, status: 500 });
      }
      return envelope(permissionListFixture, meta);
    }),
    http.get(`${baseUrl}/admin/v1/iam/roles`, async () => {
      await waitFor(options);
      if (options.roleListOutcome === 'serverError') {
        return failure(50000, '服务端错误', { traceId, status: 500 });
      }
      return envelope(roleListFixture, meta);
    }),
    http.delete(`${baseUrl}/admin/v1/iam/roles/:id`, ({ request }) => {
      if (!request.headers.get('idempotency-key')) {
        return failure(40000, '写操作必须提供 Idempotency-Key', { traceId, status: 400 });
      }
      if (options.roleDeleteOutcome === 'approval') {
        return failure(40900, '高风险变更需审批', { traceId, status: 409 });
      }
      if (options.roleDeleteOutcome === 'serverError') {
        return failure(50000, '服务端错误', { traceId, status: 500 });
      }
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}

/** B 端契约 Mock：仅覆盖登录与会话相关端点，租户上下文由服务端推导（§6）。 */
export function createBizHandlers(options: HandlerOptions = {}): HttpHandler[] {
  const baseUrl = options.baseUrl ?? 'http://api.test';
  const traceId = options.traceId ?? 'test-trace';
  const meta: EnvelopeOptions = { traceId };

  return [
    http.post(`${baseUrl}/biz/v1/auth/login`, () => {
      if (options.loginOutcome === 'unauthorized') {
        return failure(40100, '账号或密码错误', { traceId, status: 401 });
      }
      return envelope(tokenPairFixture, meta);
    }),
    http.post(`${baseUrl}/biz/v1/auth/refresh`, () => envelope(tokenPairFixture, meta)),
    http.post(`${baseUrl}/biz/v1/auth/logout`, () => new HttpResponse(null, { status: 204 })),
    http.post(`${baseUrl}/biz/v1/auth/logout-all`, () => new HttpResponse(null, { status: 204 })),
    http.get(`${baseUrl}/biz/v1/iam/me/permissions`, () =>
      envelope(bizSubjectPermissionsFixture, meta),
    ),
  ];
}
