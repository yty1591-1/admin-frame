import { PageHeader } from '@admin-frame/ui';
import { useAuth } from '../app/auth/auth-context';
import { RoleListPanel } from '../features/iam-roles';

export default function IamRoleListPage() {
  const { client } = useAuth();
  return (
    <>
      <PageHeader
        title="角色管理"
        description="查看平台与商户角色；删除属高风险变更，需填写原因并按审批流程执行。"
      />
      <RoleListPanel api={client.iam} />
    </>
  );
}
