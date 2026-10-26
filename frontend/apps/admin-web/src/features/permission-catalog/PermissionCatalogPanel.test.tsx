import { createAdminClient } from '@admin-frame/api-client';
import { defaultTranslator } from '@admin-frame/i18n';
import {
  createAdminHandlers,
  createTestServer,
  permissionListFixture,
  renderWithProviders,
  TEST_API_BASE_URL,
} from '@admin-frame/testing';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { PermissionHarness } from '../../testing/auth-harness';
import { PermissionCatalogPanel } from './PermissionCatalogPanel';

const t = defaultTranslator;
const client = createAdminClient({ baseUrl: TEST_API_BASE_URL });
const api = client.iam;
const server = createTestServer(createAdminHandlers());

// 权限码与名称取夹具，避免测试重复维护中文文案。
const readPermission = permissionListFixture.items[0];
if (readPermission === undefined) {
  throw new Error('permissionListFixture must contain a permission');
}

/** antd 会在两个汉字之间插入空格，按钮可访问名匹配需要放宽。 */
function looseName(text: string): RegExp {
  return new RegExp([...text].join('\\s*'));
}

const resetName = looseName(t('common.reset'));

let listUrls: string[] = [];

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
  server.events.on('request:start', ({ request }) => {
    if (request.url.includes('/iam/permissions')) {
      listUrls.push(request.url);
    }
  });
});

beforeEach(() => {
  listUrls = [];
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

function renderCatalog() {
  return renderWithProviders(
    <PermissionHarness permissions={['iam:permission:read']}>
      <PermissionCatalogPanel api={api} />
    </PermissionHarness>,
    { initialEntries: ['/iam/permissions'] },
  );
}

describe('PermissionCatalogPanel', () => {
  it('加载权限目录并渲染权限码', async () => {
    renderCatalog();

    expect(await screen.findByText(readPermission.code)).toBeInTheDocument();
    expect(screen.getByText(readPermission.name)).toBeInTheDocument();
    expect(screen.getByText(t('permission.catalog.kind.api'))).toBeInTheDocument();
    expect(screen.getAllByText(t('permission.catalog.channel.admin'))).toHaveLength(2);
  });

  it('关键字查询写入 URL，重置后清除筛选参数', async () => {
    const user = userEvent.setup();
    renderCatalog();
    await screen.findByText(readPermission.code);

    await user.type(screen.getByLabelText(t('permission.catalog.keywordAria')), 'iam:role');
    await user.keyboard('{Enter}');

    await waitFor(() => {
      expect(listUrls.some((url) => url.includes('keyword=iam%3Arole'))).toBe(true);
    });

    await user.click(screen.getByRole('button', { name: resetName }));

    await waitFor(() => {
      expect(listUrls.at(-1)?.includes('keyword=')).toBe(false);
    });
  });

  it('依赖不可用时展示错误态与 trace_id', async () => {
    server.use(...createAdminHandlers({ permissionListOutcome: 'serverError' }));
    renderCatalog();

    expect(await screen.findByText(t('common.listLoadFailedTitle'))).toBeInTheDocument();
    expect(screen.getByText(t('common.traceId', { traceId: 'test-trace' }))).toBeInTheDocument();
  });
});
