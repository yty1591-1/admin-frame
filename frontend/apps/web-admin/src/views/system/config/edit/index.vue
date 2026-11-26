<script lang="ts" setup>
import type { ConfigItem } from '#/api';

import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import {
  Button,
  Descriptions,
  Input,
  message,
  Space,
  Tag,
} from 'ant-design-vue';

import {
  getConfigItemApi,
  transitionConfigItemApi,
  updateConfigItemValueApi,
} from '#/api';

defineOptions({ name: 'SystemConfigEdit' });

const route = useRoute();
const router = useRouter();

const loading = ref(false);
const saving = ref(false);
const transitioning = ref(false);
const item = ref<ConfigItem>();
const value = ref('');
const changeSummary = ref('');

const itemId = computed(() => Number(route.query.id || 0));

const canEdit = computed(
  () => !!item.value && !item.value.sensitive && itemId.value > 0,
);

const canDeprecate = computed(() => item.value?.status === 'active');
const canArchive = computed(
  () =>
    item.value?.status === 'active' ||
    item.value?.status === 'deprecated' ||
    item.value?.status === 'draft',
);

async function loadItem() {
  if (!itemId.value) {
    message.error('缺少配置项 id');
    return;
  }
  loading.value = true;
  try {
    const detail = await getConfigItemApi(itemId.value);
    item.value = detail;
    value.value = detail.value ?? '';
    changeSummary.value = '';
    if (detail.sensitive) {
      message.warning('敏感键由密钥子域管理（M2），本页不可写值');
    }
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (!item.value || !canEdit.value) {
    return;
  }
  saving.value = true;
  try {
    const updated = await updateConfigItemValueApi(item.value.id, {
      change_summary: changeSummary.value || undefined,
      expected_version: item.value.version,
      value: value.value,
    });
    item.value = updated;
    value.value = updated.value ?? value.value;
    changeSummary.value = '';
    message.success(`已写入版本 ${updated.current_version}`);
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    saving.value = false;
  }
}

async function transitionTo(to: 'active' | 'archived' | 'deprecated') {
  if (!item.value) {
    return;
  }
  transitioning.value = true;
  try {
    const updated = await transitionConfigItemApi(item.value.id, {
      expected_version: item.value.version,
      to,
    });
    item.value = updated;
    message.success(`状态已迁移为 ${updated.status}`);
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    transitioning.value = false;
  }
}

function goVersions() {
  router.push({
    path: '/system/config/versions',
    query: { id: String(itemId.value) },
  });
}

function goBack() {
  router.push('/system/config/items');
}

watch(
  () => route.query.id,
  () => {
    loadItem();
  },
);

onMounted(loadItem);
</script>

<template>
  <Page>
    <div v-if="item" class="bg-card mb-4 rounded-lg p-4">
      <Descriptions :column="2" bordered size="small">
        <Descriptions.Item label="配置键">{{ item.key }}</Descriptions.Item>
        <Descriptions.Item label="环境 / 命名空间">
          {{ item.env }} / {{ item.namespace }}
        </Descriptions.Item>
        <Descriptions.Item label="类型">
          {{ item.value_type }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag>{{ item.status }}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="当前版本">
          {{ item.current_version }}
        </Descriptions.Item>
        <Descriptions.Item label="元数据版本">
          {{ item.version }}
        </Descriptions.Item>
        <Descriptions.Item label="敏感">
          <Tag :color="item.sensitive ? 'error' : 'default'">
            {{ item.sensitive ? '是' : '否' }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="负责人">
          {{ item.owner || '—' }}
        </Descriptions.Item>
        <Descriptions.Item :span="2" label="说明">
          {{ item.description || '—' }}
        </Descriptions.Item>
        <Descriptions.Item :span="2" label="当前哈希">
          {{ item.value_hash || '—' }}
        </Descriptions.Item>
      </Descriptions>
    </div>

    <div class="bg-card rounded-lg p-4">
      <div class="mb-2 font-medium">新值</div>
      <Input.TextArea
        v-model:value="value"
        :disabled="!canEdit || loading"
        :rows="8"
        placeholder="写入后追加为新版本"
      />
      <div class="mb-2 mt-4 font-medium">变更摘要</div>
      <Input
        v-model:value="changeSummary"
        :disabled="!canEdit || loading"
        placeholder="可选"
      />
      <Space class="mt-4" wrap>
        <Button
          v-access:code="'config:write'"
          :disabled="!canEdit"
          :loading="saving"
          type="primary"
          @click="save"
        >
          保存新版本
        </Button>
        <Button
          v-if="item?.status === 'draft'"
          v-access:code="'config:write'"
          :loading="transitioning"
          @click="transitionTo('active')"
        >
          启用
        </Button>
        <Button
          v-if="canDeprecate"
          v-access:code="'config:write'"
          :loading="transitioning"
          @click="transitionTo('deprecated')"
        >
          废弃
        </Button>
        <Button
          v-if="canArchive"
          v-access:code="'config:write'"
          :loading="transitioning"
          danger
          @click="transitionTo('archived')"
        >
          归档
        </Button>
        <Button @click="goVersions">查看版本历史</Button>
        <Button @click="goBack">返回列表</Button>
      </Space>
    </div>
  </Page>
</template>
