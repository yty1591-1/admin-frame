import { isAppError, type AdminIamApi, type Role } from '@admin-frame/api-client';
import { createIdempotencyKey } from '@admin-frame/shared';
import {
  Button,
  DangerActionConfirm,
  Input,
  Select,
  Space,
  StatusTag,
  TableShell,
  Typography,
  useToast,
  type DangerActionError,
  type TableColumnsType,
} from '@admin-frame/ui';
import { useEffect, useRef, useState } from 'react';
import { roleChannelMeta, roleStatusMeta, roleTypeMeta } from '../../entities/iam-role';
import { usePermission } from '../../entities/permission';
import { isApprovalRequired, useListParams } from '../../shared';
import { ApprovalNotice } from './ApprovalNotice';
import { useDeleteRole } from './use-delete-role';
import { useRoleList } from './use-role-list';

/** 必须是模块级常量：`useListParams` 以引用判断筛选键集合（§8.1）。 */
const FILTER_KEYS = ['keyword', 'status'] as const;

const STATUS_OPTIONS = [
  { value: 'active', label: '启用' },
  { value: 'disabled', label: '停用' },
] as const;

const DELETE_IMPACTS = [
  '立即解除该角色与全部主体的绑定关系',
  '绑定关系与历史审计记录按合规要求保留',
] as const;

export function RoleListPanel({ api }: { api: AdminIamApi }) {
  const { page, pageSize, filters, setPage, setPageSize, setFilter, resetFilters } =
    useListParams(FILTER_KEYS);
  const query = useRoleList(api, {
    page,
    pageSize,
    keyword: filters.keyword,
    status: filters.status as Role['status'] | undefined,
  });
  const canWrite = usePermission('iam:role:write');
  const toast = useToast();
  const deleteMutation = useDeleteRole(api);

  // 输入框允许中间态；提交（回车/查询/清空）后才写入 URL 触发请求。
  const [keyword, setKeyword] = useState(filters.keyword ?? '');
  useEffect(() => {
    setKeyword(filters.keyword ?? '');
  }, [filters.keyword]);

  const [target, setTarget] = useState<Role | null>(null);
  const [deleteError, setDeleteError] = useState<DangerActionError | null>(null);
  const [approval, setApproval] = useState<{ role: Role; reason: string } | null>(null);
  // 同一次提交的重试必须复用同一个 key：打开确认时生成，成功/取消后清空。
  const idempotencyKey = useRef<string | null>(null);

  const openDelete = (role: Role): void => {
    idempotencyKey.current = createIdempotencyKey();
    setDeleteError(null);
    setTarget(role);
  };

  const handleCancel = (): void => {
    if (deleteMutation.isPending) {
      return;
    }
    setTarget(null);
    setDeleteError(null);
    idempotencyKey.current = null;
  };

  const handleConfirm = (reason: string): void => {
    if (target === null) {
      return;
    }
    const current = target;
    const key = idempotencyKey.current ?? createIdempotencyKey();
    idempotencyKey.current = key;
    deleteMutation.mutate(
      { id: current.id, reason, idempotencyKey: key },
      {
        onSuccess: () => {
          toast.success(`角色「${current.name}」已删除`);
          setTarget(null);
          idempotencyKey.current = null;
        },
        onError: (error) => {
          // 40900：转审批引导，不自动重试、不按普通冲突处理（D2-05）。
          if (isApprovalRequired(error)) {
            setApproval({ role: current, reason });
            setTarget(null);
            idempotencyKey.current = null;
            return;
          }
          setDeleteError({
            message: '删除角色失败，请稍后重试。',
            traceId: isAppError(error) ? error.traceId : undefined,
          });
        },
      },
    );
  };

  const columns: TableColumnsType<Role> = [
    {
      title: '角色码',
      dataIndex: 'code',
      key: 'code',
      width: 200,
      render: (code: string) => <Typography.Text code>{code}</Typography.Text>,
    },
    { title: '名称', dataIndex: 'name', key: 'name' },
    {
      title: '端别',
      dataIndex: 'channel',
      key: 'channel',
      width: 100,
      render: (channel: Role['channel']) => (
        <StatusTag tone={roleChannelMeta[channel].tone} label={roleChannelMeta[channel].label} />
      ),
    },
    {
      title: '类型',
      dataIndex: 'role_type',
      key: 'role_type',
      width: 120,
      render: (type: Role['role_type']) => (
        <StatusTag tone={roleTypeMeta[type].tone} label={roleTypeMeta[type].label} />
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: Role['status']) => (
        <StatusTag tone={roleStatusMeta[status].tone} label={roleStatusMeta[status].label} />
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 100,
      render: (_value: unknown, record: Role) =>
        canWrite && record.role_type === 'custom' ? (
          <Button
            type="link"
            danger
            aria-label={`删除角色 ${record.name}`}
            onClick={() => {
              openDelete(record);
            }}
          >
            删除
          </Button>
        ) : null,
    },
  ];

  return (
    <>
      <TableShell<Role>
        columns={columns}
        rows={query.data?.items}
        rowKey="id"
        total={query.data?.total ?? 0}
        page={page}
        pageSize={pageSize}
        loading={query.isFetching}
        error={
          query.isError
            ? {
                message: '角色列表加载失败，请检查筛选条件或稍后重试。',
                traceId: isAppError(query.error) ? query.error.traceId : undefined,
              }
            : null
        }
        emptyText="暂无角色"
        toolbar={
          <Space wrap>
            <Input
              aria-label="角色关键字"
              allowClear
              placeholder="搜索角色码或名称"
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
              aria-label="状态"
              allowClear
              placeholder="全部状态"
              style={{ width: 160 }}
              options={[...STATUS_OPTIONS]}
              value={filters.status ?? null}
              onChange={(value) => {
                setFilter('status', value ?? null);
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
        }
        onPageChange={(nextPage, nextPageSize) => {
          if (nextPageSize !== pageSize) {
            setPageSize(nextPageSize);
            return;
          }
          setPage(nextPage);
        }}
        onRetry={() => {
          void query.refetch();
        }}
      />
      <DangerActionConfirm
        open={target !== null}
        title="删除角色"
        target={target === null ? '' : `${target.name}（${target.code}）`}
        impacts={DELETE_IMPACTS}
        loading={deleteMutation.isPending}
        error={deleteError}
        onConfirm={handleConfirm}
        onCancel={handleCancel}
      />
      <ApprovalNotice
        open={approval !== null}
        roleName={approval?.role.name ?? ''}
        roleCode={approval?.role.code ?? ''}
        reason={approval?.reason ?? ''}
        onClose={() => {
          setApproval(null);
        }}
      />
    </>
  );
}
