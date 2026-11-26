<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { NotificationRequest } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Descriptions,
  Drawer,
  message,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  getNotificationApi,
  listNotificationsApi,
  sendNotificationApi,
} from '#/api';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

import FormModal from '../../iam/components/form-modal.vue';

defineOptions({ name: 'MessagingNotifications' });

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
        componentProps: { allowClear: true, placeholder: '场景' },
        fieldName: 'scene',
        label: '场景',
      },
      {
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: '待投递', value: 'pending' },
            { label: '已派发', value: 'dispatched' },
            { label: '已发送', value: 'sent' },
            { label: '已送达', value: 'delivered' },
            { label: '未送达', value: 'undelivered' },
            { label: '失败', value: 'failed' },
            { label: '已拒绝', value: 'rejected' },
          ],
          placeholder: '状态',
        },
        fieldName: 'status',
        label: '状态',
      },
      {
        component: 'Input',
        componentProps: { allowClear: true, placeholder: '租户 ID' },
        fieldName: 'tenant_id',
        label: '租户 ID',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<NotificationRequest[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'id', key: 'id', title: '通知 ID', width: 200 },
  { dataIndex: 'scene', key: 'scene', title: '场景', width: 140 },
  {
    dataIndex: 'template_key',
    key: 'template_key',
    title: '模板键',
    width: 180,
  },
  {
    dataIndex: 'recipient_ref',
    key: 'recipient_ref',
    title: '接收方摘要',
    width: 200,
  },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { key: 'actions', title: '操作', width: 100 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  delivered: { color: 'success', label: '已送达' },
  dispatched: { color: 'processing', label: '已派发' },
  failed: { color: 'error', label: '失败' },
  pending: { color: 'warning', label: '待投递' },
  rejected: { color: 'default', label: '已拒绝' },
  sent: { color: 'processing', label: '已发送' },
  undelivered: { color: 'warning', label: '未送达' },
};

const sendOpen = ref(false);
const sendSubmitting = ref(false);
const sendFields: FormField[] = [
  { label: '场景', name: 'scene', required: true, span: 2 },
  { label: '模板键', name: 'template_key', required: true, span: 2 },
  {
    component: 'tags',
    label: '渠道',
    name: 'channels',
    placeholder: '输入 email / sms / push 后回车',
    required: true,
    span: 2,
  },
  { label: '邮箱', name: 'email', span: 1 },
  { label: '手机号', name: 'phone', span: 1 },
  { label: '租户 ID', name: 'tenant_id', span: 2 },
];

const detailOpen = ref(false);
const detailLoading = ref(false);
const detail = ref<NotificationRequest>();

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listNotificationsApi({
      page: pagination.current,
      pageSize: pagination.pageSize,
      scene: values.scene || undefined,
      status: values.status || undefined,
      tenant_id: values.tenant_id || undefined,
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

async function handleSend(payload: Record<string, any>) {
  sendSubmitting.value = true;
  try {
    const channels = Array.isArray(payload.channels)
      ? payload.channels
      : [payload.channels].filter(Boolean);
    await sendNotificationApi({
      channels,
      email: payload.email || undefined,
      phone: payload.phone || undefined,
      scene: payload.scene,
      template_key: payload.template_key,
      tenant_id: payload.tenant_id || undefined,
    });
    message.success('通知已受理');
    sendOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    sendSubmitting.value = false;
  }
}

async function openDetail(record: NotificationRequest) {
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    detail.value = await getNotificationApi(record.id);
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
          v-access:code="'notification:write'"
          type="primary"
          @click="sendOpen = true"
        >
          代发通知
        </Button>
      </div>

      <Table
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :pagination="pagination"
        row-key="id"
        size="middle"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="statusMap[record.status]?.color ?? 'default'">
              {{ statusMap[record.status]?.label ?? record.status }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Button
              size="small"
              type="link"
              @click="openDetail(record as NotificationRequest)"
            >
              详情
            </Button>
          </template>
        </template>
      </Table>
    </div>

    <FormModal
      v-model:open="sendOpen"
      :fields="sendFields"
      :loading="sendSubmitting"
      submit-text="代发"
      title="代发通知"
      @submit="handleSend"
    />

    <Drawer v-model:open="detailOpen" title="通知单详情" width="560">
      <Descriptions
        v-if="detail"
        :column="1"
        :loading="detailLoading"
        bordered
        size="small"
      >
        <Descriptions.Item label="通知 ID">{{ detail.id }}</Descriptions.Item>
        <Descriptions.Item label="场景">{{ detail.scene }}</Descriptions.Item>
        <Descriptions.Item label="模板键">
          {{ detail.template_key }}
        </Descriptions.Item>
        <Descriptions.Item label="接收方摘要">
          {{ detail.recipient_ref }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag :color="statusMap[detail.status]?.color ?? 'default'">
            {{ statusMap[detail.status]?.label ?? detail.status }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="租户">
          {{ detail.tenant_id || 'platform' }}
        </Descriptions.Item>
        <Descriptions.Item label="创建于">
          {{ detail.created_at }}
        </Descriptions.Item>
      </Descriptions>
      <p class="text-muted-foreground mt-4 text-xs">
        失败记录可进入重放确认；渠道投递明细将在此继续展开。
      </p>
    </Drawer>
  </Page>
</template>
