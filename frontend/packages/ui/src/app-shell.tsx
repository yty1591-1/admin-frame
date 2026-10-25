import { Button, Layout, Space, Typography } from 'antd';
import type { CSSProperties, ReactNode } from 'react';

export type AppShellMenuEntry = {
  key: string;
  label: string;
};

export type AppShellProps = {
  title: string;
  subtitle?: string | undefined;
  menus?: readonly AppShellMenuEntry[] | undefined;
  selectedKey?: string | undefined;
  onSelect?: ((key: string) => void) | undefined;
  children: ReactNode;
};

const headerStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'baseline',
  gap: 12,
  background: '#fff',
  borderBottom: '1px solid #f0f0f0',
};

/** 中后台外壳：只负责布局与菜单渲染，不感知路由、权限与数据。 */
export function AppShell({
  title,
  subtitle,
  menus = [],
  selectedKey,
  onSelect,
  children,
}: AppShellProps) {
  const { Header, Sider, Content } = Layout;
  return (
    <Layout style={{ minHeight: '100vh' }}>
      {menus.length > 0 ? (
        <Sider theme="light" width={220}>
          <nav aria-label="主导航" style={{ padding: 12 }}>
            <Space direction="vertical" size={4} style={{ width: '100%' }}>
              {menus.map((entry) => (
                <Button
                  key={entry.key}
                  type={entry.key === selectedKey ? 'primary' : 'text'}
                  block
                  aria-current={entry.key === selectedKey ? 'page' : undefined}
                  onClick={() => {
                    onSelect?.(entry.key);
                  }}
                >
                  {entry.label}
                </Button>
              ))}
            </Space>
          </nav>
        </Sider>
      ) : null}
      <Layout>
        <Header style={headerStyle}>
          <Typography.Title level={1} style={{ fontSize: 18, margin: 0 }}>
            {title}
          </Typography.Title>
          {subtitle ? <Typography.Text type="secondary">{subtitle}</Typography.Text> : null}
        </Header>
        <Content style={{ padding: 24 }}>{children}</Content>
      </Layout>
    </Layout>
  );
}
