import { Card, Space, Typography } from '@admin-frame/ui';

export default function HomePage() {
  return (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Card title="工程底座（P0）">
        <Typography.Paragraph>
          前端 monorepo、契约类型生成、分层边界、测试与 CI 门禁已就绪。
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary">
          契约版本：{__CONTRACT_COMMIT__}
        </Typography.Paragraph>
      </Card>
    </Space>
  );
}
