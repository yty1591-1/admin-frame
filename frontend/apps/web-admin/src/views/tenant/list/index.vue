<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { Tenant } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import { Button, Descriptions, Drawer, message, Tag } from 'ant-design-vue';

import { createTenantApi, getTenantApi, listTenantsApi } from '#/api';
import { AdminListTable } from '#/components/admin-list-table';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

import FormModal from '../../iam/components/form-modal.vue';

defineOptions({ name: 'TenantList' });

const [QueryForm, queryFormApi] = useVbenForm(
  withListQueryForm({
    handleReset: async () => {
      await queryFormApi.reset();
      handleSearch();
    },
    handleSubmit: async () => {
      handleSearch();
    },
    schema: [
      {
        component: 'Input',
        componentProps: { allowClear: true, placeholder: '输入关键词' },
        fieldName: 'keyword',
        label: '关键词',
      },
      {
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: '待开通', value: 'pending' },
            { label: '已开通', value: 'active' },
            { label: '已停用', value: 'suspended' },
            { label: '已终止', value: 'terminated' },
          ],
          placeholder: '状态',
        },
        fieldName: 'status',
        label: '状态',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<Tenant[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'name', key: 'name', title: '租户名称', width: 200 },
  { dataIndex: 'code', key: 'code', title: '租户编码', width: 140 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { dataIndex: 'plan_id', key: 'plan_id', title: '套餐 ID', width: 140 },
  { dataIndex: 'updated_at', key: 'updated_at', title: '更新于', width: 180 },
  { key: 'actions', title: '操作', width: 100 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  active: { color: 'success', label: '已开通' },
  pending: { color: 'warning', label: '待开通' },
  suspended: { color: 'default', label: '已停用' },
  terminated: { color: 'error', label: '已终止' },
};

const createOpen = ref(false);
const createSubmitting = ref(false);
const createFields: FormField[] = [
  { label: '租户名称', name: 'name', required: true, span: 2 },
  {
    label: '业务编码',
    name: 'code',
    placeholder: '小写字母、数字与 ._-',
    span: 2,
  },
  {
    label: '套餐编码',
    name: 'plan_code',
    placeholder: '省略时使用 standard',
    span: 2,
  },
  {
    label: '首名 owner 主体 ID',
    name: 'owner_subject_id',
    placeholder: '开通前须至少一名在册 owner',
    span: 2,
  },
];

const detailOpen = ref(false);
const detailLoading = ref(false);
const detail = ref<Tenant>();

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listTenantsApi({
      keyword: values.keyword || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: values.status || undefined,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

function handleSearch() {
  pagination.current = 1;
  fetchList();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function handleCreate(payload: Record<string, any>) {
  createSubmitting.value = true;
  try {
    await createTenantApi({
      code: payload.code || undefined,
      name: payload.name,
      owner_subject_id: payload.owner_subject_id || undefined,
      plan_code: payload.plan_code || undefined,
    });
    message.success('租户已创建（待开通）');
    createOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    createSubmitting.value = false;
  }
}

async function openDetail(record: Tenant) {
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    detail.value = await getTenantApi(record.id);
  } catch {
    detail.value = record;
  } finally {
    detailLoading.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <QueryForm class="min-w-0 flex-1" />
        <Button
          v-access:code="'tenant:write'"
          type="primary"
          @click="createOpen = true"
        >
          创建租户
        </Button>
      </div>

      <AdminListTable
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :pagination="pagination"
        row-key="id"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <div class="font-medium">{{ record.name }}</div>
            <div class="text-muted-foreground text-xs">{{ record.code }}</div>
          </template>
          <template v-else-if="column.key === 'status'">
            <Tag :color="statusMap[record.status]?.color ?? 'default'">
              {{ statusMap[record.status]?.label ?? record.status }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Button
              size="small"
              type="link"
              @click="openDetail(record as Tenant)"
            >
              详情
            </Button>
          </template>
        </template>
      </AdminListTable>
    </div>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      submit-text="创建"
      title="创建租户"
      @submit="handleCreate"
    />

    <Drawer v-model:open="detailOpen" title="租户详情" width="560">
      <Descriptions
        v-if="detail"
        :column="1"
        :loading="detailLoading"
        bordered
        size="small"
      >
        <Descriptions.Item label="租户名称">
          {{ detail.name }}
        </Descriptions.Item>
        <Descriptions.Item label="租户编码">
          {{ detail.code }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag :color="statusMap[detail.status]?.color ?? 'default'">
            {{ statusMap[detail.status]?.label ?? detail.status }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="套餐">
          {{ detail.plan_id || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="时区 / 语言">
          {{ detail.timezone || '—' }} / {{ detail.locale || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="联系人">
          {{ detail.contact_name || '—' }}
          {{ detail.contact_email ? `（${detail.contact_email}）` : '' }}
        </Descriptions.Item>
        <Descriptions.Item label="版本">
          v{{ detail.version }}
        </Descriptions.Item>
        <Descriptions.Item label="更新于">
          {{ detail.updated_at }}
        </Descriptions.Item>
      </Descriptions>
      <p class="text-muted-foreground mt-4 text-xs">
        成员、邀请、配额与入驻审批将在详情页签中继续补齐。
      </p>
    </Drawer>
  </Page>
</template>
