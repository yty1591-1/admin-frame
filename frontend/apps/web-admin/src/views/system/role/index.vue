<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { ChangeAction, DataScope, Role, RoleStatus } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Drawer,
  Form,
  FormItem,
  Input,
  InputNumber,
  message,
  Modal,
  Select,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  cloneRoleApi,
  createChangeRequestApi,
  createRoleApi,
  deleteRoleApi,
  getErrorMessage,
  getRoleApi,
  getRoleConstraintsApi,
  isConflict,
  listRolesApi,
  putRoleConstraintsApi,
  replaceRolePermissionsApi,
  updateRoleApi,
} from '#/api';

import ConflictModal from '../../iam/components/conflict-modal.vue';
import DataScopeEditor from '../../iam/components/data-scope-editor.vue';
import FormModal from '../../iam/components/form-modal.vue';
import PermissionPicker from '../../iam/components/permission-picker.vue';

defineOptions({ name: 'SystemRole' });

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '角色码 / 名称',
      },
      fieldName: 'keyword',
      label: '关键字',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 'active' },
          { label: '停用', value: 'disabled' },
        ],
        placeholder: '请选择状态',
      },
      fieldName: 'status',
      label: '状态',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<Role[]>([]);
const roleOptions = ref<{ label: string; value: string }[]>([]);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'code', key: 'code', title: '角色码', width: 180 },
  { dataIndex: 'name', key: 'name', title: '角色名称' },
  { dataIndex: 'role_type', key: 'role_type', title: '类型', width: 100 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 90 },
  { dataIndex: 'parent_id', key: 'parent_id', title: '父角色', width: 180 },
  { dataIndex: 'updated_at', key: 'updated_at', title: '更新时间', width: 190 },
  { key: 'actions', title: '操作', width: 320 },
];

const roleTypeMeta: Record<string, { color: string; text: string }> = {
  breakglass: { color: 'red', text: '破玻璃' },
  custom: { color: 'green', text: '自定义' },
  system: { color: 'blue', text: '系统' },
};

const createOpen = ref(false);
const createSubmitting = ref(false);
const editOpen = ref(false);
const editSubmitting = ref(false);
const editing = ref<Role>();

const grantOpen = ref(false);
const grantSubmitting = ref(false);
const granting = ref<Role>();
const grantPermissions = ref<string[]>([]);
const grantScopes = ref<DataScope[]>([]);
const grantReason = ref('');

const constraintsOpen = ref(false);
const constraintsSubmitting = ref(false);
const constraints = reactive<{
  max_bindings?: number;
  reason: string;
  required_role_ids: string[];
  version: number;
}>({
  max_bindings: undefined,
  reason: '',
  required_role_ids: [],
  version: 1,
});

const cloneOpen = ref(false);
const cloneSubmitting = ref(false);
const cloning = ref<Role>();

const conflictOpen = ref(false);
const conflictSubmitting = ref(false);
const conflict = reactive<{
  action?: ChangeAction;
  message: string;
  payload: Record<string, unknown>;
  targetId?: string;
}>({
  message: '',
  payload: {},
});

const createFields = computed<FormField[]>(() => [
  {
    label: '角色码',
    name: 'code',
    required: true,
    placeholder: '端内唯一，如 ops_auditor',
  },
  { label: '角色名称', name: 'name', required: true },
  {
    component: 'select',
    label: '父角色',
    name: 'parent_id',
    options: roleOptions.value,
    placeholder: '可选，继承父角色权限',
  },
  { label: '描述', name: 'description', span: 2 },
  {
    component: 'textarea',
    label: '变更原因',
    name: 'reason',
    rows: 2,
    span: 2,
  },
]);

const editFields = computed<FormField[]>(() => [
  { label: '角色码', name: 'code', disabled: true },
  { label: '角色名称', name: 'name', required: true },
  {
    component: 'select',
    label: '父角色',
    name: 'parent_id',
    options: roleOptions.value,
    placeholder: '留空表示根角色',
  },
  {
    component: 'select',
    label: '状态',
    name: 'status',
    options: [
      { label: '启用', value: 'active' },
      { label: '停用', value: 'disabled' },
    ],
    required: true,
  },
  {
    component: 'textarea',
    label: '变更原因',
    name: 'reason',
    rows: 2,
    span: 2,
    placeholder: '停用角色时必填',
  },
]);

const cloneFields: FormField[] = [
  { label: '新角色码', name: 'code', required: true },
  { label: '新角色名称', name: 'name', required: true },
];

