import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PageHeader } from './page-header';

describe('PageHeader', () => {
  it('渲染唯一 h1、描述与主操作区', () => {
    render(
      <PageHeader
        title="权限目录"
        description="只读列表"
        extra={<button type="button">新建</button>}
      />,
    );

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('权限目录');
    expect(screen.getByText('只读列表')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新建' })).toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: '面包屑' })).not.toBeInTheDocument();
  });

  it('面包屑使用可访问导航并区分链接与当前项', () => {
    render(
      <PageHeader
        title="权限目录"
        breadcrumb={[{ label: 'IAM', href: '/iam' }, { label: '权限' }]}
      />,
    );

    expect(screen.getByRole('navigation', { name: '面包屑' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'IAM' })).toHaveAttribute('href', '/iam');
    expect(screen.getByText('权限')).toBeInTheDocument();
  });
});
