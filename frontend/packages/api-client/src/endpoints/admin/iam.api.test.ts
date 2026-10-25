import { HttpResponse, http } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { createAdminClient } from '../../admin-client';
import { AppError, isApprovalRequired } from '../../errors/app-error';

const BASE_URL = 'http://api.test';

const rolePayload = {
  id: 'role-1',
  channel: 'admin',
  scope_type: 'admin',
  code: 'ops',
  name: '运营',
  role_type: 'custom',
  status: 'active',
  version: 3,
  permissions: ['iam:role:read'],
  data_scopes: [{ resource: 'iam', scope_type: 'all' }],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
};

function ok(data: unknown) {
  return HttpResponse.json({ code: 0, message: 'ok', trace_id: 'trace-1', data });
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

describe('AdminIamApi 角色端点', () => {
  it('角色列表按分页与筛选参数请求并校验契约', async () => {
    server.use(
      http.get(`${BASE_URL}/admin/v1/iam/roles`, ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get('page')).toBe('2');
        expect(url.searchParams.get('page_size')).toBe('50');
        expect(url.searchParams.get('status')).toBe('active');
        expect(url.searchParams.get('keyword')).toBe('运营');
        return ok({ items: [rolePayload], total: 1 });
      }),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    const data = await client.iam.listRoles({
      page: 2,
      pageSize: 50,
      status: 'active',
      keyword: '运营',
    });

    expect(data.total).toBe(1);
    expect(data.items[0]?.code).toBe('ops');
  });

  it('删除角色注入幂等键与原因', async () => {
    server.use(
      http.delete(`${BASE_URL}/admin/v1/iam/roles/role-1`, async ({ request }) => {
        expect(request.headers.get('idempotency-key')).toBe('idem-1');
        expect(await request.json()).toEqual({ reason: '离职交接' });
        return ok({ id: 'role-1' });
      }),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    await expect(
      client.iam.deleteRole('role-1', { reason: '离职交接', idempotencyKey: 'idem-1' }),
    ).resolves.toBeUndefined();
  });

  it('缺少幂等键时在发请求前即拒绝', async () => {
    const client = createAdminClient({ baseUrl: BASE_URL });

    await expect(client.iam.deleteRole('role-1', { idempotencyKey: '' })).rejects.toMatchObject({
      code: 40000,
      kind: 'validation',
    });
  });

  it('40900 归类为审批引导且保留 trace_id', async () => {
    server.use(
      http.delete(`${BASE_URL}/admin/v1/iam/roles/role-1`, () =>
        HttpResponse.json(
          { code: 40900, message: '高风险变更需审批', trace_id: 'trace-40900' },
          { status: 409 },
        ),
      ),
    );
    const client = createAdminClient({ baseUrl: BASE_URL });

    const error: unknown = await client.iam
      .deleteRole('role-1', { reason: '离职交接', idempotencyKey: 'idem-1' })
      .catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(AppError);
    expect(error).toMatchObject({ code: 40900, kind: 'conflict', traceId: 'trace-40900' });
    expect(isApprovalRequired(error)).toBe(true);
  });
});
