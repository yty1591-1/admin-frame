<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { SodGroup, SodViolation } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import {
  Button,
  Form,
  FormItem,
  Input,
  InputNumber,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  createSodGroupApi,
  deleteSodGroupApi,
  listSodGroupsApi,
  listSodViolationsApi,
  preflightSodGroupApi,
  updateSodGroupApi,
} from '#/api';

import RoleMultiSelect from '../components/role-multi-select.vue';

defineOptions({ name: 'IamSod' });

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '编码 / 名称' },
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
        placeholder: '状态',
      },
      fieldName: 'status',
      label: '状态',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<SodGroup[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'code', key: 'code', title: '编码', width: 180 },
  { dataIndex: 'name', key: 'name', title: '名称' },
  {
    dataIndex: 'max_active_roles',
    key: 'max_active_roles',
    title: '阈值',
    width: 90,
  },
  { dataIndex: 'role_ids', key: 'role_ids', title: '互斥角色数', width: 110 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 90 },
  { dataIndex: 'version', key: 'version', title: '版本', width: 80 },
  { key: 'actions', title: '操作', width: 260 },
];

const formOpen = ref(false);
const formSubmitting = ref(false);
const editing = ref<SodGroup>();
const form = reactive({
  code: '',
  description: '',
  max_active_roles: 1,
  name: '',
  reason: '',
  role_ids: [] as string[],
});

const violationsOpen = ref(false);
const violationsLoading = ref(false);
const violations = ref<SodViolation[]>([]);
const violationsTitle = ref('违规主体');

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listSodGroupsApi({
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

function openCreate() {
  editing.value = undefined;
  Object.assign(form, {
    code: '',
    description: '',
    max_active_roles: 1,
    name: '',
    reason: '',
    role_ids: [],
  });
  formOpen.value = true;
}

function openEdit(item: SodGroup) {
  editing.value = item;
  Object.assign(form, {
    code: item.code,
    description: item.description,
    max_active_roles: item.max_active_roles,
    name: item.name,
    reason: '',
    role_ids: [...item.role_ids],
  });
  formOpen.value = true;
}

async function saveForm() {
  if (!form.code.trim() || !form.name.trim() || form.role_ids.length < 2) {
    message.warning('编码、名称必填，且互斥角色至少 2 个');
    return;
  }
  formSubmitting.value = true;
  try {
    await (editing.value
      ? updateSodGroupApi(editing.value.id, {
          description: form.description,
          max_active_roles: form.max_active_roles,
          name: form.name,
          reason: form.reason,
          role_ids: form.role_ids,
          version: editing.value.version,
        })
      : createSodGroupApi({
          code: form.code,
          description: form.description,
          max_active_roles: form.max_active_roles,
          name: form.name,
          reason: form.reason,
          role_ids: form.role_ids,
        }));
    message.success('互斥组已保存');
    formOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    formSubmitting.value = false;
  }
}

function handleDelete(item: SodGroup) {
  Modal.confirm({
    content: '删除后保留编码与历史关系，仅停止参与鉴权预检。',
    okButtonProps: { danger: true },
    okText: '删除',
    onOk: async () => {
      try {
        await deleteSodGroupApi(item.id, {
          reason: '页面删除',
          version: item.version,
        });
        message.success('互斥组已删除');
        await fetchList();
      } catch {
        // 错误已由全局请求拦截器提示
      }
    },
    title: '删除互斥组',
  });
}

async function preflight(item: SodGroup) {
  violationsLoading.value = true;
  violationsTitle.value = `预检：${item.name}`;
  violationsOpen.value = true;
  try {
    const result = await preflightSodGroupApi({
      max_active_roles: item.max_active_roles,
      role_ids: item.role_ids,
    });
    violations.value = result.items;
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    violationsLoading.value = false;
  }
}

async function loadViolations(item: SodGroup) {
  violationsLoading.value = true;
  violationsTitle.value = `存量违规：${item.name}`;
  violationsOpen.value = true;
  try {
    const result = await listSodViolationsApi(item.id);
    violations.value = result.items;
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    violationsLoading.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="互斥角色组（SoD）：同一主体同时持有的互斥角色不得超过阈值"
    title="职责分离"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
        <Button v-access:code="'iam:sod:write'" @click="openCreate">
          新建互斥组
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
        <template v-if="column.key === 'role_ids'">
          {{ record.role_ids.length }}
        </template>
        <template v-else-if="column.key === 'status'">
          <Tag :color="record.status === 'active' ? 'success' : 'default'">
            {{ record.status === 'active' ? '启用' : '停用' }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space :size="4" wrap>
            <Button
              size="small"
              type="link"
              @click="preflight(record as SodGroup)"
            >
              预检
            </Button>
            <Button
              size="small"
              type="link"
              @click="loadViolations(record as SodGroup)"
            >
              违规
            </Button>
            <Button
              v-access:code="'iam:sod:write'"
              size="small"
              type="link"
              @click="openEdit(record as SodGroup)"
            >
              编辑
            </Button>
            <Button
              v-access:code="'iam:sod:write'"
              danger
              size="small"
              type="link"
              @click="handleDelete(record as SodGroup)"
            >
              删除
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <Modal
      v-model:open="formOpen"
      :confirm-loading="formSubmitting"
      :title="editing ? '编辑互斥组' : '新建互斥组'"
      width="720"
      @ok="saveForm"
    >
      <Form layout="vertical">
        <div class="grid grid-cols-2 gap-x-4">
          <FormItem label="编码" name="code" required>
            <Input v-model:value="form.code" :disabled="!!editing" />
          </FormItem>
          <FormItem label="名称" name="name" required>
            <Input v-model:value="form.name" />
          </FormItem>
          <FormItem label="阈值（同时持有角色上限）" name="max_active_roles">
            <InputNumber
              v-model:value="form.max_active_roles"
              :max="64"
              :min="1"
              class="w-full"
            />
          </FormItem>
          <FormItem label="互斥角色" name="role_ids" required>
            <RoleMultiSelect v-model="form.role_ids" />
          </FormItem>
          <FormItem class="col-span-2" label="描述" name="description">
            <Input.TextArea v-model:value="form.description" :rows="2" />
          </FormItem>
          <FormItem class="col-span-2" label="变更原因" name="reason">
            <Input.TextArea v-model:value="form.reason" :rows="2" />
          </FormItem>
        </div>
      </Form>
    </Modal>

    <Modal
      v-model:open="violationsOpen"
      :footer="null"
      :title="violationsTitle"
      width="680"
    >
      <Table
        :data-source="violations"
        :loading="violationsLoading"
        :pagination="false"
        row-key="subject_id"
        size="small"
      >
        <Table.Column
          key="subject_id"
          title="主体 ID"
          data-index="subject_id"
        />
        <Table.Column key="role_ids" title="持有角色">
          <template #default="{ record }">
            <Tag v-for="role in record.role_ids" :key="role" class="mb-1">
              {{ role }}
            </Tag>
          </template>
        </Table.Column>
      </Table>
    </Modal>
  </Page>
</template>
