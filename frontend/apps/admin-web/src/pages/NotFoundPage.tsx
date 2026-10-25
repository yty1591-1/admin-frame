import { ErrorState } from '@admin-frame/ui';

export default function NotFoundPage() {
  return <ErrorState title="页面不存在" description="请检查地址或返回工作台。" />;
}
