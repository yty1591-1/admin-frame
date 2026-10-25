import { HttpResponse, delay, http as mswHttp } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { createAdminClient } from '../admin-client';
import { AppError } from '../errors/app-error';

const BASE_URL = 'http://api.test';

const permissionsPayload = {
  channel: 'admin',
  subject_type: 'staff',
  subject_id: 'staff-1',
  roles: ['platform-admin'],
  permissions: ['iam:role:read'],
  data_scopes: [{ resource: '*', scope_type: 'all' }],
  snapshot_version: 3,
  generated_at: '2026-01-01T00:00:00Z',
};

const tokenPairPayload = {
  access_token: 'access-1',
  refresh_token: 'refresh-1',
  token_type: 'Bearer',
  expires_in: 600,
};

function ok(data: unknown) {
  return HttpResponse.json({ code: 0, message: 'ok', trace_id: 'trace-1', data });
}

function fail(code: number, status: number) {
  return HttpResponse.json({ code, message: 'denied', trace_id: 'trace-9' }, { status });
}

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

describe('HttpClient', () => {
  it('解包信封并注入鉴权头', async () => {
    server.use(
      mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, ({ request }) => {
        expect(request.headers.get('authorization')).toBe('Bearer access-1');
        return ok(permissionsPayload);
      }),
    );
    const client = createAdminClient({
      baseUrl: BASE_URL,
      getAccessToken: () => 'access-1',
    });

    const snapshot = await client.iam.mePermissions();

    expect(snapshot.permissions).toEqual(['iam:role:read']);
    expect(snapshot.channel).toBe('admin');
  });

  it('登录响应窄化为 MFA 挑战', async () => {
    server.use(
      mswHttp.post(`${BASE_URL}/admin/v1/auth/login`, () =>
        ok({ mfa_required: true, mfa_ticket: 'ticket-1' }),
      ),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    const outcome = await client.auth.login('alice@example.com', 'secret');

    expect(outcome.kind).toBe('mfa');
    if (outcome.kind === 'mfa') {
      expect(outcome.challenge.mfa_ticket).toBe('ticket-1');
    }
  });

  it('登录响应窄化为令牌对', async () => {
    server.use(mswHttp.post(`${BASE_URL}/admin/v1/auth/login`, () => ok(tokenPairPayload)));
    const client = createAdminClient({ baseUrl: BASE_URL });

    const outcome = await client.auth.login('alice@example.com', 'secret');

    expect(outcome.kind).toBe('tokens');
  });

  it('业务错误映射为 AppError 并保留 trace_id', async () => {
    server.use(mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, () => fail(40300, 403)));
    const client = createAdminClient({ baseUrl: BASE_URL });

    const error = await client.iam.mePermissions().catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(AppError);
    if (error instanceof AppError) {
      expect(error.kind).toBe('forbidden');
      expect(error.code).toBe(40300);
      expect(error.traceId).toBe('trace-9');
    }
  });

  it('40100 触发上层刷新回调', async () => {
    server.use(mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, () => fail(40100, 401)));
    const onUnauthorized = vi.fn();
    const client = createAdminClient({ baseUrl: BASE_URL, onUnauthorized });

    await expect(client.iam.mePermissions()).rejects.toBeInstanceOf(AppError);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('响应数据不符合契约时按服务端错误处理', async () => {
    server.use(
      mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, () =>
        ok({ ...permissionsPayload, permissions: 'not-an-array' }),
      ),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    const error = await client.iam.mePermissions().catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(AppError);
    if (error instanceof AppError) {
      expect(error.kind).toBe('server');
    }
  });

  it('超时归类为 timeout', async () => {
    server.use(
      mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, async () => {
        await delay(80);
        return ok(permissionsPayload);
      }),
    );
    const client = createAdminClient({ baseUrl: BASE_URL, timeoutMs: 10 });

    const error = await client.iam.mePermissions().catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(AppError);
    if (error instanceof AppError) {
      expect(error.kind).toBe('timeout');
    }
  });

  it('网络失败按 network 归类', async () => {
    server.use(mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, () => HttpResponse.error()));
    const client = createAdminClient({ baseUrl: BASE_URL });

    const error = await client.iam.mePermissions().catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(AppError);
    if (error instanceof AppError) {
      expect(error.kind).toBe('network');
    }
  });

  it('幂等 GET 在 5xx 后重试一次并成功', async () => {
    let calls = 0;
    server.use(
      mswHttp.get(`${BASE_URL}/admin/v1/iam/me/permissions`, () => {
        calls += 1;
        return calls === 1 ? fail(50000, 500) : ok(permissionsPayload);
      }),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    const snapshot = await client.iam.mePermissions();

    expect(calls).toBe(2);
    expect(snapshot.subject_id).toBe('staff-1');
  });

  it('缺少 Idempotency-Key 的写操作被拒绝', async () => {
    const client = createAdminClient({ baseUrl: BASE_URL });

    await expect(
      client.http.request('/admin/v1/auth/logout', {
        method: 'POST',
        requireIdempotencyKey: true,
      }),
    ).rejects.toMatchObject({ kind: 'validation', code: 40000 });
  });

  it('204 无响应体按空结果处理', async () => {
    server.use(
      mswHttp.post(
        `${BASE_URL}/admin/v1/auth/logout`,
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    await expect(client.auth.logout()).resolves.toBeUndefined();
  });
});
