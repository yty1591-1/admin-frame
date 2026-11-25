<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../components/types';

import type { DataScope, RoleTemplate, TemplateDrift } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Drawer,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  createTemplateApi,
  deleteTemplateApi,
  getErrorMessage,
  getTemplateDriftApi,
  instantiateTemplateApi,
  listTemplatesApi,
  replaceTemplatePermissionsApi,
  syncTemplateApi,
  updateTemplateApi,
} from '#/api';

import DataScopeEditor from '../components/data-scope-editor.vue';
import FormModal from '../components/form-modal.vue';
import PermissionPicker from '../components/permission-picker.vue';

defineOptions({ name: 'IamTemplate' });

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: 'A 端', value: 'admin' },
          { label: 'B 端', value: 'biz' },
        ],
        placeholder: '端别',
      },
      fieldName: 'channel',
      label: '端别',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 'active' },
          { label: '停用', value: 'disabled' },
        ],
        placeholder: '状态',
      },
      fieldName: 'status',
      label: '状态',
    },
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '模板码 / 名称' },
      fieldName: 'keyword',
      label: '关键字',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<RoleTemplate[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'code', key: 'code', title: '模板码', width: 190 },
  { dataIndex: 'name', key: 'name', title: '名称' },
  { dataIndex: 'channel', key: 'channel', title: '端别', width: 90 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 90 },
  { dataIndex: 'version', key: 'version', title: '版本', width: 80 },
  { dataIndex: 'updated_at', key: 'updated_at', title: '更新时间', width: 190 },
  { key: 'actions', title: '操作', width: 360 },
];

const createOpen = ref(false);
const createSubmitting = ref(false);
const editOpen = ref(false);
const editSubmitting = ref(false);
const editing = ref<RoleTemplate>();

const grantOpen = ref(false);
const grantSubmitting = ref(false);
const granting = ref<RoleTemplate>();
const grantPermissions = ref<string[]>([]);
const grantScopes = ref<DataScope[]>([]);

const driftOpen = ref(false);
const driftLoading = ref(false);
const driftRows = ref<TemplateDrift[]>([]);
const driftTemplate = ref<RoleTemplate>();

const instantiateOpen = ref(false);
const instantiateSubmitting = ref(false);
const instantiating = ref<RoleTemplate>();

const createFields: FormField[] = [
  {
    component: 'select',
    label: '端别',
    name: 'channel',
    options: [
      { label: 'A 端', value: 'admin' },
      { label: 'B 端', value: 'biz' },
    ],
    required: true,
  },
  { label: '模板码', name: 'code', required: true },
  { label: '模板名称', name: 'name', required: true, span: 2 },
  { component: 'textarea', label: '描述', name: 'description', span: 2 },
];

const editFields = computed<FormField[]>(() => [
  { disabled: true, label: '模板码', name: 'code' },
  { label: '模板名称', name: 'name', required: true },
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
  { component: 'textarea', label: '描述', name: 'description', span: 2 },
]);

const instantiateFields: FormField[] = [
  {
    component: 'select',
    label: '作用域',
    name: 'scope_type',
    options: [
      { label: '平台角色（platform）', value: 'platform' },
      { label: '商户角色（merchant）', value: 'merchant' },
    ],
    required: true,
  },
  { label: '新角色码', name: 'code', required: true },
  { label: '新角色名称', name: 'name', required: true },
  {
    component: 'textarea',
    label: '实例化原因',
    name: 'reason',
    rows: 2,
    span: 2,
  },
];

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listTemplatesApi({
      channel: values.channel || undefined,
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
    await createTemplateApi({
      channel: payload.channel,
      code: payload.code,
      data_scopes: [],
      description: payload.description,
      name: payload.name,
      permissions: [],
    });
    message.success('模板已创建，请继续配置权限基线');
    createOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    createSubmitting.value = false;
  }
}

async function handleEdit(payload: Record<string, any>) {
  if (!editing.value) {
    return;
  }
  editSubmitting.value = true;
  try {
    await updateTemplateApi(editing.value.id, {
      description: payload.description,
      name: payload.name,
      status: payload.status,
      version: editing.value.version,
    });
    message.success('模板已更新');
    editOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    editSubmitting.value = false;
  }
}

function openCreate() {
  createOpen.value = true;
}

function openEdit(item: RoleTemplate) {
  editing.value = item;
  editOpen.value = true;
}

async function openGrant(item: RoleTemplate) {
  granting.value = item;
  grantPermissions.value = [...item.permissions];
  grantScopes.value = item.data_scopes.map((scope) => ({ ...scope }));
  grantOpen.value = true;
}

async function saveGrant() {
  if (!granting.value) {
    return;
  }
  grantSubmitting.value = true;
  try {
    await replaceTemplatePermissionsApi(granting.value.id, {
      data_scopes: grantScopes.value,
      permissions: grantPermissions.value,
      version: granting.value.version,
    });
    message.success('模板权限基线已保存');
    grantOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    grantSubmitting.value = false;
  }
}

