import '@testing-library/jest-dom/vitest';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { AppShell } from './app-shell';

describe('AppShell', () => {
  it('渲染标题与菜单，并回调选中项', async () => {
    const onSelect = vi.fn();
    render(
      <AppShell
        title="A 端运营后台"
        subtitle="环境：local"
        menus={[
          { key: '/', label: '工作台' },
          { key: '/iam/roles', label: '角色管理' },
        ]}
        selectedKey="/"
        onSelect={onSelect}
      >
        <p>内容区</p>
      </AppShell>,
    );

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('A 端运营后台');
    expect(screen.getByText('内容区')).toBeInTheDocument();

    await userEvent.click(screen.getByText('角色管理'));
    expect(onSelect).toHaveBeenCalledWith('/iam/roles');
  });

  it('无菜单时不渲染侧栏', () => {
    render(
      <AppShell title="B 端商户后台">
        <p>内容区</p>
      </AppShell>,
    );
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });
});
