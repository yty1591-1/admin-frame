<script setup lang="ts">
import type { VbenFormSchema } from '@vben/app-kernel/adapter/form';
import type { Recordable } from '@vben/types';

import type { MeView } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { ProfileBaseSetting } from '@vben/common-ui';
import { useUserStore } from '@vben/stores';

import { Alert, Button, message } from 'ant-design-vue';

import { getMeApi, updateMyProfileApi } from '#/api';
import { $t } from '#/locales';

const userStore = useUserStore();
const profileBaseSettingRef = ref();
const me = ref<MeView | null>(null);
const loading = ref(true);
const loadFailed = ref(false);
const submitting = ref(false);

const STATUS_LABEL_KEYS = {
  active: 'profile.statusActive',
  disabled: 'profile.statusDisabled',
  locked: 'profile.statusLocked',
  pending: 'profile.statusPending',
} as const;

const CHANNEL_LABEL_KEYS = {
  admin: 'profile.channelAdmin',
  biz: 'profile.channelBiz',
} as const;

/** 只读资料项：值在这里完成格式化，共享组件不猜业务语义。 */
const infoItems = computed(() => {
  const data = me.value;
  if (!data) {
    return [];
  }
  const roles = userStore.userInfo?.roles ?? [];
  return [
    {
      label: $t('profile.loginName'),
      value: data.subject,
      description: $t('profile.loginNameImmutable'),
    },
    {
      label: $t('profile.accountStatus'),
      value: $t(STATUS_LABEL_KEYS[data.status]),
    },
    {
      label: $t('profile.channel'),
      value: $t(CHANNEL_LABEL_KEYS[data.channel]),
    },
    {
      label: $t('profile.merchantId'),
      // B 端账号必有商户；空串只可能出现在脏数据上，展示 `-` 更明确。
      value: data.merchant_id || '-',
    },
    {
      label: $t('profile.role'),
      value: roles.length > 0 ? roles.join('、') : $t('profile.roleEmpty'),
    },
  ];
});

const formSchema = computed((): VbenFormSchema[] => [
  {
    fieldName: 'displayName',
    component: 'Input',
    label: $t('profile.displayName'),
    componentProps: {
      maxlength: 64,
      placeholder: $t('profile.displayNamePlaceholder'),
    },
  },
]);

/**
 * 拉取资料。
 *
 * 失败时保持空态并给出重试入口：不能把请求失败渲染成「昵称是登录名」，
 * 否则用户会误以为自己没有设置过昵称。
 */
async function loadMe() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const data = await getMeApi({ skipErrorMessage: true });
    me.value = data;
    await profileBaseSettingRef.value
      ?.getFormApi()
      .setValues({ displayName: data.display_name });
  } catch {
    me.value = null;
    loadFailed.value = true;
  } finally {
    loading.value = false;
  }
}

onMounted(loadMe);

/**
 * 保存昵称。
 *
 * 服务端已返回更新后的完整资料，直接用它刷新本地状态并同步 userStore，
 * 顶部 header 与左侧身份卡无需刷新页面即可生效。
 */
async function handleSubmit(values: Recordable<any>) {
  const displayName = String(values.displayName ?? '').trim();
  submitting.value = true;
  try {
    const updated = await updateMyProfileApi(
      { display_name: displayName },
      { skipErrorMessage: true },
    );
    me.value = updated;
    const current = userStore.userInfo;
    if (current) {
      userStore.setUserInfo({
        ...current,
        realName: updated.display_name || updated.subject,
      });
    }
    message.success($t('profile.updateProfileSuccess'));
  } catch (error) {
    const backendMessage = (
      error as { response?: { data?: { message?: string } } }
    )?.response?.data?.message;
    message.error(backendMessage || $t('profile.updateProfileFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>
<template>
  <div>
    <div v-if="loading" class="space-y-3">
      <div
        v-for="index in 4"
        :key="index"
        class="bg-muted h-12 animate-pulse rounded-lg"
      ></div>
    </div>

    <div
      v-else-if="loadFailed"
      class="flex flex-col items-start gap-3 rounded-xl border border-dashed p-5 sm:flex-row sm:items-center sm:justify-between"
    >
      <Alert
        class="w-full flex-1"
        :message="$t('profile.profileLoadFailed')"
        show-icon
        type="error"
      />
      <Button class="shrink-0" @click="loadMe">
        {{ $t('profile.profileRetry') }}
      </Button>
    </div>

    <ProfileBaseSetting
      v-else
      ref="profileBaseSettingRef"
      :form-schema="formSchema"
      :hint="$t('profile.displayNameHint')"
      :info-items="infoItems"
      :submitting="submitting"
      @submit="handleSubmit"
    />
  </div>
</template>
