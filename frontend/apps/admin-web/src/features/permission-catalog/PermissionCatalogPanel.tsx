import { isAppError, type AdminIamApi, type Permission } from '@admin-frame/api-client';
import type { Channel } from '@admin-frame/shared';
import {
  Button,
  Card,
  ErrorState,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  type TableColumnsType,
} from '@admin-frame/ui';
import { useEffect, useState } from 'react';
import { useListParams } from '../../shared';
import { usePermissionCatalog } from './use-permission-catalog';

/** 必须是模块级常量：`useListParams` 以引用判断筛选键集合（§8.1）。 */
const FILTER_KEYS = ['keyword', 'channel'] as const;

const CHANNEL_OPTIONS = [
  { value: 'admin', label: 'A 端' },
  { value: 'biz', label: 'B 端' },
] as const;

const CHANNEL_LABELS: Record<Permission['channel'], string> = {
  admin: 'A 端',
  biz: 'B 端',
  both: '双端',
};

const KIND_LABELS: Record<Permission['kind'], string> = {
  api: '接口',
  menu: '菜单',
  button: '按钮',
};

const PAGE_SIZE_OPTIONS = ['20', '50', '100'];

const columns: TableColumnsType<Permission> = [
  {
    title: '权限码',
    dataIndex: 'code',
    key: 'code',
    width: 260,
    render: (code: string) => <Typography.Text code>{code}</Typography.Text>,
  },
  { title: '名称', dataIndex: 'name', key: 'name' },
  {
    title: '端别',
    dataIndex: 'channel',
    key: 'channel',
    width: 100,
    render: (channel: Permission['channel']) => CHANNEL_LABELS[channel],
  },
  {
    title: '类型',
    dataIndex: 'kind',
    key: 'kind',
    width: 100,
    render: (kind: Permission['kind']) => KIND_LABELS[kind],
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    width: 100,
    render: (status: Permission['status']) =>
      status === 'active' ? <Tag color="green">启用</Tag> : <Tag>已废弃</Tag>,
  },
];

export function PermissionCatalogPanel({ api }: { api: AdminIamApi }) {
  const { page, pageSize, filters, setPage, setPageSize, setFilter, resetFilters } =
    useListParams(FILTER_KEYS);
  const query = usePermissionCatalog(api, {
    page,
    pageSize,
    keyword: filters.keyword,
    channel: filters.channel as Channel | undefined,
  });
  // 输入框允许中间态；提交（回车/查询/清空）后才写入 URL 触发请求。
  const [keyword, setKeyword] = useState(filters.keyword ?? '');

  useEffect(() => {
    setKeyword(filters.keyword ?? '');
  }, [filters.keyword]);

  if (query.isError) {
    return (
      <ErrorState
        title="权限目录加载失败"
        description="请检查筛选条件或稍后重试。"
        traceId={isAppError(query.error) ? query.error.traceId : undefined}
        onRetry={() => {
          void query.refetch();
        }}
      />
    );
  }

  return (
    <Card>
      <Space wrap style={{ marginBottom: 16 }}>
        <Input
          aria-label="权限关键字"
          allowClear
          placeholder="搜索权限码或名称"
          style={{ width: 280 }}
          value={keyword}
          onChange={(event) => {
            const next = event.target.value;
            setKeyword(next);
            if (next === '') {
              setFilter('keyword', null);
            }
          }}
          onPressEnter={() => {
            setFilter('keyword', keyword);
          }}
        />
        <Button
          type="primary"
          onClick={() => {
            setFilter('keyword', keyword);
          }}
        >
          查询
        </Button>
        <Select<string>
          aria-label="端别"
          allowClear
          placeholder="全部端别"
          style={{ width: 160 }}
          options={[...CHANNEL_OPTIONS]}
          value={filters.channel ?? null}
          onChange={(value) => {
            setFilter('channel', value ?? null);
          }}
        />
        <Button
          onClick={() => {
            resetFilters();
          }}
        >
          重置
        </Button>
      </Space>
      <Table<Permission>
        rowKey="code"
        size="middle"
        columns={columns}
        dataSource={query.data?.items ?? []}
        loading={query.isPending}
        pagination={{
          current: page,
          pageSize,
          total: query.data?.total ?? 0,
          showSizeChanger: true,
          pageSizeOptions: PAGE_SIZE_OPTIONS,
          showTotal: (total) => `共 ${total} 条`,
          onChange: (nextPage, nextPageSize) => {
            if (nextPageSize !== pageSize) {
              setPageSize(nextPageSize);
              return;
            }
            setPage(nextPage);
          },
        }}
      />
    </Card>
  );
}
