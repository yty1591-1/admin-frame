import { PageHeader } from '@admin-frame/ui';
import { useAuth } from '../app/auth/auth-context';
import { PermissionCatalogPanel } from '../features/permission-catalog';

export default function PermissionCatalogPage() {
  const { client } = useAuth();
  return (
    <>
      <PageHeader title="权限目录" description="平台已注册的权限点；权限码由后端定义，前端只读。" />
      <PermissionCatalogPanel api={client.iam} />
    </>
  );
}
