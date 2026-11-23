<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type {
  ConfigDeclaredKey,
  ConfigEnv,
  ConfigItem,
  ConfigRelease,
  ConfigReleaseStatus,
} from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';
import { useUserStore } from '@vben/stores';

import {
  Button,
  Descriptions,
  Drawer,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import {
  approveConfigReleaseApi,
  cancelConfigReleaseApi,
  CONFIG_ENV_OPTIONS,
  CONFIG_RELEASE_STATUS_OPTIONS,
  createConfigReleaseApi,
  getConfigErrorMessage,
  getConfigReleaseApi,
  listConfigDeclaredKeysApi,
  listConfigItemsApi,
  listConfigNamespacesApi,
  listConfigReleasesApi,
  publishConfigReleaseApi,
  rollbackConfigReleaseApi,
  submitConfigReleaseApi,
} from '#/api';
import { redirectIfInstallRequired } from '#/api/configcenter/ensure-install';

defineOptions({ name: 'SystemConfigReleases' });

const router = useRouter();
const userStore = useUserStore();
const currentUserId = computed(() => userStore.userInfo?.userId || '');

const namespaces = ref<{ label: string; value: string }[]>([]);
const loading = ref(false);
const rows = ref<ConfigRelease[]>([]);
const createOpen = ref(false);
const createSubmitting = ref(false);
const selectableItems = ref<ConfigItem[]>([]);
const selectedItemIds = ref<number[]>([]);
const itemsLoading = ref(false);
const declaredByKey = ref<Record<string, ConfigDeclaredKey>>({});

const detailOpen = ref(false);
const detail = ref<ConfigRelease>();

const decideOpen = ref(false);
const decideLoading = ref(false);
const decide = reactive<{
  comment: string;
  decision: 'approved' | 'rejected';
  id: number;
}>({ comment: '', decision: 'approved', id: 0 });

const rollbackOpen = ref(false);
const rollbackLoading = ref(false);
const rollback = reactive<{ comment: string; id: number }>({
  comment: '',
  id: 0,
});

const cancelOpen = ref(false);
const cancelLoading = ref(false);
const cancelForm = reactive<{ id: number; reason: string }>({
  id: 0,
  reason: '',
});

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const statusText: Record<ConfigReleaseStatus, string> = {
  active: '已生效',
  approved: '已通过',
  cancelled: '已取消',
  draft: '草稿',
  pending_review: '待审批',
  rejected: '已驳回',
  rolled_back: '已回滚',
  superseded: '已替代',
};

const statusColor: Record<ConfigReleaseStatus, string> = {
  active: 'success',
  approved: 'blue',
  cancelled: 'default',
  draft: 'default',
  pending_review: 'orange',
  rejected: 'error',
  rolled_back: 'warning',
  superseded: 'default',
};

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Select',
      componentProps: { options: CONFIG_ENV_OPTIONS },
      defaultValue: 'dev',
      fieldName: 'env',
      label: '环境',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        options: namespaces,
        showSearch: true,
      },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: CONFIG_RELEASE_STATUS_OPTIONS,
      },
      fieldName: 'status',
      label: '状态',
    },
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '名称或说明' },
      fieldName: 'keyword',
      label: '关键词',
    },
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '创建人 subject id' },
      fieldName: 'owner',
      label: '创建人',
    },
  ],
  showDefaultActions: false,
});

