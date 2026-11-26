<script lang="ts" setup>
import type { TableColumnsType } from 'ant-design-vue';

import type { FormField } from '../components/types';

import type { Binding, ChangeAction, IamSubjectPermissions } from '#/api';

import { computed, onMounted, ref } from 'vue';

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
  createBindingApi,
  createChangeRequestApi,
  deleteBindingApi,
  getErrorMessage,
  getSubjectPermissionsApi,
  isConflict,
  listBindingsApi,
  listRolesApi,
} from '#/api';
import { AdminListTable } from '#/components/admin-list-table';
import { listQueryCardClass } from '#/utils/list-query-form';

import ConflictModal from '../components/conflict-modal.vue';
import FormModal from '../components/form-modal.vue';
import JsonPreview from '../components/json-preview.vue';
import RoleSelect from '../components/role-select.vue';

defineOptions({ name: 'IamBinding' });

const loading = ref(false);
const rows = ref<Binding[]>([]);
const roleId = ref<string>();
const subjectId = ref('');
const roleOptions = ref<{ label: string; value: string }[]>([]);

const createOpen = ref(false);
const createSubmitting = ref(false);

const permissionsOpen = ref(false);
const permissionsLoading = ref(false);
const permissions = ref<IamSubjectPermissions>();
const permissionsSubject = ref('');

const conflictOpen = ref(false);
const conflictSubmitting = ref(false);
const conflict = ref<{
  action?: ChangeAction;
  message: string;
  payload: Record<string, unknown>;
  targetId?: string;
}>({
  message: '',
  payload: {},
});

const columns: TableColumnsType = [
  { dataIndex: 'subject_id', key: 'subject_id', title: '主体 ID', width: 180 },
  { dataIndex: 'role_id', key: 'role_id', title: '角色 ID', width: 180 },
  { dataIndex: 'expires_at', key: 'expires_at', title: '过期时间', width: 190 },
  { dataIndex: 'granted_by', key: 'granted_by', title: '授予人', width: 160 },
  { dataIndex: 'created_at', key: 'created_at', title: '创建时间', width: 190 },
  { key: 'actions', title: '操作', width: 220 },
];

const createFields = computed<FormField[]>(() => [
  {
    component: 'select',
    label: '角色',
    name: 'role_id',
    options: roleOptions.value,
    required: true,
  },
  {
    label: '主体 ID（staff）',
    name: 'subject_id',
    placeholder: '账号 ID / 主体标识',
    required: true,
  },
  {
    component: 'datetime',
    label: '过期时间（可选）',
    name: 'expires_at',
    span: 2,
  },
  {
    component: 'textarea',
    label: '变更原因',
    name: 'reason',
    rows: 2,
    span: 2,
  },
]);

async function loadRoleOptions() {
  const result = await listRolesApi({ pageSize: 100, status: 'active' });
  roleOptions.value = result.items.map((role) => ({
    label: `${role.name}（${role.code}）`,
    value: role.id,
  }));
}

async function fetchList() {
  if (!roleId.value && !subjectId.value.trim()) {
    message.warning('请先选择角色或输入主体 ID');
    return;
  }
  loading.value = true;
  try {
    const result = await listBindingsApi({
      role_id: roleId.value || undefined,
      subject_id: subjectId.value.trim() || undefined,
      subject_type: subjectId.value.trim() ? 'staff' : undefined,
    });
    rows.value = result.items;
  } finally {
    loading.value = false;
  }
}

function handleWriteError(
  error: unknown,
  action: ChangeAction,
  payload: Record<string, unknown>,
  targetId?: string,
) {
  if (isConflict(error)) {
    conflict.value = {
      action,
      message: getErrorMessage(error, '数据已被修改，或该操作需要走审批'),
      payload,
      targetId,
    };
    conflictOpen.value = true;
  }
}

async function handleCreate(payload: Record<string, any>) {
  createSubmitting.value = true;
  const body = {
    expires_at: payload.expires_at || undefined,
    reason: payload.reason,
    role_id: payload.role_id,
    subject_id: payload.subject_id,
    subject_type: 'staff' as const,
  };
  try {
    await createBindingApi(body);
    message.success('绑定成功');
    createOpen.value = false;
    await fetchList();
  } catch (error) {
    handleWriteError(error, 'binding_create', {
      expires_at: body.expires_at,
      role_id: body.role_id,
      subject_id: body.subject_id,
      subject_type: body.subject_type,
    });
  } finally {
    createSubmitting.value = false;
  }
}