async function openDrift(item: RoleTemplate) {
  driftTemplate.value = item;
  driftOpen.value = true;
  driftLoading.value = true;
  try {
    const result = await getTemplateDriftApi(item.id);
    driftRows.value = result.items;
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    driftLoading.value = false;
  }
}

async function handleSync(item: RoleTemplate) {
  try {
    const preview = await syncTemplateApi(item.id, {
      dry_run: true,
      reason: '页面预检',
    });
    Modal.confirm({
      content: `待同步实例 ${preview.total} 个；确认后将把模板权限与数据范围覆盖到实例角色。`,
      onOk: async () => {
        try {
          const result = await syncTemplateApi(item.id, {
            dry_run: false,
            reason: '页面同步',
          });
          message.success(`已同步 ${result.total} 个实例`);
          await fetchList();
        } catch (error) {
          message.error(getErrorMessage(error));
        }
      },
      title: '同步模板到实例',
    });
  } catch (error) {
    message.error(getErrorMessage(error));
  }
}

function openInstantiate(item: RoleTemplate) {
  instantiating.value = item;
  instantiateOpen.value = true;
}

async function handleInstantiate(payload: Record<string, any>) {
  if (!instantiating.value) {
    return;
  }
  instantiateSubmitting.value = true;
  try {
    await instantiateTemplateApi(instantiating.value.id, {
      channel: instantiating.value.channel,
      code: payload.code,
      name: payload.name,
      reason: payload.reason,
      scope_type: payload.scope_type,
    });
    message.success('角色已按模板实例化');
    instantiateOpen.value = false;
    await fetchList();
  } catch (error) {
    message.error(getErrorMessage(error));
  } finally {
    instantiateSubmitting.value = false;
  }
}

function handleDelete(item: RoleTemplate) {
  Modal.confirm({
    content: `确认删除模板「${item.name}」？既有实例角色会保留。`,
    okButtonProps: { danger: true },
    okText: '删除',
    onOk: async () => {
      try {
        await deleteTemplateApi(item.id, {
          reason: '页面删除',
          version: item.version,
        });
        message.success('模板已删除');
        await fetchList();
      } catch (error) {
        message.error(getErrorMessage(error));
      }
    },
    title: '删除角色模板',
  });
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="平台定义权限基线，商户或平台按模板实例化角色，并可查看漂移与同步"
    title="角色模板"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
        <Button v-access:code="'iam:role:write'" @click="openCreate">
          新建模板
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
        <template v-if="column.key === 'channel'">
          <Tag :color="record.channel === 'admin' ? 'blue' : 'green'">
            {{ record.channel === 'admin' ? 'A 端' : 'B 端' }}
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
              size="small"
              type="link"
              @click="openGrant(record as RoleTemplate)"
            >
              权限基线
            </Button>
            <Button
              size="small"
              type="link"
              @click="openDrift(record as RoleTemplate)"
            >
              漂移
            </Button>
            <Button
              v-access:code="'iam:role:grant'"
              size="small"
              type="link"
              @click="handleSync(record as RoleTemplate)"
            >
              同步
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              size="small"
              type="link"
              @click="openInstantiate(record as RoleTemplate)"
            >
              实例化
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              size="small"
              type="link"
              @click="openEdit(record as RoleTemplate)"
            >
              编辑
            </Button>
            <Button
              v-access:code="'iam:role:write'"
              danger
              size="small"
              type="link"
              @click="handleDelete(record as RoleTemplate)"
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
      title="新建角色模板"
      @submit="handleCreate"
    />

    <FormModal
      v-model:open="editOpen"
      :fields="editFields"
      :loading="editSubmitting"
      :model-value="editing ?? {}"
      title="编辑角色模板"
      @submit="handleEdit"
    />

    <FormModal
      v-model:open="instantiateOpen"
      :fields="instantiateFields"
      :loading="instantiateSubmitting"
      :model-value="{ scope_type: 'platform' }"
      title="按模板实例化角色"
      @submit="handleInstantiate"
    />

    <Drawer v-model:open="grantOpen" :width="720" title="模板权限基线">
      <template #extra>
        <Button :loading="grantSubmitting" type="primary" @click="saveGrant">
          保存基线
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
      </div>
    </Drawer>

    <Drawer
      v-model:open="driftOpen"
      :width="640"
      :title="`模板漂移：${driftTemplate?.name ?? ''}`"
    >
      <Table
        :data-source="driftRows"
        :loading="driftLoading"
        :pagination="false"
        row-key="role_id"
        size="small"
      >
        <Table.Column key="role_code" title="实例角色" data-index="role_code" />
        <Table.Column
          key="role_version"
          title="角色版本"
          data-index="role_version"
        />
        <Table.Column
          key="template_version"
          title="模板版本"
          data-index="template_version"
        />
      </Table>
    </Drawer>
  </Page>
</template>