const [CreateForm, createFormApi] = useVbenForm({
  commonConfig: { componentProps: { class: 'w-full' } },
  schema: [
    {
      component: 'Select',
      componentProps: { options: CONFIG_ENV_OPTIONS },
      defaultValue: 'dev',
      fieldName: 'env',
      label: '环境',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: { options: namespaces, showSearch: true },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
    {
      component: 'Input',
      fieldName: 'name',
      label: '发布名称',
      rules: 'required',
    },
    {
      component: 'Input',
      fieldName: 'description',
      label: '说明',
    },
  ],
  showDefaultActions: false,
});

const columns: TableColumnsType = [
  { dataIndex: 'release_no', key: 'release_no', title: '编号', width: 90 },
  { dataIndex: 'name', key: 'name', title: '名称', width: 180 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { dataIndex: 'strategy', key: 'strategy', title: '策略', width: 80 },
  { dataIndex: 'created_by', key: 'created_by', title: '创建人', width: 140 },
  {
    dataIndex: 'submitted_by',
    key: 'submitted_by',
    title: '提交人',
    width: 140,
  },
  { dataIndex: 'created_at', key: 'created_at', title: '创建时间', width: 190 },
  { key: 'actions', title: '操作', width: 360 },
];

const itemColumns: TableColumnsType = [
  { dataIndex: 'key', key: 'key', title: '配置键' },
  {
    dataIndex: 'impact',
    key: 'impact',
    title: '生效方式',
    width: 110,
  },
  {
    dataIndex: 'current_version',
    key: 'current_version',
    title: '版本',
    width: 90,
  },
  { dataIndex: 'value_type', key: 'value_type', title: '类型', width: 100 },
];

const selectionImpact = computed(() => {
  let hot = 0;
  let restart = 0;
  for (const item of selectableItems.value) {
    if (!selectedItemIds.value.includes(item.id)) {
      continue;
    }
    if (declaredByKey.value[item.key]?.hot_whitelist) {
      hot += 1;
    } else {
      restart += 1;
    }
  }
  return { hot, restart };
});

function impactLabel(key: string) {
  return declaredByKey.value[key]?.hot_whitelist ? '热更' : '需重启';
}

function impactColor(key: string) {
  return declaredByKey.value[key]?.hot_whitelist ? 'success' : 'warning';
}

async function loadNamespaces() {
  const result = await listConfigNamespacesApi();
  namespaces.value = result.items.map((item) => ({
    label: `${item.namespace} (${item.key_count})`,
    value: item.namespace,
  }));
  if (namespaces.value[0]) {
    const current = await queryFormApi.getValues();
    if (!current.namespace) {
      await queryFormApi.setValues({ namespace: namespaces.value[0].value });
    }
  }
}

async function fetchList() {
  const values = await queryFormApi.getValues();
  if (!values.env || !values.namespace) {
    message.warning('请选择环境与命名空间');
    return;
  }
  loading.value = true;
  try {
    const result = await listConfigReleasesApi({
      env: values.env as ConfigEnv,
      keyword: (values.keyword as string) || undefined,
      namespace: values.namespace,
      owner: (values.owner as string) || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: (values.status || undefined) as ConfigReleaseStatus | undefined,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载发布单失败'));
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

async function loadDeclaredKeys(namespace: string) {
  if (!namespace) {
    declaredByKey.value = {};
    return;
  }
  const result = await listConfigDeclaredKeysApi(namespace);
  const next: Record<string, ConfigDeclaredKey> = {};
  for (const item of result.items) {
    next[item.key] = item;
  }
  declaredByKey.value = next;
}

async function loadSelectableItems(env: ConfigEnv, namespace: string) {
  itemsLoading.value = true;
  selectedItemIds.value = [];
  try {
    await loadDeclaredKeys(namespace);
    const all: ConfigItem[] = [];
    let page = 1;
    for (;;) {
      const result = await listConfigItemsApi({
        env,
        namespace,
        page,
        pageSize: 100,
      });
      all.push(...result.items.filter((item) => item.current_version > 0));
      if (all.length >= result.total || result.items.length === 0) {
        break;
      }
      page += 1;
    }
    selectableItems.value = all;
  } finally {
    itemsLoading.value = false;
  }
}

async function openCreate() {
  const values = await queryFormApi.getValues();
  createOpen.value = true;
  await createFormApi.reset();
  await createFormApi.setValues({
    env: values.env || 'dev',
    namespace: values.namespace || namespaces.value[0]?.value,
  });
  await loadSelectableItems(
    (values.env || 'dev') as ConfigEnv,
    values.namespace || namespaces.value[0]?.value || '',
  );
}

async function onCreateEnvNamespaceChange() {
  const values = await createFormApi.getValues();
  if (!values.env || !values.namespace) {
    return;
  }
  await loadSelectableItems(values.env, values.namespace);
}

async function submitCreate() {
  const { valid } = await createFormApi.validate();
  if (!valid) {
    return;
  }
  if (selectedItemIds.value.length === 0) {
    message.error('请至少选择一个已有版本的配置键');
    return;
  }
  const values = await createFormApi.getValues();
  const entries = selectableItems.value
    .filter((item) => selectedItemIds.value.includes(item.id))
    .map((item) => ({
      item_id: item.id,
      item_version: item.current_version,
    }));
  createSubmitting.value = true;
  try {
    await createConfigReleaseApi({
      description: values.description || undefined,
      entries,
      env: values.env,
      name: values.name,
      namespace: values.namespace,
      strategy: 'all',
    });
    message.success('已创建发布单（仅全量策略）');
    createOpen.value = false;
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '创建发布单失败'));
  } finally {
    createSubmitting.value = false;
  }
}

async function openDetail(record: ConfigRelease) {
  try {
    await loadDeclaredKeys(record.namespace);
    detail.value = await getConfigReleaseApi(record.id);
    detailOpen.value = true;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载详情失败'));
  }
}

async function submitRelease(record: ConfigRelease) {
  try {
    await submitConfigReleaseApi(record.id);
    message.success('已提交审批');
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '提交失败'));
  }
}

function openApprove(record: ConfigRelease, decision: 'approved' | 'rejected') {
  if (record.submitted_by && record.submitted_by === currentUserId.value) {
    message.error('提交人不得自审');
    return;
  }
  decide.id = record.id;
  decide.decision = decision;
  decide.comment = '';
  decideOpen.value = true;
}

async function confirmDecide() {
  if (decide.decision === 'rejected' && !decide.comment.trim()) {
    message.error('驳回必须填写意见');
    return;
  }
  decideLoading.value = true;
  try {
    await approveConfigReleaseApi(decide.id, {
      comment: decide.comment || undefined,
      decision: decide.decision,
    });
    message.success(decide.decision === 'approved' ? '已通过' : '已驳回');
    decideOpen.value = false;
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '审批失败'));
  } finally {
    decideLoading.value = false;
  }
}

