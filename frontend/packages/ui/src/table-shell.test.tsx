import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import type { TableColumnsType } from 'antd';
import { describe, expect, it, vi } from 'vitest';
import { TableShell, type TableShellProps } from './table-shell';

type Row = { id: string; name: string };

const columns: TableColumnsType<Row> = [
  { title: '标识', dataIndex: 'id', key: 'id' },
  { title: '名称', dataIndex: 'name', key: 'name' },
];

const rows: Row[] = [
  { id: 'role-admin', name: '平台管理员' },
  { id: 'role-ops', name: '运营' },
];

function renderShell(props: Partial<TableShellProps<Row>> = {}) {
  return render(
    <TableShell<Row>
      columns={columns}
      rows={rows}
      rowKey="id"
      total={45}
      page={1}
      pageSize={20}
      loading={false}
      onPageChange={vi.fn()}
      {...props}
    />,
  );
}

describe('TableShell', () => {
  it('错误态展示 trace_id 并支持重试', async () => {
    const onRetry = vi.fn();
    renderShell({
      rows: undefined,
      total: 0,
      error: { message: '权限目录加载失败，请稍后重试。', traceId: 'trace-9' },
      onRetry,
    });

    expect(screen.getByText('权限目录加载失败，请稍后重试。')).toBeInTheDocument();
    expect(screen.getByText(/trace-9/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('首屏未加载时渲染骨架而非空态', () => {
    renderShell({ rows: undefined, total: 0, loading: true });

    expect(screen.getByRole('status', { name: '页面加载中' })).toBeInTheDocument();
  });

  it('空数据使用调用方指定的空态文案', () => {
    renderShell({ rows: [], total: 0, emptyText: '暂无角色' });

    expect(screen.getByText('暂无角色')).toBeInTheDocument();
  });

  it('翻页回调携带页码与页大小', async () => {
    const onPageChange = vi.fn();
    renderShell({ onPageChange });

    await userEvent.click(screen.getByTitle('2'));
    expect(onPageChange).toHaveBeenCalledWith(2, 20);
  });
});
