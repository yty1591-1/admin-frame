import { createAdminClient } from '@admin-frame/api-client';
import { defaultTranslator } from '@admin-frame/i18n';
import {
  createAdminHandlers,
  createTestServer,
  renderWithProviders,
  roleListFixture,
  TEST_API_BASE_URL,
} from '@admin-frame/testing';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { PermissionHarness } from '../../testing/auth-harness';
import { RoleListPanel } from './RoleListPanel';

const t = defaultTranslator;
const client = createAdminClient({ baseUrl: TEST_API_BASE_URL });
const api = client.iam;
const server = createTestServer(createAdminHandlers());

// 角色名直接取夹具，避免测试与契约数据各写一份中文。
const customRole = roleListFixture.items[0];
const systemRole = roleListFixture.items[1];
if (customRole === undefined || systemRole === undefined) {
  throw new Error('roleListFixture must contain both roles');
}

type CapturedRequest = {
  method: string;
  url: string;
  idempotencyKey: string | null;
};

let requests: CapturedRequest[] = [];

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
  server.events.on('request:start', ({ request }) => {
    requests.push({
      method: request.method,
      url: request.url,
      idempotencyKey: request.headers.get('idempotency-key'),
    });
  });
});

beforeEach(() => {
  requests = [];
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

function renderPanel(permissions: readonly string[] = ['iam:role:read']) {
  return renderWithProviders(
    <PermissionHarness permissions={permissions}>
      <RoleListPanel api={api} />
    </PermissionHarness>,
    { initialEntries: ['/iam/roles'] },
  );
}

/** antd 会在两个汉字之间插入空格，可访问名匹配需要放宽。 */
function looseName(text: string): RegExp {
  return new RegExp([...text].join('\\s*'));
}

const deleteAria = looseName(t('iam.role.deleteAria', { name: customRole.name }));
const confirmName = looseName(t('iam.role.deleteTitle'));
const reasonLabel = looseName(t('common.reasonLabel'));
const resetName = looseName(t('common.reset'));

async function openDeleteDialog(user: ReturnType<typeof userEvent.setup>): Promise<HTMLElement> {
  await user.click(screen.getByRole('button', { name: deleteAria }));
  return await screen.findByRole('dialog');
}

describe('RoleListPanel', () => {
  it('加载角色列表并展示渠道、类型与状态', async () => {
    renderPanel();

    expect(await screen.findByText(customRole.name)).toBeInTheDocument();
    expect(screen.getByText(systemRole.name)).toBeInTheDocument();
    expect(screen.getByText(t('iam.role.type.system'))).toBeInTheDocument();
    expect(screen.getByText(t('iam.role.type.custom'))).toBeInTheDocument();
  });

  it('无写权限时不渲染删除入口', async () => {
    renderPanel(['iam:role:read']);

    await screen.findByText(customRole.name);
    expect(screen.queryByRole('button', { name: deleteAria })).not.toBeInTheDocument();
  });

  it('有写权限时删除自定义角色需填写原因，成功后提示并复用幂等键', async () => {
    const user = userEvent.setup();
    renderPanel(['iam:role:read', 'iam:role:write']);
    await screen.findByText(customRole.name);

    const dialog = await openDeleteDialog(user);
    const confirm = within(dialog).getByRole('button', { name: confirmName });
    expect(confirm).toBeDisabled();

    await user.type(within(dialog).getByLabelText(reasonLabel), 'deprecated role');
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    expect(
      await screen.findByText(t('iam.role.deleteSuccess', { name: customRole.name })),
    ).toBeInTheDocument();

    const deletes = requests.filter((entry) => entry.method === 'DELETE');
    expect(deletes).toHaveLength(1);
    expect(deletes[0]?.idempotencyKey).toBeTruthy();
  });

  it('40900 转审批引导且不自动重试', async () => {
    server.use(...createAdminHandlers({ roleDeleteOutcome: 'approval' }));
    const user = userEvent.setup();
    renderPanel(['iam:role:read', 'iam:role:write']);
    await screen.findByText(customRole.name);

    const dialog = await openDeleteDialog(user);
    await user.type(within(dialog).getByLabelText(reasonLabel), 'deprecated role');
    await user.click(within(dialog).getByRole('button', { name: confirmName }));

    expect(await screen.findByText(t('iam.approval.title'))).toBeInTheDocument();
    expect(
      screen.getByText(t('iam.approval.content', { name: customRole.name })),
    ).toBeInTheDocument();

    // 40900 只提交一次，不按普通冲突重试（D2-05）。
    expect(requests.filter((entry) => entry.method === 'DELETE')).toHaveLength(1);
  });

  it('列表加载失败时展示统一错误态与 trace_id', async () => {
    server.use(...createAdminHandlers({ roleListOutcome: 'serverError' }));
    renderPanel();

    expect(await screen.findByText(t('common.listLoadFailedTitle'))).toBeInTheDocument();
    expect(screen.getByText(t('common.traceId', { traceId: 'test-trace' }))).toBeInTheDocument();
  });

  it('关键字查询写入 URL 并携带到请求参数', async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText(customRole.name);

    await user.type(screen.getByLabelText(t('iam.role.keywordAria')), 'ops');
    await user.keyboard('{Enter}');

    await waitFor(() => {
      const listRequests = requests.filter(
        (entry) => entry.method === 'GET' && entry.url.includes('/iam/roles'),
      );
      expect(listRequests.some((entry) => entry.url.includes('keyword=ops'))).toBe(true);
    });
  });

  it('重置按钮清空关键字筛选', async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText(customRole.name);

    await user.type(screen.getByLabelText(t('iam.role.keywordAria')), 'ops');
    await user.keyboard('{Enter}');
    await waitFor(() => {
      expect(requests.some((entry) => entry.url.includes('keyword=ops'))).toBe(true);
    });

    await user.click(screen.getByRole('button', { name: resetName }));

    await waitFor(() => {
      const listRequests = requests.filter(
        (entry) => entry.method === 'GET' && entry.url.includes('/iam/roles'),
      );
      expect(listRequests.at(-1)?.url.includes('keyword=')).toBe(false);
    });
  });
});