async function publishRelease(record: ConfigRelease) {
  Modal.confirm({
    content: '确认全量发布？发布后所有实例将在刷新周期内切换到该 Release。',
    okText: '发布',
    onOk: async () => {
      try {
        await publishConfigReleaseApi(record.id);
        message.success('已发布');
        fetchList();
      } catch (error) {
        message.error(getConfigErrorMessage(error, '发布失败'));
      }
    },
    title: '确认发布',
  });
}

function openRollback(record: ConfigRelease) {
  rollback.id = record.id;
  rollback.comment = '';
  rollbackOpen.value = true;
}

function openCancel(record: ConfigRelease) {
  cancelForm.id = record.id;
  cancelForm.reason = '';
  cancelOpen.value = true;
}

async function confirmCancel() {
  if (!cancelForm.reason.trim()) {
    message.error('取消必须填写原因');
    return;
  }
  cancelLoading.value = true;
  try {
    await cancelConfigReleaseApi(cancelForm.id, {
      reason: cancelForm.reason.trim(),
    });
    message.success('已取消发布单');
    cancelOpen.value = false;
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '取消失败'));
  } finally {
    cancelLoading.value = false;
  }
}

function canCancel(record: ConfigRelease) {
  return (
    record.status === 'draft' ||
    record.status === 'pending_review' ||
    record.status === 'approved'
  );
}

async function confirmRollback() {
  rollbackLoading.value = true;
  try {
    await rollbackConfigReleaseApi(rollback.id, {
      break_glass: true,
      break_glass_reason:
        rollback.comment || 'emergency rollback from admin UI',
      comment: rollback.comment || undefined,
    });
    message.success('已破玻璃回滚（须 24h 内补审）');
    rollbackOpen.value = false;
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '回滚失败'));
  } finally {
    rollbackLoading.value = false;
  }
}

function canApprove(record: ConfigRelease) {
  return (
    record.status === 'pending_review' &&
    record.submitted_by !== currentUserId.value
  );
}

function onItemSelectChange(keys: (number | string)[]) {
  selectedItemIds.value = keys.map(Number);
}

onMounted(async () => {
  if (await redirectIfInstallRequired(router)) {
    return;
  }
  try {
    await loadNamespaces();
    await fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '初始化失败'));
  }
});
</script>

