import { Space, Typography } from '@admin-frame/ui';
import { useAuth } from '../app/auth/auth-context';
import { PermissionCatalogPanel } from '../features/permission-catalog';

export default function PermissionCatalogPage() {
  const { client } = useAuth();
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Typography.Title level={4} style={{ margin: 0 }}>
        权限目录
      </Typography.Title>
      <PermissionCatalogPanel api={client.iam} />
    </Space>
  );
}
