import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { EmptyState } from './empty-state';
import { ErrorState } from './error-state';
import { PageLoading } from './page-loading';

describe('状态组件', () => {
  it('PageLoading 暴露可访问的加载状态', () => {
    render(<PageLoading />);
    expect(screen.getByRole('status')).toHaveTextContent('加载中');
  });

  it('EmptyState 默认为无数据文案', () => {
    render(<EmptyState />);
    expect(screen.getByText('暂无数据')).toBeInTheDocument();
  });

  it('ErrorState 展示 trace_id 并支持重试', async () => {
    const onRetry = vi.fn();
    render(
      <ErrorState title="加载失败" description="请稍后再试" traceId="trace-1" onRetry={onRetry} />,
    );

    expect(screen.getByText('加载失败')).toBeInTheDocument();
    expect(screen.getByText(/trace-1/)).toBeInTheDocument();

    // antd 会在两个汉字之间插入空格，故用正则匹配可访问名。
    await userEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