<template>
  <Page
    description="M1 仅支持 strategy=all；写 / 审批 / 发布三权分离，提交人不得自审"
    title="发布与回滚"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button
          v-access:code="'config:write'"
          type="primary"
          @click="openCreate"
        >
          新建发布单
        </Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      :scroll="{ x: 1400 }"
      row-key="id"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'status'">
          <Tag :color="statusColor[record.status as ConfigReleaseStatus]">
            {{ statusText[record.status as ConfigReleaseStatus] }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space wrap>
            <Button
              size="small"
              type="link"
              @click="openDetail(record as ConfigRelease)"
            >
              详情
            </Button>
            <Button
              v-if="record.status === 'draft'"
              v-access:code="'config:write'"
              size="small"
              type="link"
              @click="submitRelease(record as ConfigRelease)"
            >
              提交
            </Button>
            <Button
              v-if="canApprove(record as ConfigRelease)"
              v-access:code="'config:approve'"
              size="small"
              type="link"
              @click="openApprove(record as ConfigRelease, 'approved')"
            >
              通过
            </Button>
            <Button
              v-if="canApprove(record as ConfigRelease)"
              v-access:code="'config:approve'"
              danger
              size="small"
              type="link"
              @click="openApprove(record as ConfigRelease, 'rejected')"
            >
              驳回
            </Button>
            <Button
              v-if="record.status === 'approved'"
              v-access:code="'config:publish'"
              size="small"
              type="link"
              @click="publishRelease(record as ConfigRelease)"
            >
              发布
            </Button>
            <Button
              v-if="canCancel(record as ConfigRelease)"
              v-access:code="'config:write'"
              danger
              size="small"
              type="link"
              @click="openCancel(record as ConfigRelease)"
            >
              取消
            </Button>
            <Button
              v-if="
                record.status === 'active' || record.status === 'superseded'
              "
              v-access:code="'config:publish'"
              danger
              size="small"
              type="link"
              @click="openRollback(record as ConfigRelease)"
            >
              回滚
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <Modal
      v-model:open="createOpen"
      :confirm-loading="createSubmitting"
      title="新建发布单（全量）"
      width="800px"
      @ok="submitCreate"
    >
      <CreateForm />
      <div class="mb-2 mt-2 flex items-center justify-between">
        <span class="font-medium">选择配置键（须已有版本）</span>
        <Button size="small" @click="onCreateEnvNamespaceChange">
          按表单环境刷新
        </Button>
      </div>
      <p v-if="selectedItemIds.length > 0" class="mb-2 text-sm text-gray-500">
        已选 {{ selectedItemIds.length }} 项：热更 {{ selectionImpact.hot }} /
        需重启
        {{ selectionImpact.restart }}
        （ADR-0015 白名单）
      </p>
      <Table
        :columns="itemColumns"
        :data-source="selectableItems"
        :loading="itemsLoading"
        :pagination="false"
        :row-selection="{
          onChange: onItemSelectChange,
          selectedRowKeys: selectedItemIds,
        }"
        :scroll="{ y: 320 }"
        row-key="id"
        size="small"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'impact'">
            <Tag :color="impactColor((record as ConfigItem).key)">
              {{ impactLabel((record as ConfigItem).key) }}
            </Tag>
          </template>
        </template>
      </Table>
    </Modal>

    <Drawer v-model:open="detailOpen" title="发布单详情" width="640">
      <template v-if="detail">
        <Descriptions :column="1" bordered size="small">
          <Descriptions.Item label="名称">{{ detail.name }}</Descriptions.Item>
          <Descriptions.Item label="编号">
            {{ detail.release_no }}
          </Descriptions.Item>
          <Descriptions.Item label="状态">
            {{ statusText[detail.status] }}
          </Descriptions.Item>
          <Descriptions.Item label="策略">
            {{ detail.strategy }}
          </Descriptions.Item>
          <Descriptions.Item label="快照哈希">
            {{ detail.snapshot_hash }}
          </Descriptions.Item>
          <Descriptions.Item label="说明">
            {{ detail.description || '—' }}
          </Descriptions.Item>
        </Descriptions>
        <div class="mb-2 mt-4 font-medium">包含键</div>
        <Table
          :columns="[
            { dataIndex: 'config_key', key: 'config_key', title: '键' },
            {
              dataIndex: 'impact',
              key: 'impact',
              title: '生效方式',
              width: 110,
            },
            { dataIndex: 'item_version', title: '版本', width: 90 },
            { dataIndex: 'value_hash', title: '哈希' },
          ]"
          :data-source="detail.items || []"
          :pagination="false"
          row-key="item_id"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'impact'">
              <Tag :color="impactColor(record.config_key)">
                {{ impactLabel(record.config_key) }}
              </Tag>
            </template>
          </template>
        </Table>
      </template>
    </Drawer>

    <Modal
      v-model:open="decideOpen"
      :confirm-loading="decideLoading"
      :title="decide.decision === 'approved' ? '审批通过' : '审批驳回'"
      @ok="confirmDecide"
    >
      <Input.TextArea
        v-model:value="decide.comment"
        :placeholder="
          decide.decision === 'rejected' ? '驳回意见必填' : '可选意见'
        "
        :rows="4"
      />
    </Modal>

    <Modal
      v-model:open="rollbackOpen"
      :confirm-loading="rollbackLoading"
      title="确认回滚"
      @ok="confirmRollback"
    >
      <p class="mb-2 text-sm text-orange-600">
        回滚会生成新 Release 指向历史快照，并走发布指针切换；请填写原因。
      </p>
      <Input.TextArea
        v-model:value="rollback.comment"
        placeholder="回滚说明"
        :rows="4"
      />
    </Modal>

    <Modal
      v-model:open="cancelOpen"
      :confirm-loading="cancelLoading"
      title="取消发布单"
      @ok="confirmCancel"
    >
      <p class="mb-2 text-sm text-gray-500">
        仅未生效的草稿 / 待审 / 已通过发布单可取消；取消后不可恢复。
      </p>
      <Input.TextArea
        v-model:value="cancelForm.reason"
        placeholder="取消原因（必填）"
        :rows="4"
      />
    </Modal>
  </Page>
</template>
