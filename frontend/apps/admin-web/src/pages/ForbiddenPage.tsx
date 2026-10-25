import { ErrorState } from '@admin-frame/ui';

export default function ForbiddenPage() {
  return (
    <ErrorState
      title="无访问权限"
      description="当前账号缺少访问该页面的权限；如需开通请联系平台管理员。"
    />
  );
}
