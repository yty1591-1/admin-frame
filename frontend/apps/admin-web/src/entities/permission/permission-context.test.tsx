import { defaultTranslator } from '@admin-frame/i18n';
import { renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PermissionHarness } from '../../testing/auth-harness';
import { RequirePermission } from './permission-context';

const t = defaultTranslator;

function renderGuarded(
  status: 'ready' | 'unavailable' | 'loading',
  permissions: readonly string[] = [],
) {
  return renderWithProviders(
    <PermissionHarness status={status} permissions={permissions}>
      <RequirePermission permission="iam:role:read">
        <div>granted-content</div>
      </RequirePermission>
    </PermissionHarness>,
    { toast: false },
  );
}

describe('RequirePermission', () => {
  it('快照就绪且具备权限时渲染页面', () => {
    renderGuarded('ready', ['iam:role:read']);

    expect(screen.getByText('granted-content')).toBeInTheDocument();
  });

  it('缺少权限时原地渲染 403 文案', () => {
    renderGuarded('ready', ['iam:permission:read']);

    expect(screen.getByText(t('permission.state.deniedTitle'))).toBeInTheDocument();
    expect(screen.getByText(t('permission.state.deniedDescription'))).toBeInTheDocument();
    expect(screen.queryByText('granted-content')).not.toBeInTheDocument();
  });

  it('权限服务不可用时 fail-closed 并展示 trace_id', () => {
    renderGuarded('unavailable');

    expect(screen.getByText(t('permission.state.unavailableTitle'))).toBeInTheDocument();
    expect(screen.getByText(t('common.traceId', { traceId: 'test-trace' }))).toBeInTheDocument();
    expect(screen.queryByText('granted-content')).not.toBeInTheDocument();
  });

  it('快照加载中展示占位而非放行', () => {
    renderGuarded('loading');

    expect(screen.getByText(t('permission.state.loading'))).toBeInTheDocument();
    expect(screen.queryByText('granted-content')).not.toBeInTheDocument();
  });
});
