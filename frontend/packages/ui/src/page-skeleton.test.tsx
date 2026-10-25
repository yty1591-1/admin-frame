import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PageSkeleton } from './page-skeleton';

describe('PageSkeleton', () => {
  it('暴露可访问的加载状态占位', () => {
    const { container } = render(<PageSkeleton />);

    expect(screen.getByRole('status', { name: '页面加载中' })).toBeInTheDocument();
    expect(container.querySelector('.ant-skeleton')).not.toBeNull();
  });

  it('rows 控制段落占位行数', () => {
    const { container } = render(<PageSkeleton rows={2} />);

    expect(container.querySelectorAll('.ant-skeleton-paragraph > li')).toHaveLength(2);
  });
});
