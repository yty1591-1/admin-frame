<script lang="ts" setup>
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import {
  applyInstall,
  beginInstall,
  completeInstall,
  fetchInstallStatus,
  type InstallState,
} from '#/api/configcenter/install';

const router = useRouter();
const status = ref<InstallState | null>(null);
const token = ref('');
const owner = ref('');
const overridesText = ref('');
const busy = ref(false);
const message = ref('');
const error = ref('');

async function refresh() {
  error.value = '';
  try {
    status.value = await fetchInstallStatus();
    if (status.value.status === 'ready') {
      message.value = '安装已完成，可返回登录。';
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

async function runInstall() {
  busy.value = true;
  error.value = '';
  message.value = '';
  try {
    let overrides: Record<string, string> = {};
    const raw = overridesText.value.trim();
    if (raw) {
      overrides = JSON.parse(raw) as Record<string, string>;
    }
    const begun = await beginInstall(token.value, owner.value || undefined);
    owner.value = begun.owner || owner.value;
    const applied = await applyInstall(token.value, owner.value, overrides);
    message.value = `已播种 ${applied.item_count} 个键 / ${applied.namespaces} 个 namespace`;
    status.value = await completeInstall(token.value, owner.value);
    message.value = '安装完成（status=ready）';
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
    await refresh();
  } finally {
    busy.value = false;
  }
}

onMounted(refresh);
</script>

<template>
  <Page
    title="配置中心首次安装"
    description="ADR-0017：经 admin-api 代理调用 config-api（mTLS）；使用 setup token 完成播种"
  >
    <div class="mx-auto max-w-xl space-y-4 p-6">
      <div class="rounded border p-4 text-sm">
        <div>环境：{{ status?.env || '—' }}</div>
        <div>状态：{{ status?.status || '—' }}</div>
        <div v-if="status?.owner">持锁：{{ status.owner }}</div>
        <div v-if="status?.last_error" class="text-red-600">错误：{{ status.last_error }}</div>
      </div>

      <label class="block text-sm">
        Setup Token
        <input
          v-model="token"
          class="mt-1 w-full rounded border px-3 py-2"
          type="password"
          autocomplete="off"
          placeholder="CONFIG_INSTALL_SETUP_TOKEN"
        />
      </label>

      <label class="block text-sm">
        Owner（可选，begin 后回填）
        <input v-model="owner" class="mt-1 w-full rounded border px-3 py-2" type="text" />
      </label>

      <label class="block text-sm">
        Overrides JSON（可选，覆盖默认/overlay）
        <textarea
          v-model="overridesText"
          class="mt-1 w-full rounded border px-3 py-2 font-mono text-xs"
          rows="6"
          placeholder='{"storage.driver":"s3"}'
        ></textarea>
      </label>

      <div class="flex gap-2">
        <button
          class="rounded bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
          type="button"
          :disabled="busy || !token"
          @click="runInstall"
        >
          开始并完成安装
        </button>
        <button class="rounded border px-4 py-2" type="button" :disabled="busy" @click="refresh">
          刷新状态
        </button>
        <button class="rounded border px-4 py-2" type="button" @click="router.push('/auth/login')">
          去登录
        </button>
      </div>

      <p v-if="message" class="text-sm text-green-700">{{ message }}</p>
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
    </div>
  </Page>
</template>
