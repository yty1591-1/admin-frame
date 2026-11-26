<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../components/types';

import type { ChangeAction, ChangeRequest, ChangeStatus } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Drawer,
  Input,
  message,
  Modal,
  Space,
  Tag,
} from 'ant-design-vue';

import {
  applyChangeRequestApi,
  approveChangeRequestApi,
  cancelChangeRequestApi,
  createChangeRequestApi,
  listChangeRequestsApi,
  rejectChangeRequestApi,
} from '#/api';
import { AdminListTable } from '#/components/admin-list-table';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

import FormModal from '../components/form-modal.vue';
import JsonPreview from '../components/json-preview.vue';

defineOptions({ name: 'IamChange' });

const actionOptions: { label: string; value: ChangeAction }[] = [
  { label: '创建角色', value: 'role_create' },
  { label: '更新角色', value: 'role_update' },
  { label: '替换角色权限', value: 'role_permission_replace' },
  { label: '更新角色约束', value: 'role_constraint_update' },
  { label: '克隆角色', value: 'role_clone' },
  { label: '删除角色', value: 'role_delete' },
  { label: '创建绑定', value: 'binding_create' },
  { label: '删除绑定', value: 'binding_delete' },
  { label: '破玻璃申请', value: 'breakglass_request' },
];

const actionLabel = new Map(
  actionOptions.map((item) => [item.value, item.label]),
);

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
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: '待审批', value: 'pending' },
            { label: '已通过', value: 'approved' },
            { label: '已应用', value: 'applied' },
            { label: '已拒绝', value: 'rejected' },
            { label: '已撤销', value: 'cancelled' },
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
          options: actionOptions,
          placeholder: '动作',
        },
        fieldName: 'action',
        label: '动作',
      },
      {
        component: 'Input',
        componentProps: { allowClear: true, placeholder: '申请人主体 ID' },
        fieldName: 'requested_by',
        label: '申请人',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<ChangeRequest[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'action', key: 'action', title: '动作', width: 190 },
  { dataIndex: 'target_type', key: 'target_type', title: '目标', width: 130 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  {
    dataIndex: 'requested_by',
    key: 'requested_by',
    title: '申请人',
    width: 170,
  },
  {
    dataIndex: 'requested_at',
    key: 'requested_at',
    title: '申请时间',
    width: 190,
  },
  { dataIndex: 'version', key: 'version', title: '版本', width: 80 },
  { key: 'actions', title: '操作', width: 260 },
];

const detailOpen = ref(false);
const detail = ref<ChangeRequest>();

const createOpen = ref(false);
const createSubmitting = ref(false);

const decideOpen = ref(false);
const decideLoading = ref(false);
const decide = reactive<{
  id: string;
  kind: '' | 'apply' | 'approve' | 'cancel' | 'reject';
  reason: string;
  version: number;
}>({ id: '', kind: '', reason: '', version: 1 });

const createFields: FormField[] = [
  {
    component: 'select',
    label: '动作',
    name: 'action',
    options: actionOptions,
    required: true,
    span: 2,
  },
  {
    label: '目标 ID（新建类可留空）',
    name: 'target_id',
    span: 2,
  },
  {
    component: 'json',
    label: '载荷（JSON）',
    name: 'payload',
    required: true,
    rows: 8,
    span: 2,
  },
  {
    component: 'textarea',
    label: '申请原因',
    name: 'reason',
    required: true,
    rows: 2,
    span: 2,
  },
];

const statusColor: Record<ChangeStatus, string> = {
  applied: 'success',
  approved: 'blue',
  cancelled: 'default',
  pending: 'orange',
  rejected: 'error',
};

const statusText: Record<ChangeStatus, string> = {
  applied: '已应用',
  approved: '已通过',
  cancelled: '已撤销',
  pending: '待审批',
  rejected: '已拒绝',
};

const decideTitle = ref('');

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listChangeRequestsApi({
      action: values.action || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      requested_by: values.requested_by || undefined,
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
    await createChangeRequestApi({
      action: payload.action,
      payload: payload.payload,
      reason: payload.reason,
      target_id: payload.target_id || undefined,
    });
    message.success('审批单已创建');
    createOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    createSubmitting.value = false;
  }
}

function openDetail(item: ChangeRequest) {
  detail.value = item;
  detailOpen.value = true;
}

function openDecision(
  item: ChangeRequest,
  kind: 'apply' | 'approve' | 'cancel' | 'reject',
) {
  Object.assign(decide, {
    id: item.id,
    kind,
    reason: '',
    version: item.version,
  });
  const titles: Record<string, string> = {
    apply: '应用审批单',
    approve: '通过审批单',
    cancel: '撤销审批单',
    reject: '拒绝审批单',
  };
  decideTitle.value = titles[kind] ?? '处理审批单';
  decideOpen.value = true;
}

async function submitDecision() {
  decideLoading.value = true;
  const body = { reason: decide.reason || undefined, version: decide.version };
  try {
    if (decide.kind === 'approve') {
      await approveChangeRequestApi(decide.id, body);
    } else if (decide.kind === 'reject') {
      await rejectChangeRequestApi(decide.id, body);
    } else if (decide.kind === 'cancel') {
      await cancelChangeRequestApi(decide.id, body);
    } else {
      await applyChangeRequestApi(decide.id, body);
    }
    message.success('操作成功');
    decideOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    decideLoading.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <QueryForm class="min-w-0 flex-1" />
        <Button v-access:code="'iam:role:write'" @click="createOpen = true">
          新建审批单
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
        <template v-if="column.key === 'action'">
          {{ actionLabel.get(record.action) ?? record.action }}
        </template>
        <template v-else-if="column.key === 'status'">
          <Tag :color="statusColor[record.status as ChangeStatus]">
            {{ statusText[record.status as ChangeStatus] }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'requested_at'">
          {{ record.requested_at?.replace('T', ' ').slice(0, 19) }}
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space :size="4" class="whitespace-nowrap">
            <Button
              size="small"
              type="link"
              @click="openDetail(record as ChangeRequest)"
            >
              详情
            </Button>
            <template v-if="record.status === 'pending'">
              <Button
                v-access:code="'iam:change:approve'"
                size="small"
                type="link"
                @click="openDecision(record as ChangeRequest, 'approve')"
              >
                通过
              </Button>
              <Button
                v-access:code="'iam:change:approve'"
                danger
                size="small"
                type="link"
                @click="openDecision(record as ChangeRequest, 'reject')"
              >
                拒绝
              </Button>
              <Button
                size="small"
                type="link"
                @click="openDecision(record as ChangeRequest, 'cancel')"
              >
                撤销
              </Button>
            </template>
            <Button
              v-if="record.status === 'approved'"
              v-access:code="'iam:change:approve'"
              size="small"
              type="link"
              @click="openDecision(record as ChangeRequest, 'apply')"
            >
              应用
            </Button>
          </Space>
        </template>
      </template>
    </AdminListTable>
    </div>

    <Drawer v-model:open="detailOpen" :width="620" title="审批单详情">
      <div v-if="detail" class="space-y-3">
        <p class="text-foreground text-sm">
          {{ actionLabel.get(detail.action) ?? detail.action }}
          <Tag :color="statusColor[detail.status]">
            {{ statusText[detail.status] }}
          </Tag>
        </p>
        <p class="text-muted-foreground text-xs">
          申请人 {{ detail.requested_by }} · {{ detail.requested_at }}
        </p>
        <p class="text-foreground text-sm">原因：{{ detail.reason }}</p>
        <div>
          <p class="text-muted-foreground mb-1 text-xs">载荷</p>
          <JsonPreview :value="detail.payload ?? null" />
        </div>
      </div>
    </Drawer>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      :model-value="{ payload: '{}' }"
      title="新建审批单"
      :width="720"
      @submit="handleCreate"
    />

    <Modal
      v-model:open="decideOpen"
      :confirm-loading="decideLoading"
      :title="decideTitle"
      @ok="submitDecision"
    >
      <p class="text-muted-foreground mb-2 text-xs">
        单号：{{ decide.id }} · version {{ decide.version }}
      </p>
      <Input.TextArea
        v-model:value="decide.reason"
        :rows="3"
        placeholder="决策说明（通过/拒绝/撤销原因）"
      />
    </Modal>
  </Page>
</template>
