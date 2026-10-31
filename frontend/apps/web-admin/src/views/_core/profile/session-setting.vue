<script setup lang="ts">
import type { SessionItem } from '@vben/common-ui';

import type { AuthSessionDTO } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { ProfileSessionList } from '@vben/common-ui';

import { message, Modal } from 'ant-design-vue';

import {
  listMySessionsApi,
  revokeAllMySessionsApi,
  revokeMySessionApi,
} from '#/api';
import { $t } from '#/locales';
import { useAuthStore } from '#/store';

/** 每页设备数；后端上限 100，默认 20。 */
const PAGE_SIZE = 20;

const authStore = useAuthStore();

const sessions = ref<AuthSessionDTO[]>([]);
const loading = ref(false);
const error = ref(false);
const hasMore = ref(false);
const cursor = ref('');
const revokingId = ref('');
const revokingAll = ref(false);

/** DTO（snake_case）→ 共享组件模型；共享组件不感知后端字段命名。 */
const items = computed<SessionItem[]>(() =>
  sessions.value.map((session) => ({
    id: session.id,
    current: session.current,
    status: session.status,
    createdAt: session.created_at,
    lastUsedAt: session.last_used_at,
    expiresAt: session.expires_at,
    absoluteExpiresAt: session.absolute_expires_at,
    revokedAt: session.revoked_at ?? null,
  })),
);

const currentId = computed(
  () => sessions.value.find((session) => session.current)?.id ?? '',
);

/**
 * 拉取设备会话。
 *
 * 首屏失败展示错误态与重试；「加载更多」失败只提示，不清空已展示的列表。
 */
async function fetchPage(nextCursor: string, append: boolean) {
  loading.value = true;
  if (!append) {
    error.value = false;
  }
  try {
    const data = await listMySessionsApi(
      { cursor: nextCursor || undefined, limit: PAGE_SIZE },
      { skipErrorMessage: true },
    );
    sessions.value = append ? [...sessions.value, ...data.items] : data.items;
    cursor.value = data.next_cursor ?? '';
    hasMore.value = Boolean(data.next_cursor);
  } catch {
    if (append) {
      message.error($t('profile.sessionLoadFailed'));
    } else {
      sessions.value = [];
      error.value = true;
    }
  } finally {
    loading.value = false;
  }
}

onMounted(() => fetchPage('', false));

function handleLoadMore() {
  if (!hasMore.value || loading.value) {
    return;
  }
  fetchPage(cursor.value, true);
}

function handleRetry() {
  fetchPage('', false);
}

/**
 * 撤销单台设备。
 *
 * 撤销当前设备等同登出：服务端已使当前会话失效，必须清空本地令牌并回登录页，
 * 否则页面会带着失效令牌继续请求。
 */
async function revokeSession(id: string) {
  revokingId.value = id;
  try {
    await revokeMySessionApi(id, { skipErrorMessage: true });
    const target = sessions.value.find((session) => session.id === id);
    if (target?.current) {
      message.success($t('profile.deviceRevokeSuccess'));
      await authStore.logout(false);
      return;
    }
    sessions.value = sessions.value.filter((session) => session.id !== id);
    message.success($t('profile.deviceRevokeSuccess'));
  } catch {
    message.error($t('profile.deviceRevokeFailed'));
  } finally {
    revokingId.value = '';
  }
}

function handleRevoke(id: string) {
  Modal.confirm({
    cancelText: $t('common.cancel'),
    content: $t('profile.deviceRevokeConfirm'),
    okText: $t('common.confirm'),
    title: $t('profile.deviceRevoke'),
    // onOk 返回 Promise：请求期间按钮处于 loading，避免重复提交。
    onOk: () => revokeSession(id),
  });
}

async function revokeAll() {
  revokingAll.value = true;
  try {
    await revokeAllMySessionsApi({ skipErrorMessage: true });
    message.success($t('profile.deviceRevokeAllSuccess'));
    await authStore.logout(false);
  } catch {
    message.error($t('profile.deviceRevokeAllFailed'));
  } finally {
    revokingAll.value = false;
  }
}

function handleRevokeAll() {
  Modal.confirm({
    cancelText: $t('common.cancel'),
    content: $t('profile.deviceRevokeAllConfirm'),
    okText: $t('common.confirm'),
    title: $t('profile.deviceRevokeAll'),
    onOk: () => revokeAll(),
  });
}
</script>
<template>
  <ProfileSessionList
    :current-id="currentId"
    :error="error"
    :has-more="hasMore"
    :items="items"
    :loading="loading"
    :revoking-all="revokingAll"
    :revoking-id="revokingId"
    @load-more="handleLoadMore"
    @retry="handleRetry"
    @revoke="handleRevoke"
    @revoke-all="handleRevokeAll"
  />
</template>