function handleUnbind(binding: Binding) {
  Modal.confirm({
    content: '解绑后该主体立即失去对应角色权限，快照会在缓存 TTL 内失效。',
    okButtonProps: { danger: true },
    okText: '解绑',
    onOk: async () => {
      const body = { reason: '页面解绑' };
      try {
        await deleteBindingApi(binding.id, body.reason);
        message.success('已解绑');
        await fetchList();
      } catch (error) {
        handleWriteError(error, 'binding_delete', {}, binding.id);
      }
    },
    title: '解绑角色',
  });
}

async function openPermissions(subject: string) {
  permissionsSubject.value = subject;
  permissionsOpen.value = true;
  permissionsLoading.value = true;
  try {
    permissions.value = await getSubjectPermissionsApi('staff', subject);
  } finally {
    permissionsLoading.value = false;
  }
}

async function submitApproval(payload: {
  action: ChangeAction;
  payload: Record<string, unknown>;
  reason: string;
  targetId?: string;
}) {
  conflictSubmitting.value = true;
  try {
    await createChangeRequestApi({
      action: payload.action,
      payload: payload.payload,
      reason: payload.reason,
      target_id: payload.targetId,
    });
    message.success('已提交审批单');
    conflictOpen.value = false;
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    conflictSubmitting.value = false;
  }
}

onMounted(loadRoleOptions);
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <Space wrap>
          <RoleSelect
            :model-value="roleId"
            class="w-64"
            placeholder="按角色查询"
            @update:model-value="roleId = $event"
          />
          <Input
            v-model:value="subjectId"
            class="w-64"
            placeholder="按主体 ID 查询（staff）"
          />
          <Button type="primary" @click="fetchList">查询</Button>
          <Button @click="fetchList">刷新</Button>
        </Space>
        <Button v-access:code="'iam:binding:write'" @click="createOpen = true">
          新建绑定
        </Button>
      </div>

      <AdminListTable
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :pagination="false"
        row-key="id"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'expires_at'">
            {{ record.expires_at?.replace('T', ' ').slice(0, 19) || '永久' }}
          </template>
          <template v-else-if="column.key === 'created_at'">
            {{ record.created_at?.replace('T', ' ').slice(0, 19) }}
          </template>
          <template v-else-if="column.key === 'actions'">
            <Space :size="4" class="whitespace-nowrap">
              <Button
                size="small"
                type="link"
                @click="openPermissions(record.subject_id)"
              >
                主体权限
              </Button>
              <Button
                v-access:code="'iam:binding:write'"
                danger
                size="small"
                type="link"
                @click="handleUnbind(record as Binding)"
              >
                解绑
              </Button>
            </Space>
          </template>
        </template>
      </AdminListTable>
    </div>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      title="新建绑定"
      @submit="handleCreate"
    />

    <Drawer
      v-model:open="permissionsOpen"
      :width="640"
      :title="`主体权限：${permissionsSubject}`"
    >
      <div v-if="permissions" class="space-y-4">
        <div>
          <p class="text-muted-foreground mb-1 text-xs">角色</p>
          <Space wrap>
            <Tag v-for="role in permissions.roles" :key="role" color="blue">
              {{ role }}
            </Tag>
            <span
              v-if="permissions.roles.length === 0"
              class="text-muted-foreground text-sm"
            >
              无角色
            </span>
          </Space>
        </div>
        <div>
          <p class="text-muted-foreground mb-1 text-xs">权限点</p>
          <Space wrap>
            <Tag v-for="code in permissions.permissions" :key="code">
              {{ code }}
            </Tag>
          </Space>
        </div>
        <div>
          <p class="text-muted-foreground mb-1 text-xs">数据范围</p>
          <JsonPreview :value="permissions.data_scopes" />
        </div>
        <p class="text-muted-foreground text-xs">
          snapshot_version：{{ permissions.snapshot_version }} ·
          {{ permissions.generated_at }}
        </p>
      </div>
    </Drawer>

    <ConflictModal
      v-model:open="conflictOpen"
      :action="conflict.action"
      :message="conflict.message"
      :payload="conflict.payload"
      :submitting="conflictSubmitting"
      :target-id="conflict.targetId"
      @refresh="fetchList"
      @submit="submitApproval"
    />
  </Page>
</template>
