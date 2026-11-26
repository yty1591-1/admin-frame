<script lang="ts" setup>
import type { TableColumnsType } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { NotificationTemplate } from '#/api';

import { reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import { Button, message, Table, Tag } from 'ant-design-vue';

import {
  listNotificationTemplatesApi,
  upsertNotificationTemplateApi,
} from '#/api';

import FormModal from '../../iam/components/form-modal.vue';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

defineOptions({ name: 'MessagingTemplates' });

const [QueryForm, queryFormApi] = useVbenForm(
  withListQueryForm({
    handleReset: async () => {
      await queryFormApi.reset();
      rows.value = [];
      queriedKey.value = '';
    },
    handleSubmit: async () => {
      await handleSearch();
    },
    schema: [
      {
        component: 'Input',
        componentProps: { allowClear: true, placeholder: '模板键' },
        fieldName: 'key',
        label: '模板键',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<NotificationTemplate[]>([]);
const queriedKey = ref('');

const columns: TableColumnsType = [
  { dataIndex: 'key', key: 'key', title: '模板键', width: 180 },
  { dataIndex: 'channel', key: 'channel', title: '渠道', width: 100 },
  { dataIndex: 'version', key: 'version', title: '版本', width: 90 },
  { dataIndex: 'enabled', key: 'enabled', title: '启用', width: 100 },
  { dataIndex: 'locale', key: 'locale', title: '语言', width: 100 },
  { dataIndex: 'updated_at', key: 'updated_at', title: '更新于', width: 180 },
];

const channelMap = reactive<Record<string, string>>({
  email: '邮件',
  push: '推送',
  sms: '短信',
});

const registerOpen = ref(false);
const registerSubmitting = ref(false);
const registerFields: FormField[] = [
  { label: '模板键', name: 'key', required: true, span: 2 },
  {
    component: 'select',
    label: '渠道',
    name: 'channel',
    options: [
      { label: '邮件', value: 'email' },
      { label: '短信', value: 'sms' },
      { label: '推送', value: 'push' },
    ],
    required: true,
    span: 2,
  },
  { label: '主题（邮件必填）', name: 'subject', span: 2 },
  {
    component: 'textarea',
    label: '正文',
    name: 'body',
    required: true,
    rows: 6,
    span: 2,
  },
  { label: '语言', name: 'locale', placeholder: 'zh-CN', span: 2 },
];

async function handleSearch() {
  const values = await queryFormApi.getValues();
  const key = String(values.key ?? '').trim();
  if (!key) {
    message.warning('请输入模板键');
    return;
  }
  loading.value = true;
  try {
    const result = await listNotificationTemplatesApi(key);
    rows.value = result.items;
    queriedKey.value = key;
  } finally {
    loading.value = false;
  }
}

async function handleRegister(payload: Record<string, any>) {
  registerSubmitting.value = true;
  try {
    await upsertNotificationTemplateApi({
      body: payload.body,
      channel: payload.channel,
      key: payload.key,
      locale: payload.locale || undefined,
      subject: payload.subject || undefined,
    });
    message.success('模板已登记');
    registerOpen.value = false;
    await queryFormApi.setValues({ key: payload.key });
    await handleSearch();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    registerSubmitting.value = false;
  }
}

function rowKey(record: NotificationTemplate) {
  return `${record.key}-${record.channel}-${record.version}`;
}
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <QueryForm class="min-w-0 flex-1" />
        <Button
          v-access:code="'notification:write'"
          type="primary"
          @click="registerOpen = true"
        >
          登记模板
        </Button>
      </div>

      <Table
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :locale="{
          emptyText: queriedKey ? '暂无模板版本' : '请先输入模板键查询',
        }"
        :pagination="false"
        :row-key="rowKey"
        size="middle"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'channel'">
            {{ channelMap[record.channel] ?? record.channel }}
          </template>
          <template v-else-if="column.key === 'enabled'">
            <Tag :color="record.enabled ? 'success' : 'default'">
              {{ record.enabled ? '启用' : '停用' }}
            </Tag>
          </template>
        </template>
      </Table>
    </div>

    <FormModal
      v-model:open="registerOpen"
      :fields="registerFields"
      :loading="registerSubmitting"
      submit-text="登记"
      title="登记通知模板"
      @submit="handleRegister"
    />
  </Page>
</template>
