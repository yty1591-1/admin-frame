import { Space, Typography } from 'antd';
import type { ReactNode } from 'react';

export type PageHeaderBreadcrumbItem = {
  label: string;
  href?: string | undefined;
};

export type PageHeaderProps = {
  title: string;
  description?: string | undefined;
  /** 只做展示，不解析路由，避免设计系统依赖 router（§3.1）。 */
  breadcrumb?: readonly PageHeaderBreadcrumbItem[] | undefined;
  /** 主操作区，例如「新建」按钮。 */
  extra?: ReactNode | undefined;
};

/** 页面头部：标题（页面内唯一 h1）、描述、面包屑与操作区。 */
export function PageHeader({ title, description, breadcrumb, extra }: PageHeaderProps) {
  const items = breadcrumb ?? [];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
      {items.length > 0 ? (
        <nav aria-label="面包屑">
          <ol
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              gap: 4,
              margin: 0,
              padding: 0,
              listStyle: 'none',
            }}
          >
            {items.map((item, index) => (
              <li key={`${item.label}-${String(index)}`} style={{ display: 'inline-flex', gap: 4 }}>
                {item.href === undefined ? (
                  <Typography.Text type="secondary">{item.label}</Typography.Text>
                ) : (
                  <Typography.Link href={item.href}>{item.label}</Typography.Link>
                )}
                {index < items.length - 1 ? (
                  <Typography.Text type="secondary" aria-hidden>
                    /
                  </Typography.Text>
                ) : null}
              </li>
            ))}
          </ol>
        </nav>
      ) : null}
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 16,
        }}
      >
        <Space direction="vertical" size={4}>
          <Typography.Title level={1} style={{ margin: 0, fontSize: 20 }}>
            {title}
          </Typography.Title>
          {description === undefined ? null : (
            <Typography.Text type="secondary">{description}</Typography.Text>
          )}
        </Space>
        {extra === undefined ? null : <div>{extra}</div>}
      </div>
    </div>
  );
}