const createModel = computed(() => ({
  parent_id: undefined,
  reason: '',
}));

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listRolesApi({
      keyword: values.keyword || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: (values.status as RoleStatus) || undefined,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

async function loadRoleOptions() {
  const result = await listRolesApi({ pageSize: 100, status: 'active' });
  roleOptions.value = result.items.map((role) => ({
    label: `${role.name}（${role.code}）`,
    value: role.id,
  }));
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

function handleWriteError(
  error: unknown,
  action: ChangeAction,
  payload: Record<string, unknown>,
  targetId?: string,
) {
  if (isConflict(error)) {
    Object.assign(conflict, {
      action,
      message: getErrorMessage(error, '数据已被修改，或该操作需要走审批'),
      payload,
      targetId,
    });
    conflictOpen.value = true;
    return;
  }
  message.error(getErrorMessage(error));
}

async function handleCreate(payload: Record<string, any>) {
  createSubmitting.value = true;
  const body = {
    code: payload.code,
    data_scopes: [],
    description: payload.description,
    name: payload.name,
    parent_id: payload.parent_id || undefined,
    permissions: [],
    reason: payload.reason,
    scope_type: 'platform' as const,
  };
  try {
    await createRoleApi(body);
    message.success('角色已创建');
    createOpen.value = false;
    await Promise.all([fetchList(), loadRoleOptions()]);
  } catch (error) {
    handleWriteError(error, 'role_create', {
      code: body.code,
      data_scopes: body.data_scopes,
      description: body.description,
      name: body.name,
      parent_id: body.parent_id,
      permissions: body.permissions,
      scope_type: body.scope_type,
    });
  } finally {
    createSubmitting.value = false;
  }
}

async function openEdit(role: Role) {
  const detail = await getRoleApi(role.id);
  editing.value = detail;
  editOpen.value = true;
}

async function handleEdit(payload: Record<string, any>) {
  if (!editing.value) {
    return;
  }
  editSubmitting.value = true;
  const body = {
    description: payload.description,
    name: payload.name,
    parent_id: payload.parent_id ?? '',
    reason: payload.reason,
    status: payload.status,
    version: editing.value.version,
  };
  try {
    await updateRoleApi(editing.value.id, body);
    message.success('角色已更新');
    editOpen.value = false;
    await Promise.all([fetchList(), loadRoleOptions()]);
  } catch (error) {
    handleWriteError(
      error,
      'role_update',
      {
        description: body.description,
        name: body.name,
        parent_id: body.parent_id,
        status: body.status,
      },
      editing.value.id,
    );
  } finally {
    editSubmitting.value = false;
  }
}

async function openGrant(role: Role) {
  const detail = await getRoleApi(role.id);
  granting.value = detail;
  grantPermissions.value = [...detail.permissions];
  grantScopes.value = detail.data_scopes.map((scope) => ({ ...scope }));
  grantReason.value = '';
  grantOpen.value = true;
}

async function saveGrant() {
  if (!granting.value) {
    return;
  }
  grantSubmitting.value = true;
  const body = {
    data_scopes: grantScopes.value,
    permissions: grantPermissions.value,
    reason: grantReason.value,
    version: granting.value.version,
  };
  try {
    const result = await replaceRolePermissionsApi(granting.value.id, body);
    message.success(
      `授权已更新：新增 ${result.added.length}，移除 ${result.removed.length}`,
    );
    grantOpen.value = false;
    await fetchList();
  } catch (error) {
    handleWriteError(
      error,
      'role_permission_replace',
      { data_scopes: body.data_scopes, permissions: body.permissions },
      granting.value.id,
    );
  } finally {
    grantSubmitting.value = false;
  }
}

async function openConstraints(role: Role) {
  const detail = await getRoleConstraintsApi(role.id);
  Object.assign(constraints, {
    max_bindings: detail.max_bindings ?? undefined,
    reason: '',
    required_role_ids: [...detail.required_role_ids],
    version: detail.version,
  });
  editing.value = role;
  constraintsOpen.value = true;
}

async function saveConstraints() {
  if (!editing.value) {
    return;
  }
  constraintsSubmitting.value = true;
  const body = {
    max_bindings: constraints.max_bindings ?? null,
    reason: constraints.reason,
    required_role_ids: constraints.required_role_ids,
    version: constraints.version,
  };
  try {
    await putRoleConstraintsApi(editing.value.id, body);
    message.success('角色约束已保存');
    constraintsOpen.value = false;
  } catch (error) {
    handleWriteError(
      error,
      'role_constraint_update',
      {
        max_bindings: body.max_bindings,
        required_role_ids: body.required_role_ids,
      },
      editing.value.id,
    );
  } finally {
    constraintsSubmitting.value = false;
  }
}

async function openClone(role: Role) {
  cloning.value = role;
  cloneOpen.value = true;
}

async function handleClone(payload: Record<string, any>) {
  if (!cloning.value) {
    return;
  }
  cloneSubmitting.value = true;
  try {
    await cloneRoleApi(cloning.value.id, {
      code: payload.code,
      name: payload.name,
    });
    message.success('角色已复制，新角色为停用状态');
    cloneOpen.value = false;
    await Promise.all([fetchList(), loadRoleOptions()]);
  } catch (error) {
    handleWriteError(error, 'role_clone', payload, cloning.value.id);
  } finally {
    cloneSubmitting.value = false;
  }
}

async function openDelete(role: Role) {
  const detail = await getRoleApi(role.id);
  Modal.confirm({
    content: `角色「${detail.name}」绑定数为 ${detail.binding_count ?? 0}；存在绑定时后端会拒绝删除。`,
    okButtonProps: { danger: true },
    okText: '删除',
    onOk: async () => {
      const body = { reason: '页面删除', version: detail.version };
      try {
        await deleteRoleApi(detail.id, body);
        message.success('角色已删除');
        await Promise.all([fetchList(), loadRoleOptions()]);
      } catch (error) {
        handleWriteError(error, 'role_delete', {}, detail.id);
      }
    },
    title: '删除角色',
  });
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
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    conflictSubmitting.value = false;
  }
}

function isReadonlyRole(role: { role_type: string }) {
  return role.role_type !== 'custom';
}

onMounted(async () => {
  await Promise.all([fetchList(), loadRoleOptions()]);
});
</script>

<template>
  <Page
    description="RBAC 角色、权限与数据范围；写操作受权限码与审批模式约束"
    title="角色管理"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
        <Button v-access:code="'iam:role:write'" @click="createOpen = true">
          新建角色
        </Button>
      </Space>
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
        <template v-if="column.key === 'role_type'">
          <Tag :color="roleTypeMeta[record.role_type]?.color">
            {{ roleTypeMeta[record.role_type]?.text }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'status'">
          <Tag :color="record.status === 'active' ? 'success' : 'default'">
            {{ record.status === 'active' ? '启用' : '停用' }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'updated_at'">
          {{ record.updated_at?.replace('T', ' ').slice(0, 19) }}
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space :size="4" wrap>
            <Button
              v-access:code="'iam:role:grant'"
              :disabled="record.role_type === 'breakglass'"
              size="small"
              type="link"
              @click="openGrant(record as Role)"
            >
              授权
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              :disabled="isReadonlyRole(record as Role)"
              size="small"
              type="link"
              @click="openEdit(record as Role)"
            >
              编辑
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              :disabled="isReadonlyRole(record as Role)"
              size="small"
              type="link"
              @click="openConstraints(record as Role)"
            >
              约束
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              size="small"
              type="link"
              @click="openClone(record as Role)"
            >
              克隆
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              :disabled="isReadonlyRole(record as Role)"
              danger
              size="small"
              type="link"
              @click="openDelete(record as Role)"
            >
              删除
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      :model-value="createModel"
      title="新建角色"
      @submit="handleCreate"
    />

    <FormModal
      v-model:open="editOpen"
      :fields="editFields"
      :loading="editSubmitting"
      :model-value="editing ?? {}"
      title="编辑角色"
      @submit="handleEdit"
    />

    <FormModal
      v-model:open="cloneOpen"
      :fields="cloneFields"
      :loading="cloneSubmitting"
      title="克隆角色"
      @submit="handleClone"
    />

    <Drawer v-model:open="grantOpen" :width="720" title="角色授权">
      <template #extra>
        <Button :loading="grantSubmitting" type="primary" @click="saveGrant">
          保存授权
        </Button>
      </template>
      <div class="space-y-4">
        <div>
          <p class="text-foreground mb-2 text-sm font-medium">权限点</p>
          <PermissionPicker v-model="grantPermissions" />
        </div>
        <div>
          <p class="text-foreground mb-2 text-sm font-medium">数据范围</p>
          <DataScopeEditor v-model="grantScopes" />
        </div>
        <div>
          <p class="text-muted-foreground mb-1 text-xs">变更原因</p>
          <Input.TextArea v-model:value="grantReason" :rows="2" />
        </div>
      </div>
    </Drawer>

    <Modal
      v-model:open="constraintsOpen"
      :confirm-loading="constraintsSubmitting"
      title="角色约束"
      @ok="saveConstraints"
    >
      <Form layout="vertical">
        <FormItem
          label="单角色最大有效绑定数（留空表示不限）"
          name="max_bindings"
        >
          <InputNumber
            v-model:value="constraints.max_bindings"
            :min="1"
            class="w-full"
          />
        </FormItem>
        <FormItem
          label="前置角色（授予该角色前必须已持有）"
          name="required_role_ids"
        >
          <Select
            v-model:value="constraints.required_role_ids"
            :options="roleOptions"
            class="w-full"
            mode="multiple"
          />
        </FormItem>
        <FormItem label="变更原因" name="reason">
          <Input.TextArea v-model:value="constraints.reason" :rows="2" />
        </FormItem>
      </Form>
    </Modal>

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
