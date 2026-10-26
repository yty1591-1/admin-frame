import type { AdminIamApi, Role } from '@admin-frame/api-client';
import { describeError, useI18n, type MessageKey } from '@admin-frame/i18n';
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

const STATUS_VALUES = ['active', 'disabled'] as const;

const DELETE_IMPACT_KEYS: readonly MessageKey[] = [
  'iam.role.deleteImpact.bindings',
  'iam.role.deleteImpact.audit',
];

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
  const { t } = useI18n();
  const deleteMutation = useDeleteRole(api);
  const listError = query.isError ? describeError(query.error) : null;

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
          toast.success(t('iam.role.deleteSuccess', { name: current.name }));
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
          const presentation = describeError(error);
          setDeleteError({
            message: t(presentation.messageKey),
            traceId: presentation.traceId,
          });
        },
      },
    );
  };

  const columns: TableColumnsType<Role> = [
    {
      title: t('iam.role.col.code'),
      dataIndex: 'code',
      key: 'code',
      width: 200,
      render: (code: string) => <Typography.Text code>{code}</Typography.Text>,
    },
    { title: t('iam.role.col.name'), dataIndex: 'name', key: 'name' },
    {
      title: t('iam.role.col.channel'),
      dataIndex: 'channel',
      key: 'channel',
      width: 100,
      render: (channel: Role['channel']) => (
        <StatusTag
          tone={roleChannelMeta[channel].tone}
          label={t(roleChannelMeta[channel].labelKey)}
        />
      ),
    },
    {
      title: t('iam.role.col.type'),
      dataIndex: 'role_type',
      key: 'role_type',
      width: 120,
      render: (type: Role['role_type']) => (
        <StatusTag tone={roleTypeMeta[type].tone} label={t(roleTypeMeta[type].labelKey)} />
      ),
    },
    {
      title: t('iam.role.col.status'),
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: Role['status']) => (
        <StatusTag tone={roleStatusMeta[status].tone} label={t(roleStatusMeta[status].labelKey)} />
      ),
    },
    {
      title: t('iam.role.col.actions'),
      key: 'actions',
      width: 100,
      render: (_value: unknown, record: Role) =>
        canWrite && record.role_type === 'custom' ? (
          <Button
            type="link"
            danger
            aria-label={t('iam.role.deleteAria', { name: record.name })}
            onClick={() => {
              openDelete(record);
            }}
          >
            {t('common.delete')}
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
          listError === null
            ? null
            : {
                message: t(listError.messageKey),
                traceId: listError.traceId,
              }
        }
        emptyText={t('iam.role.empty')}
        toolbar={
          <Space wrap>
            <Input
              aria-label={t('iam.role.keywordAria')}
              allowClear
              placeholder={t('iam.role.searchPlaceholder')}
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
              {t('common.search')}
            </Button>
            <Select<string>
              aria-label={t('iam.role.statusAria')}
              allowClear
              placeholder={t('iam.role.statusAll')}
              style={{ width: 160 }}
              options={STATUS_VALUES.map((value) => ({
                value,
                label: t(roleStatusMeta[value].labelKey),
              }))}
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
              {t('common.reset')}
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
        title={t('iam.role.deleteTitle')}
        target={
          target === null
            ? ''
            : t('common.targetWithCode', { name: target.name, code: target.code })
        }
        impacts={DELETE_IMPACT_KEYS.map((key) => t(key))}
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
