<script lang="ts" setup>
import type { AuthorizeResult, Permission } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Form,
  FormItem,
  Input,
  message,
  Select,
  Space,
  Tag,
} from 'ant-design-vue';

import { listAllPermissionsApi, simulateAuthorizeApi } from '#/api';

import JsonPreview from '../components/json-preview.vue';

defineOptions({ name: 'IamCheck' });

const loading = ref(false);
const permissions = ref<Permission[]>([]);
const result = ref<AuthorizeResult>();

const form = reactive({
  merchant_id: '',
  permission: '',
  resource_id: '',
  resource_type: '',
  subject_id: '',
});

const permissionOptions = ref<{ label: string; value: string }[]>([]);

onMounted(async () => {
  permissions.value = await listAllPermissionsApi();
  permissionOptions.value = permissions.value.map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }));
});

async function handleSubmit() {
  if (!form.subject_id.trim() || !form.permission) {
    message.warning('请填写主体 ID 并选择权限点');
    return;
  }
  loading.value = true;
  try {
    result.value = await simulateAuthorizeApi({
      permission: form.permission,
      resource:
        form.resource_type || form.resource_id || form.merchant_id
          ? {
              id: form.resource_id || undefined,
              merchant_id: form.merchant_id || undefined,
              type: form.resource_type || undefined,
            }
          : undefined,
      subject_id: form.subject_id.trim(),
      subject_type: 'staff',
    });
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <Page>
    <div class="bg-card mb-4 rounded-lg p-4">
      <Form layout="vertical">
        <div class="grid grid-cols-2 gap-x-4">
          <FormItem label="主体 ID（staff）" required>
            <Input
              v-model:value="form.subject_id"
              placeholder="账号 ID / 主体标识"
            />
          </FormItem>
          <FormItem label="权限点" required>
            <Select
              v-model:value="form.permission"
              :options="permissionOptions"
              allow-clear
              class="w-full"
              placeholder="选择权限码"
              show-search
            />
          </FormItem>
          <FormItem label="资源类型（可选）">
            <Input
              v-model:value="form.resource_type"
              placeholder="如 role / order"
            />
          </FormItem>
          <FormItem label="资源 ID（可选）">
            <Input v-model:value="form.resource_id" />
          </FormItem>
          <FormItem label="商户 ID（可选）">
            <Input v-model:value="form.merchant_id" />
          </FormItem>
        </div>
        <Space>
          <Button :loading="loading" type="primary" @click="handleSubmit">
            执行鉴权
          </Button>
          <Button @click="result = undefined">清空结果</Button>
        </Space>
      </Form>
    </div>

    <div v-if="result" class="bg-card rounded-lg p-4">
      <Space class="mb-3">
        <Tag :color="result.allowed ? 'success' : 'error'">
          {{ result.allowed ? '允许' : '拒绝' }}
        </Tag>
        <span class="text-foreground text-sm">{{ result.reason }}</span>
        <span class="text-muted-foreground text-xs">
          snapshot v{{ result.snapshot_version }}
        </span>
      </Space>
      <div class="mb-3">
        <p class="text-muted-foreground mb-1 text-xs">命中角色</p>
        <Space wrap>
          <Tag v-for="role in result.matched_roles" :key="role" color="blue">
            {{ role }}
          </Tag>
          <span
            v-if="result.matched_roles.length === 0"
            class="text-muted-foreground text-sm"
          >
            无
          </span>
        </Space>
      </div>
      <div>
        <p class="text-muted-foreground mb-1 text-xs">数据范围</p>
        <JsonPreview :value="result.data_scope ?? null" />
      </div>
    </div>
  </Page>
</template>
