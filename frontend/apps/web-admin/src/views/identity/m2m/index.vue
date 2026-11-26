<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { M2MClient } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Descriptions,
  Drawer,
  message,
  Modal,
  Space,
  Tag,
} from 'ant-design-vue';

import { createM2MClientApi, listM2MClientsApi } from '#/api';
import { AdminListTable } from '#/components/admin-list-table';

import FormModal from '../../iam/components/form-modal.vue';

defineOptions({ name: 'IdentityM2M' });

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 'active' },
          { label: '停用', value: 'disabled' },
          { label: '已删除', value: 'deleted' },
        ],
        placeholder: '状态',
      },
      fieldName: 'status',
      label: '状态',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: 'A 端', value: 'admin' },
          { label: 'B 端', value: 'biz' },
          { label: 'C 端', value: 'c' },
        ],
        placeholder: '端别',
      },
      fieldName: 'channel',
      label: '端别',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<M2MClient[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'name', key: 'name', title: '名称', width: 180 },
  { dataIndex: 'client_id', key: 'client_id', title: '客户端 ID', width: 220 },
  {
    dataIndex: 'allowed_channels',
    key: 'allowed_channels',
    title: '授权端别',
    width: 160,
  },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { dataIndex: 'owner', key: 'owner', title: '负责人', width: 140 },
  { key: 'actions', title: '操作', width: 100 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  active: { color: 'success', label: '启用' },
  deleted: { color: 'error', label: '已删除' },
  disabled: { color: 'default', label: '停用' },
};

const createOpen = ref(false);
const createSubmitting = ref(false);
const createFields: FormField[] = [
  { label: '名称', name: 'name', required: true, span: 2 },
  { label: '负责人', name: 'owner', span: 2 },
  {
    component: 'tags',
    label: '授权端别',
    name: 'allowed_channels',
    placeholder: '输入 admin / biz / c 后回车',
    required: true,
    span: 2,
  },
  {
    component: 'tags',
    label: '授权作用域',
    name: 'allowed_scopes',
    placeholder: '回车添加 scope',
    required: true,
    span: 2,
  },
];

const detailOpen = ref(false);
const detail = ref<M2MClient>();
const issuedSecret = ref('');

const featureDisabled = ref(false);

async function fetchList() {
  loading.value = true;
  featureDisabled.value = false;
  try {
    const values = await queryFormApi.getValues();
    const result = await listM2MClientsApi({
      channel: values.channel || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: values.status || undefined,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } catch (error) {
    const status = (error as { response?: { status?: number } })?.response
      ?.status;
    if (status === 404) {
      featureDisabled.value = true;
      rows.value = [];
      pagination.total = 0;
      message.warning('机器身份未启用（identity.m2m.enabled=false）');
    }
  } finally {
    loading.value = false;
  }
}

function handleSearch() {
  pagination.current = 1;
  fetchList();
}

async function handleReset() {
  await queryFormApi.reset();
  handleSearch();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function handleCreate(payload: Record<string, any>) {
  createSubmitting.value = true;
  try {
    const channels = Array.isArray(payload.allowed_channels)
      ? payload.allowed_channels
      : [payload.allowed_channels].filter(Boolean);
    const scopes = Array.isArray(payload.allowed_scopes)
      ? payload.allowed_scopes
      : String(payload.allowed_scopes || '')
          .split(/[,\s]+/)
          .filter(Boolean);
    const result = await createM2MClientApi({
      allowed_channels: channels,
      allowed_scopes: scopes,
      name: payload.name,
      owner: payload.owner || undefined,
    });
    createOpen.value = false;
    issuedSecret.value = result.client_secret;
    await fetchList();
    Modal.success({
      content: result.client_secret,
      title: '客户端已创建，请立即保存密钥（仅显示一次）',
      width: 560,
    });
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    createSubmitting.value = false;
  }
}

function openDetail(record: M2MClient) {
  detail.value = record;
  detailOpen.value = true;
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="管理服务客户端及其授权端别和作用域。创建和轮换后的密钥仅在本次响应展示。"
    title="机器身份"
  >
    <template #extra>
      <Button
        v-access:code="'identity:m2m:write'"
        type="primary"
        @click="createOpen = true"
      >
        创建客户端
      </Button>
    </template>

    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
      </Space>
    </div>

    <div class="bg-card rounded-lg p-4">
      <p v-if="featureDisabled" class="text-muted-foreground mb-3 text-sm">
        当前环境未启用 M2M（`identity.m2m.enabled`）。开启并重启 admin-api
        后可管理客户端。
      </p>
      <AdminListTable
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :locale="{
          emptyText: featureDisabled ? '功能未启用' : '暂无数据',
        }"
        :pagination="pagination"
        row-key="id"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'allowed_channels'">
            {{ (record.allowed_channels ?? []).join(' / ') }}
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
              @click="openDetail(record as M2MClient)"
            >
              管理
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
      title="创建客户端"
      @submit="handleCreate"
    />

    <Drawer v-model:open="detailOpen" title="机器身份管理" width="560">
      <Descriptions v-if="detail" :column="1" bordered size="small">
        <Descriptions.Item label="名称">{{ detail.name }}</Descriptions.Item>
        <Descriptions.Item label="客户端 ID">
          {{ detail.client_id }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag :color="statusMap[detail.status]?.color ?? 'default'">
            {{ statusMap[detail.status]?.label ?? detail.status }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="授权端别">
          {{ (detail.allowed_channels ?? []).join(' / ') }}
        </Descriptions.Item>
        <Descriptions.Item label="作用域">
          {{ (detail.allowed_scopes ?? []).join(', ') || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="负责人">
          {{ detail.owner || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="轮换到期">
          {{ detail.rotation_due_at || '—' }}
        </Descriptions.Item>
      </Descriptions>
      <p v-if="issuedSecret" class="text-muted-foreground mt-4 text-xs">
        最近一次签发密钥已在弹窗中展示；请勿写入日志。
      </p>
    </Drawer>
  </Page>
</template>
