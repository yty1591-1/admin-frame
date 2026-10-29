<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../components/types';

import type { ChangeRequest, ChangeStatus } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  approveBreakglassApi,
  getErrorMessage,
  listChangeRequestsApi,
  rejectChangeRequestApi,
  requestBreakglassApi,
  revokeBreakglassApi,
} from '#/api';

import FormModal from '../components/form-modal.vue';

defineOptions({ name: 'IamBreakglass' });

const loading = ref(false);
const rows = ref<ChangeRequest[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const requestOpen = ref(false);
const requestSubmitting = ref(false);

const decideOpen = ref(false);
const decideLoading = ref(false);
const decide = reactive<{
  id: string;
  kind: '' | 'approve' | 'reject' | 'revoke';
  reason: string;
  version: number;
}>({ id: '', kind: '', reason: '', version: 1 });

const columns: TableColumnsType = [
  { dataIndex: 'subject_id', key: 'subject_id', title: '主体 ID', width: 180 },
  { dataIndex: 'duration', key: 'duration', title: '时长（秒）', width: 110 },
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
  { dataIndex: 'expires_at', key: 'expires_at', title: '到期时间', width: 190 },
  { key: 'actions', title: '操作', width: 240 },
];

const statusColor: Record<ChangeStatus, string> = {
  applied: 'success',
  approved: 'blue',
  cancelled: 'default',
  pending: 'orange',
  rejected: 'error',
};

const statusText: Record<ChangeStatus, string> = {
  applied: '已生效',
  approved: '已通过',
  cancelled: '已撤销',
  pending: '待审批',
  rejected: '已拒绝',
};

const requestFields: FormField[] = [
  {
    label: '主体 ID（staff）',
    name: 'subject_id',
    required: true,
    span: 2,
  },
  {
    component: 'number',
    label: '授权时长（秒，1~7200）',
    max: 7200,
    min: 1,
    name: 'duration_seconds',
    required: true,
    span: 2,
  },
  {
    component: 'textarea',
    label: '申请原因',
    name: 'reason',
    required: true,
    rows: 3,
    span: 2,
  },
];

const requestModel = computed(() => ({ duration_seconds: 1800 }));

function subjectOf(record: ChangeRequest): string {
  return String(record.payload?.subject_id ?? '—');
}

function durationOf(record: ChangeRequest): number {
  return Number(record.payload?.duration_seconds ?? 0);
}

function expiresOf(record: ChangeRequest): string {
  if (!record.applied_at || !durationOf(record)) {
    return '—';
  }
  const time = new Date(record.applied_at);
  time.setSeconds(time.getSeconds() + durationOf(record));
  return time.toISOString().replace('T', ' ').slice(0, 19);
}

async function fetchList() {
  loading.value = true;
  try {
    const result = await listChangeRequestsApi({
      action: 'breakglass_request',
      page: pagination.current,
      pageSize: pagination.pageSize,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function handleRequest(payload: Record<string, any>) {
  requestSubmitting.value = true;
  try {
    await requestBreakglassApi({
      duration_seconds: Number(payload.duration_seconds),
      reason: payload.reason,
      subject_id: payload.subject_id,
      subject_type: 'staff',
    });
    message.success('破玻璃申请已提交，等待另一账号审批');
    requestOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    requestSubmitting.value = false;
  }
}

function openDecision(
  record: ChangeRequest,
  kind: 'approve' | 'reject' | 'revoke',
) {
  Object.assign(decide, {
    id: record.id,
    kind,
    reason: '',
    version: record.version,
  });
  decideOpen.value = true;
}

async function submitDecision() {
  decideLoading.value = true;
  const body = { reason: decide.reason || undefined, version: decide.version };
  try {
    if (decide.kind === 'approve') {
      await approveBreakglassApi(decide.id, body);
    } else if (decide.kind === 'reject') {
      await rejectChangeRequestApi(decide.id, body);
    } else {
      await revokeBreakglassApi(decide.id, body);
    }
    message.success('操作成功');
    decideOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    decideLoading.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="限时超级权限：申请本身不授权，需另一账号审批后临时生效"
    title="破玻璃申请"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <Space>
        <Button
          v-access:code="'iam:breakglass:request'"
          type="primary"
          @click="requestOpen = true"
        >
          申请破玻璃
        </Button>
        <Button @click="fetchList">刷新</Button>
      </Space>
      <p class="text-muted-foreground mt-2 text-xs">
        关闭 `iam.breakglass.enabled` 时申请会返回 403；审批人与申请人必须不同。
      </p>
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
        <template v-if="column.key === 'subject_id'">
          {{ subjectOf(record as ChangeRequest) }}
        </template>
        <template v-else-if="column.key === 'duration'">
          {{ durationOf(record as ChangeRequest) || '—' }}
        </template>
        <template v-else-if="column.key === 'status'">
          <Tag :color="statusColor[record.status as ChangeStatus]">
            {{ statusText[record.status as ChangeStatus] }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'requested_at'">
          {{ record.requested_at?.replace('T', ' ').slice(0, 19) }}
        </template>
        <template v-else-if="column.key === 'expires_at'">
          {{ expiresOf(record as ChangeRequest) }}
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space :size="4" wrap>
            <template v-if="record.status === 'pending'">
              <Button
                v-access:code="'iam:breakglass:approve'"
                size="small"
                type="link"
                @click="openDecision(record as ChangeRequest, 'approve')"
              >
                审批通过
              </Button>
              <Button
                v-access:code="'iam:breakglass:approve'"
                danger
                size="small"
                type="link"
                @click="openDecision(record as ChangeRequest, 'reject')"
              >
                拒绝
              </Button>
            </template>
            <Button
              v-if="record.status === 'applied'"
              v-access:code="'iam:breakglass:approve'"
              danger
              size="small"
              type="link"
              @click="openDecision(record as ChangeRequest, 'revoke')"
            >
              撤销授权
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <FormModal
      v-model:open="requestOpen"
      :fields="requestFields"
      :loading="requestSubmitting"
      :model-value="requestModel"
      title="申请破玻璃"
      @submit="handleRequest"
    />

    <Modal
      v-model:open="decideOpen"
      :confirm-loading="decideLoading"
      :title="
        decide.kind === 'approve'
          ? '审批破玻璃'
          : decide.kind === 'reject'
            ? '拒绝申请'
            : '撤销破玻璃授权'
      "
      @ok="submitDecision"
    >
      <p class="text-muted-foreground mb-2 text-xs">
        单号：{{ decide.id }} · version {{ decide.version }}
      </p>
      <Input.TextArea
        v-model:value="decide.reason"
        :rows="3"
        placeholder="决策说明"
      />
    </Modal>
  </Page>
</template>
